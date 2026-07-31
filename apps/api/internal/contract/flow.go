package contract

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
)

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

var variableTypes = map[string]bool{
	"string": true, "number": true, "integer": true, "boolean": true,
	"object": true, "array": true, "null": true,
}

func ValidateFlow(flow domain.FlowDefinition) domain.ValidationResult {
	result := engine.Validate(flow)
	issues := append([]domain.ValidationIssue{}, result.Issues...)
	add := func(code, message, path, nodeID, edgeID string) {
		issues = append(issues, domain.ValidationIssue{
			Code: code, Message: message, Severity: domain.SeverityError,
			Path: path, NodeID: nodeID, EdgeID: edgeID,
		})
	}
	if len([]rune(flow.Name)) > 120 {
		add("contract.name_too_long", "flow name cannot exceed 120 characters", "/name", "", "")
	}
	if len([]rune(flow.Description)) > 4000 {
		add("contract.description_too_long", "flow description cannot exceed 4000 characters", "/description", "", "")
	}
	validateFlowMetadata(flow.Metadata, &issues)
	if len(flow.Variables) > 250 {
		add("contract.too_many_variables", "at most 250 variables are allowed", "/variables", "", "")
	}
	variablePaths := map[string]bool{}
	for index, variable := range flow.Variables {
		path := fmt.Sprintf("/variables/%d", index)
		if !validPointer(variable.Path) {
			add("variable.invalid_path", "variable path must be a valid JSON Pointer", path+"/path", "", "")
		}
		if variablePaths[variable.Path] {
			add("variable.duplicate_path", "variable path must be unique", path+"/path", "", "")
		}
		variablePaths[variable.Path] = true
		if !variableTypes[variable.Type] {
			add("variable.invalid_type", "variable type is not supported", path+"/type", "", "")
		}
		if len([]rune(variable.Description)) > 500 {
			add("variable.description_too_long", "variable description cannot exceed 500 characters", path+"/description", "", "")
		}
	}
	layoutModes := map[string]bool{"force": true, "directional": true, "layers": true, "timeline": true, "clusters": true, "execution": true}
	if !layoutModes[flow.Layout.Mode] {
		add("layout.invalid_mode", "layout mode is not supported", "/layout/mode", "", "")
	}
	if flow.Layout.ClusterBy != "" && flow.Layout.ClusterBy != "category" && flow.Layout.ClusterBy != "type" && flow.Layout.ClusterBy != "group" {
		add("layout.invalid_cluster", "clusterBy must be category, type or group", "/layout/clusterBy", "", "")
	}
	for index, node := range flow.Nodes {
		path := fmt.Sprintf("/nodes/%d", index)
		if !identifier.MatchString(node.ID) {
			add("node.invalid_id", "node id does not match the canonical identifier format", path+"/id", node.ID, "")
		}
		if len([]rune(node.Label)) < 1 || len([]rune(node.Label)) > 120 {
			add("node.invalid_label", "node label must contain 1 to 120 characters", path+"/label", node.ID, "")
		}
		if len([]rune(node.Description)) > 2000 {
			add("node.description_too_long", "node description cannot exceed 2000 characters", path+"/description", node.ID, "")
		}
		if len(node.Inputs) > 20 || len(node.Outputs) > 20 {
			add("node.too_many_ports", "a node can expose at most 20 inputs and 20 outputs", path, node.ID, "")
		}
		validatePorts(node.Inputs, path+"/inputs", node.ID, &issues)
		validatePorts(node.Outputs, path+"/outputs", node.ID, &issues)
		if node.DurationMS > 86400000 {
			add("node.duration_too_large", "durationMs cannot exceed 86400000", path+"/durationMs", node.ID, "")
		}
		if math.Abs(node.Position.X) > 100000 || math.Abs(node.Position.Y) > 100000 || math.Abs(node.Position.Z) > 100000 {
			add("node.position_out_of_range", "node position must stay within ±100000", path+"/position", node.ID, "")
		}
		validateConfiguration(node, path+"/configuration", &issues)
		validateNodeMetadata(node.Metadata, path+"/metadata", node.ID, &issues)
	}
	for index, edge := range flow.Edges {
		path := fmt.Sprintf("/edges/%d", index)
		if !identifier.MatchString(edge.ID) {
			add("edge.invalid_id", "edge id does not match the canonical identifier format", path+"/id", "", edge.ID)
		}
		if !identifier.MatchString(edge.SourcePort) || !identifier.MatchString(edge.TargetPort) {
			add("edge.port_required", "sourcePort and targetPort are required canonical identifiers", path, "", edge.ID)
		}
		if edge.Priority < 0 || edge.Priority > 10000 {
			add("edge.invalid_priority", "edge priority must be between 0 and 10000", path+"/priority", "", edge.ID)
		}
		if len([]rune(edge.Label)) > 120 {
			add("edge.label_too_long", "edge label cannot exceed 120 characters", path+"/label", "", edge.ID)
		}
		if edge.Condition != nil {
			validateCondition(*edge.Condition, path+"/condition", edge.ID, &issues, 0)
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		rank := map[domain.IssueSeverity]int{domain.SeverityError: 0, domain.SeverityWarning: 1, domain.SeverityInfo: 2}
		if rank[issues[i].Severity] != rank[issues[j].Severity] {
			return rank[issues[i].Severity] < rank[issues[j].Severity]
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].Path < issues[j].Path
	})
	result.Issues, result.Valid = issues, true
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			result.Valid = false
			break
		}
	}
	return result
}

