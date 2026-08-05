package engine

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/flowverse/flowverse-api/internal/domain"
)

// DiffFlows compares two revisions by stable entity identity. Collection order
// is ignored for variables, nodes, and edges; output is always ordered as flow,
// layout, variables by path, nodes by ID, then edges by ID.
func DiffFlows(
	flowID string,
	baseRef domain.FlowRevisionRef,
	base domain.FlowDefinition,
	targetRef domain.FlowRevisionRef,
	target domain.FlowDefinition,
) domain.FlowDiff {
	base = base.Normalize()
	target = target.Normalize()

	changes := make([]domain.SemanticChange, 0)
	changes = append(changes, diffFlowProperties(base, target)...)
	changes = append(changes, diffLayout(base.Layout, target.Layout)...)
	changes = append(changes, diffVariables(base.Variables, target.Variables)...)
	changes = append(changes, diffNodes(base.Nodes, target.Nodes)...)
	changes = append(changes, diffEdges(base.Edges, target.Edges)...)

	return domain.FlowDiff{
		SchemaVersion:    domain.FlowDiffSchemaVersion,
		AlgorithmVersion: domain.FlowDiffAlgorithmVersion,
		FlowID:           flowID,
		Base:             baseRef,
		Target:           targetRef,
		Summary:          summarizeDiff(baseRef, targetRef, changes),
		Changes:          changes,
	}
}

func diffFlowProperties(base, target domain.FlowDefinition) []domain.SemanticChange {
	fields := make([]domain.FieldDelta, 0, 4)
	addFieldDelta(&fields, "/schemaVersion", domain.DiffImpactBreaking, base.SchemaVersion, target.SchemaVersion)
	addFieldDelta(&fields, "/name", domain.DiffImpactVisual, base.Name, target.Name)
	addFieldDelta(&fields, "/description", domain.DiffImpactVisual, base.Description, target.Description)
	addFieldDelta(&fields, "/metadata", domain.DiffImpactVisual, base.Metadata, target.Metadata)
	return modifiedChange(domain.DiffEntityFlow, "", fields)
}

func diffLayout(base, target domain.Layout) []domain.SemanticChange {
	fields := make([]domain.FieldDelta, 0, 3)
	addFieldDelta(&fields, "/mode", domain.DiffImpactVisual, base.Mode, target.Mode)
	addFieldDelta(&fields, "/clusterBy", domain.DiffImpactVisual, base.ClusterBy, target.ClusterBy)
	addFieldDelta(&fields, "/camera", domain.DiffImpactVisual, base.Camera, target.Camera)
	return modifiedChange(domain.DiffEntityLayout, "", fields)
}

func diffVariables(base, target []domain.VariableDefinition) []domain.SemanticChange {
	baseByPath := canonicalIndex(base, func(variable domain.VariableDefinition) string { return variable.Path })
	targetByPath := canonicalIndex(target, func(variable domain.VariableDefinition) string { return variable.Path })
	paths := sortedUnionKeys(baseByPath, targetByPath)
	changes := make([]domain.SemanticChange, 0)

	for _, path := range paths {
		before, hadBefore := baseByPath[path]
		after, hasAfter := targetByPath[path]
		switch {
		case !hadBefore && hasAfter:
			impact := domain.DiffImpactBehavioral
			if after.Required {
				impact = domain.DiffImpactBreaking
			}
			changes = append(changes, entityChange(domain.DiffEntityVariable, path, domain.DiffOperationAdded, impact, nil, &after))
		case hadBefore && !hasAfter:
			changes = append(changes, entityChange(domain.DiffEntityVariable, path, domain.DiffOperationRemoved, domain.DiffImpactBreaking, &before, nil))
		case hadBefore && hasAfter:
			fields := make([]domain.FieldDelta, 0, 4)
			addFieldDelta(&fields, "/type", domain.DiffImpactBreaking, before.Type, after.Type)
			requiredImpact := domain.DiffImpactBehavioral
			if !before.Required && after.Required {
				requiredImpact = domain.DiffImpactBreaking
			}
			addFieldDelta(&fields, "/required", requiredImpact, before.Required, after.Required)
			addFieldDelta(&fields, "/default", domain.DiffImpactBehavioral, before.Default, after.Default)
			addFieldDelta(&fields, "/description", domain.DiffImpactVisual, before.Description, after.Description)
			changes = append(changes, modifiedChange(domain.DiffEntityVariable, path, fields)...)
		}
	}
	return changes
}

