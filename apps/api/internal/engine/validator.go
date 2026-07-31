package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flowverse/flowverse-api/internal/domain"
)

var validOperators = map[string]bool{
	"equals": true, "not_equals": true, "greater_than": true,
	"greater_than_or_equal": true, "less_than": true, "less_than_or_equal": true,
	"contains": true, "not_contains": true, "exists": true, "not_exists": true,
	"and": true, "or": true,
}

func Validate(flow domain.FlowDefinition) domain.ValidationResult {
	issues := make([]domain.ValidationIssue, 0)
	add := func(issue domain.ValidationIssue) { issues = append(issues, issue) }
	if flow.SchemaVersion != domain.SchemaVersion {
		add(domain.ValidationIssue{Code: "schema.unsupported", Severity: domain.SeverityError, Path: "/schemaVersion", Message: "schemaVersion must be " + domain.SchemaVersion})
	}
	if strings.TrimSpace(flow.Name) == "" {
		add(domain.ValidationIssue{Code: "flow.name_required", Severity: domain.SeverityError, Path: "/name", Message: "flow name is required"})
	}
	if len(flow.Nodes) > domain.MaxNodes {
		add(domain.ValidationIssue{Code: "graph.too_many_nodes", Severity: domain.SeverityError, Path: "/nodes", Message: fmt.Sprintf("at most %d nodes are allowed", domain.MaxNodes)})
	}
	if len(flow.Edges) > domain.MaxEdges {
		add(domain.ValidationIssue{Code: "graph.too_many_edges", Severity: domain.SeverityError, Path: "/edges", Message: fmt.Sprintf("at most %d edges are allowed", domain.MaxEdges)})
	}

	nodes := make(map[string]domain.Node, len(flow.Nodes))
	triggers, ends := 0, 0
	for i, node := range flow.Nodes {
		path := fmt.Sprintf("/nodes/%d", i)
		if node.ID == "" {
			add(domain.ValidationIssue{Code: "node.id_required", Severity: domain.SeverityError, Path: path + "/id", Message: "node id is required"})
			continue
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			add(domain.ValidationIssue{Code: "node.duplicate_id", Severity: domain.SeverityError, Path: path + "/id", NodeID: node.ID, Message: "node id must be unique"})
		}
		nodes[node.ID] = node
		if !node.Type.Valid() {
			add(domain.ValidationIssue{Code: "node.invalid_type", Severity: domain.SeverityError, Path: path + "/type", NodeID: node.ID, Message: "unsupported node type"})
		}
		switch node.EffectiveActivationMode() {
		case domain.ActivationEach, domain.ActivationAny, domain.ActivationAll:
		default:
			add(domain.ValidationIssue{Code: "node.invalid_activation", Severity: domain.SeverityError, Path: path + "/activationMode", NodeID: node.ID, Message: "activationMode must be each, any or all"})
		}
		if node.DurationMS < 0 {
			add(domain.ValidationIssue{Code: "node.invalid_duration", Severity: domain.SeverityError, Path: path + "/durationMs", NodeID: node.ID, Message: "duration cannot be negative"})
		}
		if node.Type == domain.NodeTrigger {
			triggers++
		}
		if node.Type == domain.NodeEnd {
			ends++
		}
	}
	if triggers == 0 {
		add(domain.ValidationIssue{Code: "graph.trigger_required", Severity: domain.SeverityError, Path: "/nodes", Message: "at least one trigger is required"})
	}
	if ends == 0 {
		add(domain.ValidationIssue{Code: "graph.end_required", Severity: domain.SeverityError, Path: "/nodes", Message: "at least one end is required"})
	}

	edgeIDs := map[string]bool{}
	outgoing := map[string][]domain.Edge{}
	incoming := map[string][]domain.Edge{}
	for i, edge := range flow.Edges {
		path := fmt.Sprintf("/edges/%d", i)
		if edge.ID == "" {
			add(domain.ValidationIssue{Code: "edge.id_required", Severity: domain.SeverityError, Path: path + "/id", Message: "edge id is required"})
		} else if edgeIDs[edge.ID] {
			add(domain.ValidationIssue{Code: "edge.duplicate_id", Severity: domain.SeverityError, Path: path + "/id", EdgeID: edge.ID, Message: "edge id must be unique"})
		}
		edgeIDs[edge.ID] = true
		source, sourceOK := nodes[edge.Source]
		target, targetOK := nodes[edge.Target]
		if !sourceOK {
			add(domain.ValidationIssue{Code: "edge.source_missing", Severity: domain.SeverityError, Path: path + "/source", EdgeID: edge.ID, Message: "source node does not exist"})
		}
		if !targetOK {
			add(domain.ValidationIssue{Code: "edge.target_missing", Severity: domain.SeverityError, Path: path + "/target", EdgeID: edge.ID, Message: "target node does not exist"})
		}
		if sourceOK && source.Type == domain.NodeGroup || targetOK && target.Type == domain.NodeGroup {
			add(domain.ValidationIssue{Code: "edge.group_not_connectable", Severity: domain.SeverityError, Path: path, EdgeID: edge.ID, Message: "group nodes cannot be connected"})
		}
		if sourceOK && edge.SourcePort != "" && !hasPort(source.Outputs, edge.SourcePort) {
			add(domain.ValidationIssue{Code: "edge.source_port_missing", Severity: domain.SeverityError, Path: path + "/sourcePort", EdgeID: edge.ID, Message: "source port does not exist"})
		}
		if targetOK && edge.TargetPort != "" && !hasPort(target.Inputs, edge.TargetPort) {
			add(domain.ValidationIssue{Code: "edge.target_port_missing", Severity: domain.SeverityError, Path: path + "/targetPort", EdgeID: edge.ID, Message: "target port does not exist"})
		}
		if edge.Condition != nil {
			validateCondition(*edge.Condition, path+"/condition", edge.ID, &issues)
		}
		if sourceOK && targetOK {
			outgoing[edge.Source] = append(outgoing[edge.Source], edge)
			incoming[edge.Target] = append(incoming[edge.Target], edge)
		}
	}

	for id, node := range nodes {
		if node.Type == domain.NodeGroup {
			continue
		}
		if len(incoming[id]) == 0 && node.Type != domain.NodeTrigger {
			add(domain.ValidationIssue{Code: "node.disconnected_input", Severity: domain.SeverityWarning, NodeID: id, Message: "node has no incoming connection"})
		}
		if len(outgoing[id]) == 0 && node.Type != domain.NodeEnd {
			severity := domain.SeverityWarning
			code := "node.no_output"
			if node.Type == domain.NodeDecision {
				severity, code = domain.SeverityError, "decision.no_output"
			}
			add(domain.ValidationIssue{Code: code, Severity: severity, NodeID: id, Message: "node has no outgoing connection"})
		}
		if node.Type == domain.NodeDecision {
			defaults := 0
			conditions := map[string]bool{}
			for _, edge := range outgoing[id] {
				if edge.Default {
					defaults++
				}
				if edge.Condition != nil {
					key := fmt.Sprintf("%#v", *edge.Condition)
					if conditions[key] {
						add(domain.ValidationIssue{Code: "decision.duplicate_condition", Severity: domain.SeverityWarning, NodeID: id, EdgeID: edge.ID, Message: "decision has a duplicate condition"})
					}
					conditions[key] = true
				}
			}
			if defaults == 0 {
				add(domain.ValidationIssue{Code: "decision.default_required", Severity: domain.SeverityError, NodeID: id, Message: "decision requires one default path"})
			} else if defaults > 1 {
				add(domain.ValidationIssue{Code: "decision.multiple_defaults", Severity: domain.SeverityError, NodeID: id, Message: "decision can have only one default path"})
			}
		}
		if node.EffectiveActivationMode() == domain.ActivationAll && len(incoming[id]) < 2 {
			add(domain.ValidationIssue{Code: "join.insufficient_inputs", Severity: domain.SeverityWarning, NodeID: id, Message: "activation all has fewer than two incoming paths"})
		}
	}

	analysis := Analyze(flow)
	for _, id := range analysis.UnreachableNodeIDs {
		if nodes[id].Type != domain.NodeGroup {
			add(domain.ValidationIssue{Code: "node.unreachable", Severity: domain.SeverityWarning, NodeID: id, Message: "node cannot be reached from a trigger"})
		}
	}
	reachableEnds := 0
	unreachable := make(map[string]bool, len(analysis.UnreachableNodeIDs))
	for _, id := range analysis.UnreachableNodeIDs {
		unreachable[id] = true
	}
	for id, node := range nodes {
		if node.Type == domain.NodeEnd && !unreachable[id] {
			reachableEnds++
		}
	}
	if ends > 0 && reachableEnds == 0 {
		add(domain.ValidationIssue{Code: "graph.no_reachable_end", Severity: domain.SeverityError, Message: "no end node is reachable from a trigger"})
	}
	for _, cycle := range analysis.Cycles {
		severity := domain.SeverityWarning
		code := "cycle.with_exit"
		message := "cycle has a potential exit and will be bounded at runtime"
		if !cycle.HasExit {
			severity, code, message = domain.SeverityError, "cycle.no_exit", "cycle has no structural exit"
		}
		add(domain.ValidationIssue{Code: code, Severity: severity, NodeID: strings.Join(cycle.NodeIDs, ","), Message: message})
	}

	sort.SliceStable(issues, func(i, j int) bool {
		rank := map[domain.IssueSeverity]int{domain.SeverityError: 0, domain.SeverityWarning: 1, domain.SeverityInfo: 2}
		if rank[issues[i].Severity] != rank[issues[j].Severity] {
			return rank[issues[i].Severity] < rank[issues[j].Severity]
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].NodeID+issues[i].EdgeID < issues[j].NodeID+issues[j].EdgeID
	})
	result := domain.ValidationResult{Valid: true, Issues: issues}
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			result.Valid = false
			break
		}
	}
	return result
}