func validateFlowMetadata(metadata map[string]any, issues *[]domain.ValidationIssue) {
	for key := range metadata {
		if key != "tags" && key != "createdWith" {
			appendIssue(issues, "metadata.unknown_property", "flow metadata property is not supported", "/metadata/"+key, "", "")
		}
	}
	if value, exists := metadata["createdWith"]; exists {
		text, ok := value.(string)
		if !ok || len([]rune(text)) > 80 {
			appendIssue(issues, "metadata.invalid_created_with", "createdWith must be a string of at most 80 characters", "/metadata/createdWith", "", "")
		}
	}
	validateTags(metadata["tags"], "/metadata/tags", "", issues)
}

func validateNodeMetadata(metadata map[string]any, path, nodeID string, issues *[]domain.ValidationIssue) {
	for key := range metadata {
		if key != "category" && key != "color" && key != "tags" && key != "groupId" {
			appendIssue(issues, "node_metadata.unknown_property", "node metadata property is not supported", path+"/"+key, nodeID, "")
		}
	}
	if value, exists := metadata["category"]; exists {
		text, ok := value.(string)
		if !ok || len([]rune(text)) > 80 {
			appendIssue(issues, "node_metadata.invalid_category", "category must be a string of at most 80 characters", path+"/category", nodeID, "")
		}
	}
	if value, exists := metadata["color"]; exists {
		color, ok := value.(string)
		if !ok || !hexColor.MatchString(color) {
			appendIssue(issues, "node_metadata.invalid_color", "color must use #RRGGBB format", path+"/color", nodeID, "")
		}
	}
	if value, exists := metadata["groupId"]; exists {
		groupID, ok := value.(string)
		if !ok || !identifier.MatchString(groupID) {
			appendIssue(issues, "node_metadata.invalid_group", "groupId must be a canonical identifier", path+"/groupId", nodeID, "")
		}
	}
	validateTags(metadata["tags"], path+"/tags", nodeID, issues)
}