func diffNodes(base, target []domain.Node) []domain.SemanticChange {
	baseByID := canonicalIndex(base, func(node domain.Node) string { return node.ID })
	targetByID := canonicalIndex(target, func(node domain.Node) string { return node.ID })
	ids := sortedUnionKeys(baseByID, targetByID)
	changes := make([]domain.SemanticChange, 0)

	for _, id := range ids {
		before, hadBefore := baseByID[id]
		after, hasAfter := targetByID[id]
		switch {
		case !hadBefore && hasAfter:
			impact := domain.DiffImpactBehavioral
			if after.Type == domain.NodeGroup {
				impact = domain.DiffImpactVisual
			}
			changes = append(changes, entityChange(domain.DiffEntityNode, id, domain.DiffOperationAdded, impact, nil, &after))
		case hadBefore && !hasAfter:
			impact := domain.DiffImpactBreaking
			if before.Type == domain.NodeGroup {
				impact = domain.DiffImpactVisual
			}
			changes = append(changes, entityChange(domain.DiffEntityNode, id, domain.DiffOperationRemoved, impact, &before, nil))
		case hadBefore && hasAfter:
			fields := make([]domain.FieldDelta, 0, 11)
			addFieldDelta(&fields, "/type", domain.DiffImpactBreaking, before.Type, after.Type)
			addFieldDelta(&fields, "/label", domain.DiffImpactVisual, before.Label, after.Label)
			addFieldDelta(&fields, "/description", domain.DiffImpactVisual, before.Description, after.Description)
			addFieldDelta(&fields, "/inputs", portChangeImpact(before.Inputs, after.Inputs), before.Inputs, after.Inputs)
			addFieldDelta(&fields, "/outputs", portChangeImpact(before.Outputs, after.Outputs), before.Outputs, after.Outputs)
			addFieldDelta(&fields, "/activationMode", domain.DiffImpactBehavioral, before.ActivationMode, after.ActivationMode)
			addFieldDelta(&fields, "/durationMs", domain.DiffImpactBehavioral, before.DurationMS, after.DurationMS)
			addFieldDelta(&fields, "/configuration", configurationImpact(before.Type, after.Type), before.Configuration, after.Configuration)
			addFieldDelta(&fields, "/position", domain.DiffImpactVisual, before.Position, after.Position)
			addFieldDelta(&fields, "/locked", domain.DiffImpactVisual, before.Locked, after.Locked)
			addFieldDelta(&fields, "/metadata", domain.DiffImpactVisual, before.Metadata, after.Metadata)
			changes = append(changes, modifiedChange(domain.DiffEntityNode, id, fields)...)
		}
	}
	return changes
}

func diffEdges(base, target []domain.Edge) []domain.SemanticChange {
	baseByID := canonicalIndex(base, func(edge domain.Edge) string { return edge.ID })
	targetByID := canonicalIndex(target, func(edge domain.Edge) string { return edge.ID })
	ids := sortedUnionKeys(baseByID, targetByID)
	changes := make([]domain.SemanticChange, 0)

	for _, id := range ids {
		before, hadBefore := baseByID[id]
		after, hasAfter := targetByID[id]
		switch {
		case !hadBefore && hasAfter:
			changes = append(changes, entityChange(domain.DiffEntityEdge, id, domain.DiffOperationAdded, domain.DiffImpactBehavioral, nil, &after))
		case hadBefore && !hasAfter:
			changes = append(changes, entityChange(domain.DiffEntityEdge, id, domain.DiffOperationRemoved, domain.DiffImpactBreaking, &before, nil))
		case hadBefore && hasAfter:
			fields := make([]domain.FieldDelta, 0, 8)
			addFieldDelta(&fields, "/source", domain.DiffImpactBreaking, before.Source, after.Source)
			addFieldDelta(&fields, "/target", domain.DiffImpactBreaking, before.Target, after.Target)
			addFieldDelta(&fields, "/sourcePort", domain.DiffImpactBreaking, before.SourcePort, after.SourcePort)
			addFieldDelta(&fields, "/targetPort", domain.DiffImpactBreaking, before.TargetPort, after.TargetPort)
			addFieldDelta(&fields, "/label", domain.DiffImpactVisual, before.Label, after.Label)
			addFieldDelta(&fields, "/condition", domain.DiffImpactBehavioral, before.Condition, after.Condition)
			addFieldDelta(&fields, "/priority", domain.DiffImpactBehavioral, before.Priority, after.Priority)
			addFieldDelta(&fields, "/isDefault", domain.DiffImpactBehavioral, before.Default, after.Default)
			changes = append(changes, modifiedChange(domain.DiffEntityEdge, id, fields)...)
		}
	}
	return changes
}