func hasPort(ports []domain.Port, id string) bool {
	for _, port := range ports {
		if port.ID == id {
			return true
		}
	}
	return false
}

func validateCondition(condition domain.Condition, path, edgeID string, issues *[]domain.ValidationIssue) {
	operator := condition.Operator
	children := condition.Conditions
	if len(condition.And) > 0 {
		operator, children = "and", condition.And
	}
	if len(condition.Or) > 0 {
		operator, children = "or", condition.Or
	}
	if !validOperators[operator] {
		*issues = append(*issues, domain.ValidationIssue{Code: "condition.invalid_operator", Severity: domain.SeverityError, Path: path + "/operator", EdgeID: edgeID, Message: "condition operator is not supported"})
		return
	}
	if operator == "and" || operator == "or" {
		if len(children) == 0 {
			*issues = append(*issues, domain.ValidationIssue{Code: "condition.empty_group", Severity: domain.SeverityError, Path: path, EdgeID: edgeID, Message: "logical condition requires children"})
		}
		for i, child := range children {
			validateCondition(child, fmt.Sprintf("%s/conditions/%d", path, i), edgeID, issues)
		}
		return
	}
	if condition.Field == "" {
		*issues = append(*issues, domain.ValidationIssue{Code: "condition.field_required", Severity: domain.SeverityError, Path: path + "/field", EdgeID: edgeID, Message: "condition field is required"})
	}
}