func validateTags(raw any, path, nodeID string, issues *[]domain.ValidationIssue) {
	if raw == nil {
		return
	}
	var tags []string
	switch values := raw.(type) {
	case []any:
		for _, value := range values {
			tag, ok := value.(string)
			if !ok {
				appendIssue(issues, "metadata.invalid_tags", "tags must contain only strings", path, nodeID, "")
				return
			}
			tags = append(tags, tag)
		}
	case []string:
		tags = values
	default:
		appendIssue(issues, "metadata.invalid_tags", "tags must be an array", path, nodeID, "")
		return
	}
	if len(tags) > 20 {
		appendIssue(issues, "metadata.too_many_tags", "metadata can contain at most 20 tags", path, nodeID, "")
	}
	seen := map[string]bool{}
	for index, tag := range tags {
		if len([]rune(tag)) < 1 || len([]rune(tag)) > 40 {
			appendIssue(issues, "metadata.invalid_tag", "tag must contain 1 to 40 characters", fmt.Sprintf("%s/%d", path, index), nodeID, "")
		}
		if seen[tag] {
			appendIssue(issues, "metadata.duplicate_tag", "tags must be unique", fmt.Sprintf("%s/%d", path, index), nodeID, "")
		}
		seen[tag] = true
	}
}

func validatePorts(ports []domain.Port, path, nodeID string, issues *[]domain.ValidationIssue) {
	seen := map[string]bool{}
	for index, port := range ports {
		itemPath := fmt.Sprintf("%s/%d", path, index)
		if !identifier.MatchString(port.ID) {
			appendIssue(issues, "port.invalid_id", "port id does not match the canonical identifier format", itemPath+"/id", nodeID, "")
		}
		if seen[port.ID] {
			appendIssue(issues, "port.duplicate_id", "port id must be unique within its direction", itemPath+"/id", nodeID, "")
		}
		seen[port.ID] = true
		if len([]rune(port.Label)) < 1 || len([]rune(port.Label)) > 80 {
			appendIssue(issues, "port.invalid_label", "port label must contain 1 to 80 characters", itemPath+"/label", nodeID, "")
		}
	}
}

func validateConfiguration(node domain.Node, path string, issues *[]domain.ValidationIssue) {
	allowed := map[domain.NodeType]map[string]bool{
		domain.NodeTrigger:     {"eventName": true},
		domain.NodeProcess:     {"operations": true},
		domain.NodeDecision:    {"strategy": true},
		domain.NodeData:        {"operations": true},
		domain.NodeIntegration: {"service": true, "latencyMs": true, "outcome": true, "response": true, "errorCode": true},
		domain.NodeDelay:       {"delayMs": true},
		domain.NodeEnd:         {"result": true, "output": true},
		domain.NodeGroup:       {"collapsed": true},
	}
	for key := range node.Configuration {
		if !allowed[node.Type][key] {
			appendIssue(issues, "node.unknown_configuration", "configuration property is not valid for this node type", path+"/"+key, node.ID, "")
		}
	}
	switch node.Type {
	case domain.NodeDecision:
		strategy, ok := node.Configuration["strategy"].(string)
		if !ok || strategy != "first_match" && strategy != "all_matches" {
			appendIssue(issues, "decision.invalid_strategy", "decision strategy must be first_match or all_matches", path+"/strategy", node.ID, "")
		}
	case domain.NodeData:
		validateOperations(node, path, true, issues)
	case domain.NodeProcess:
		validateOperations(node, path, false, issues)
	case domain.NodeIntegration:
		if value, ok := integer(node.Configuration["latencyMs"]); !ok || value < 0 || value > 86400000 {
			appendIssue(issues, "integration.invalid_latency", "latencyMs must be an integer between 0 and 86400000", path+"/latencyMs", node.ID, "")
		}
		outcome, ok := node.Configuration["outcome"].(string)
		if !ok || outcome != "success" && outcome != "failure" {
			appendIssue(issues, "integration.invalid_outcome", "outcome must be success or failure", path+"/outcome", node.ID, "")
		}
	case domain.NodeDelay:
		if value, ok := integer(node.Configuration["delayMs"]); !ok || value < 0 || value > 31536000000 {
			appendIssue(issues, "delay.invalid_duration", "delayMs must be a non-negative integer", path+"/delayMs", node.ID, "")
		}
	case domain.NodeEnd:
		result, ok := node.Configuration["result"].(string)
		if !ok || result != "success" && result != "failure" {
			appendIssue(issues, "end.invalid_result", "result must be success or failure", path+"/result", node.ID, "")
		}
	}
}