func entityChange[T any](
	entityType domain.DiffEntityType,
	entityID string,
	operation domain.DiffOperation,
	impact domain.DiffImpact,
	before *T,
	after *T,
) domain.SemanticChange {
	change := domain.SemanticChange{
		Code:       string(entityType) + "." + string(operation),
		EntityType: entityType,
		EntityID:   entityID,
		Operation:  operation,
		Impact:     impact,
		Fields:     []domain.FieldDelta{},
	}
	if before == nil {
		value := domain.DiffValue{Exists: false, Value: nil}
		change.Before = &value
	} else {
		value := domain.DiffValue{Exists: true, Value: *before}
		change.Before = &value
	}
	if after == nil {
		value := domain.DiffValue{Exists: false, Value: nil}
		change.After = &value
	} else {
		value := domain.DiffValue{Exists: true, Value: *after}
		change.After = &value
	}
	return change
}

func modifiedChange(entityType domain.DiffEntityType, entityID string, fields []domain.FieldDelta) []domain.SemanticChange {
	if len(fields) == 0 {
		return nil
	}
	return []domain.SemanticChange{{
		Code:       string(entityType) + "." + string(domain.DiffOperationModified),
		EntityType: entityType,
		EntityID:   entityID,
		Operation:  domain.DiffOperationModified,
		Impact:     highestFieldImpact(fields),
		Fields:     fields,
	}}
}

func addFieldDelta(fields *[]domain.FieldDelta, path string, impact domain.DiffImpact, before, after any) {
	if reflect.DeepEqual(before, after) {
		return
	}
	*fields = append(*fields, domain.FieldDelta{
		Path:   path,
		Impact: impact,
		Before: semanticValue(before),
		After:  semanticValue(after),
	})
}

func semanticValue(value any) domain.DiffValue {
	if value == nil {
		return domain.DiffValue{Exists: false, Value: nil}
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return domain.DiffValue{Exists: false, Value: nil}
		}
	}
	return domain.DiffValue{Exists: true, Value: value}
}

func portChangeImpact(base, target []domain.Port) domain.DiffImpact {
	baseIDs := make(map[string]struct{}, len(base))
	targetIDs := make(map[string]struct{}, len(target))
	for _, port := range base {
		baseIDs[port.ID] = struct{}{}
	}
	for _, port := range target {
		targetIDs[port.ID] = struct{}{}
	}
	for id := range baseIDs {
		if _, exists := targetIDs[id]; !exists {
			return domain.DiffImpactBreaking
		}
	}
	for id := range targetIDs {
		if _, exists := baseIDs[id]; !exists {
			return domain.DiffImpactBehavioral
		}
	}
	return domain.DiffImpactVisual
}

func configurationImpact(baseType, targetType domain.NodeType) domain.DiffImpact {
	if baseType == domain.NodeTrigger || targetType == domain.NodeTrigger {
		return domain.DiffImpactBreaking
	}
	if baseType == domain.NodeGroup && targetType == domain.NodeGroup {
		return domain.DiffImpactVisual
	}
	return domain.DiffImpactBehavioral
}

func highestFieldImpact(fields []domain.FieldDelta) domain.DiffImpact {
	highest := domain.DiffImpactNone
	for _, field := range fields {
		highest = higherImpact(highest, field.Impact)
	}
	return highest
}

func higherImpact(left, right domain.DiffImpact) domain.DiffImpact {
	if impactRank(right) > impactRank(left) {
		return right
	}
	return left
}

func impactRank(impact domain.DiffImpact) int {
	switch impact {
	case domain.DiffImpactVisual:
		return 1
	case domain.DiffImpactBehavioral:
		return 2
	case domain.DiffImpactBreaking:
		return 3
	default:
		return 0
	}
}

func summarizeDiff(base, target domain.FlowRevisionRef, changes []domain.SemanticChange) domain.FlowDiffSummary {
	summary := domain.FlowDiffSummary{
		ExactMatch:             base.Checksum != "" && base.Checksum == target.Checksum,
		SemanticMatch:          len(changes) == 0,
		BehaviorallyEquivalent: true,
		OverallImpact:          domain.DiffImpactNone,
		ChangeCount:            len(changes),
	}
	for _, change := range changes {
		summary.FieldChangeCount += len(change.Fields)
		summary.OverallImpact = higherImpact(summary.OverallImpact, change.Impact)
		switch change.Operation {
		case domain.DiffOperationAdded:
			summary.ByOperation.Added++
		case domain.DiffOperationRemoved:
			summary.ByOperation.Removed++
		case domain.DiffOperationModified:
			summary.ByOperation.Modified++
		}
		switch change.Impact {
		case domain.DiffImpactVisual:
			summary.ByImpact.Visual++
		case domain.DiffImpactBehavioral:
			summary.ByImpact.Behavioral++
			summary.BehaviorallyEquivalent = false
		case domain.DiffImpactBreaking:
			summary.ByImpact.Breaking++
			summary.BehaviorallyEquivalent = false
		}
		switch change.EntityType {
		case domain.DiffEntityFlow:
			summary.ByEntity.Flow++
		case domain.DiffEntityLayout:
			summary.ByEntity.Layout++
		case domain.DiffEntityVariable:
			summary.ByEntity.Variable++
		case domain.DiffEntityNode:
			summary.ByEntity.Node++
		case domain.DiffEntityEdge:
			summary.ByEntity.Edge++
		}
	}
	return summary
}

func canonicalIndex[T any](values []T, identity func(T) string) map[string]T {
	result := make(map[string]T, len(values))
	for _, value := range values {
		id := identity(value)
		current, exists := result[id]
		if !exists || canonicalLess(value, current) {
			result[id] = value
		}
	}
	return result
}

// canonicalLess makes malformed drafts with duplicate identities deterministic
// too. Valid flow definitions never need this tie-breaker.
func canonicalLess[T any](left, right T) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Compare(leftJSON, rightJSON) < 0
}

func sortedUnionKeys[T any](left, right map[string]T) []string {
	keys := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range right {
		if _, exists := seen[key]; exists {
			continue
		}
		keys = append(keys, key)
	}
	radixSortStrings(keys)
	return keys
}

// radixSortStrings orders UTF-8 bytes exactly like ordinary Go string order.
// It keeps deterministic output without adding pairwise O(n log n)
// comparisons; work is linear in the key bytes inspected.
func radixSortStrings(values []string) {
	auxiliary := make([]string, len(values))
	var sortRange func(int, int, int)
	sortRange = func(start, end, depth int) {
		if end-start < 2 {
			return
		}

		// Bucket zero is end-of-string and buckets 1..256 are byte values.
		var offsets [258]int
		for index := start; index < end; index++ {
			offsets[radixByte(values[index], depth)+2]++
		}
		for bucket := 0; bucket < 257; bucket++ {
			offsets[bucket+1] += offsets[bucket]
		}
		next := offsets
		for index := start; index < end; index++ {
			bucket := radixByte(values[index], depth) + 1
			auxiliary[next[bucket]] = values[index]
			next[bucket]++
		}
		copy(values[start:end], auxiliary[:end-start])

		for bucket := 0; bucket < 256; bucket++ {
			bucketStart := start + offsets[bucket+1]
			bucketEnd := start + offsets[bucket+2]
			if bucketEnd-bucketStart > 1 {
				sortRange(bucketStart, bucketEnd, depth+1)
			}
		}
	}
	sortRange(0, len(values), 0)
}

func radixByte(value string, depth int) int {
	if depth >= len(value) {
		return -1
	}
	return int(value[depth])
}