func validateOperations(node domain.Node, path string, required bool, issues *[]domain.ValidationIssue) {
	raw, exists := node.Configuration["operations"]
	if !exists {
		if required {
			appendIssue(issues, "operations.required", "this node requires at least one operation", path+"/operations", node.ID, "")
		}
		return
	}
	operations, ok := raw.([]any)
	if !ok || required && len(operations) == 0 || len(operations) > 50 {
		appendIssue(issues, "operations.invalid", "operations must be an array containing 1 to 50 items", path+"/operations", node.ID, "")
		return
	}
	for index, rawOperation := range operations {
		operation, ok := rawOperation.(map[string]any)
		itemPath := fmt.Sprintf("%s/operations/%d", path, index)
		if !ok {
			appendIssue(issues, "operation.invalid", "operation must be an object", itemPath, node.ID, "")
			continue
		}
		op, _ := operation["op"].(string)
		target, _ := operation["path"].(string)
		allowedKeys := map[string]bool{"op": true, "path": true}
		if op == "set" {
			allowedKeys["value"] = true
		}
		if op == "copy" {
			allowedKeys["from"] = true
		}
		for key := range operation {
			if !allowedKeys[key] {
				appendIssue(issues, "operation.unknown_property", "operation property is not valid for this operation type", itemPath+"/"+key, node.ID, "")
			}
		}
		if (op != "set" && op != "copy" && op != "delete") || !validPointer(target) || target == "" {
			appendIssue(issues, "operation.invalid", "operation op and path are invalid", itemPath, node.ID, "")
		}
		if op == "copy" {
			source, _ := operation["from"].(string)
			if !validPointer(source) {
				appendIssue(issues, "operation.invalid_source", "copy operation requires a JSON Pointer source", itemPath+"/from", node.ID, "")
			}
		}
		if op == "set" {
			if _, exists := operation["value"]; !exists {
				appendIssue(issues, "operation.value_required", "set operation requires value", itemPath+"/value", node.ID, "")
			}
		}
	}
}

func validateCondition(condition domain.Condition, path, edgeID string, issues *[]domain.ValidationIssue, depth int) {
	if depth > 20 {
		appendIssue(issues, "condition.too_deep", "condition nesting exceeds 20 levels", path, "", edgeID)
		return
	}
	if len(condition.And) > 0 || len(condition.Or) > 0 {
		appendIssue(issues, "condition.noncanonical_group", "compound conditions must use operator and conditions", path, "", edgeID)
	}
	children := condition.Conditions
	if condition.Operator == "and" || condition.Operator == "or" {
		if len(children) < 1 || len(children) > 20 {
			appendIssue(issues, "condition.invalid_children", "compound condition requires 1 to 20 children", path+"/conditions", "", edgeID)
		}
		for index, child := range children {
			validateCondition(child, fmt.Sprintf("%s/conditions/%d", path, index), edgeID, issues, depth+1)
		}
		return
	}
	if len(condition.Conditions) > 0 {
		appendIssue(issues, "condition.unexpected_children", "comparison conditions cannot contain child conditions", path+"/conditions", "", edgeID)
	}
	if (condition.Operator == "exists" || condition.Operator == "not_exists") && condition.Value != nil {
		appendIssue(issues, "condition.unexpected_value", "existence conditions cannot contain value", path+"/value", "", edgeID)
	}
	if !validPointer(condition.Field) {
		appendIssue(issues, "condition.invalid_field", "condition field must be a JSON Pointer", path+"/field", "", edgeID)
	}
}

func validPointer(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] == '~' && (index+1 >= len(value) || value[index+1] != '0' && value[index+1] != '1') {
			return false
		}
	}
	return len(value) <= 512
}

func integer(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		if math.Trunc(number) == number {
			return int64(number), true
		}
	}
	return 0, false
}

func appendIssue(issues *[]domain.ValidationIssue, code, message, path, nodeID, edgeID string) {
	*issues = append(*issues, domain.ValidationIssue{
		Code: code, Message: message, Severity: domain.SeverityError,
		Path: path, NodeID: nodeID, EdgeID: edgeID,
	})
}
