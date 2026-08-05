package engine

import (
	"reflect"
	"sort"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestRadixSortStringsMatchesGoStringOrder(t *testing.T) {
	values := []string{"z", "", "/z", "/a", "a", "aa", "a\x00", "á", "β", "node-10", "node-2"}
	want := append([]string(nil), values...)
	sort.Strings(want)

	radixSortStrings(values)

	if !reflect.DeepEqual(values, want) {
		t.Fatalf("radix order\ngot:  %#v\nwant: %#v", values, want)
	}
}

func TestDiffFlowsIgnoresTopLevelCollectionOrder(t *testing.T) {
	base := diffTestFlow()
	target := base.Clone()
	reverseVariables(target.Variables)
	reverseNodes(target.Nodes)
	reverseEdges(target.Edges)

	result := DiffFlows("flow-1", diffVersionRef("base", "checksum-a", 1), base, diffVersionRef("target", "checksum-b", 2), target)

	if len(result.Changes) != 0 {
		t.Fatalf("reordering produced changes: %#v", result.Changes)
	}
	if result.Summary.ExactMatch {
		t.Fatal("different stored checksums were reported as an exact match")
	}
	if !result.Summary.SemanticMatch || !result.Summary.BehaviorallyEquivalent {
		t.Fatalf("equivalent revisions had unexpected summary: %#v", result.Summary)
	}
	if result.Summary.OverallImpact != domain.DiffImpactNone {
		t.Fatalf("overall impact = %q, want none", result.Summary.OverallImpact)
	}
}

func TestDiffFlowsClassifiesSemanticImpact(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*domain.FlowDefinition)
		wantImpact domain.DiffImpact
		wantPath   string
	}{
		{
			name: "canvas movement is visual",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].Position.X += 80
			},
			wantImpact: domain.DiffImpactVisual,
			wantPath:   "/position",
		},
		{
			name: "duration is behavioral",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].DurationMS += 100
			},
			wantImpact: domain.DiffImpactBehavioral,
			wantPath:   "/durationMs",
		},
		{
			name: "node type is breaking",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].Type = domain.NodeDecision
			},
			wantImpact: domain.DiffImpactBreaking,
			wantPath:   "/type",
		},
		{
			name: "required variable is breaking",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Variables = append(flow.Variables, domain.VariableDefinition{Path: "/tenantId", Type: "string", Required: true})
			},
			wantImpact: domain.DiffImpactBreaking,
		},
		{
			name: "port label is visual",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].Inputs[0].Label = "Renamed"
			},
			wantImpact: domain.DiffImpactVisual,
			wantPath:   "/inputs",
		},
		{
			name: "port addition is behavioral",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].Inputs = append(flow.Nodes[0].Inputs, domain.Port{ID: "in-extra"})
			},
			wantImpact: domain.DiffImpactBehavioral,
			wantPath:   "/inputs",
		},
		{
			name: "port removal is breaking",
			mutate: func(flow *domain.FlowDefinition) {
				flow.Nodes[0].Inputs = nil
			},
			wantImpact: domain.DiffImpactBreaking,
			wantPath:   "/inputs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := diffTestFlow()
			target := base.Clone()
			test.mutate(&target)

			result := DiffFlows("flow-1", diffVersionRef("base", "a", 1), base, diffVersionRef("target", "b", 2), target)
			if result.Summary.OverallImpact != test.wantImpact {
				t.Fatalf("overall impact = %q, want %q; changes=%#v", result.Summary.OverallImpact, test.wantImpact, result.Changes)
			}
			if len(result.Changes) != 1 {
				t.Fatalf("change count = %d, want 1: %#v", len(result.Changes), result.Changes)
			}
			if result.Changes[0].Impact != test.wantImpact {
				t.Fatalf("change impact = %q, want %q", result.Changes[0].Impact, test.wantImpact)
			}
			if test.wantPath != "" {
				if len(result.Changes[0].Fields) != 1 || result.Changes[0].Fields[0].Path != test.wantPath {
					t.Fatalf("fields = %#v, want only %s", result.Changes[0].Fields, test.wantPath)
				}
			}
			wantBehavioralEquivalence := test.wantImpact == domain.DiffImpactVisual
			if result.Summary.BehaviorallyEquivalent != wantBehavioralEquivalence {
				t.Fatalf("behaviorallyEquivalent = %v, want %v", result.Summary.BehaviorallyEquivalent, wantBehavioralEquivalence)
			}
		})
	}
}

func TestDiffFlowsProducesStableEntityOrderAndCounts(t *testing.T) {
	base := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Before",
		Variables: []domain.VariableDefinition{
			{Path: "/z", Type: "string"},
			{Path: "/gone", Type: "string"},
		},
		Layout: domain.Layout{Mode: "force"},
		Nodes: []domain.Node{
			{ID: "z", Type: domain.NodeGroup, Label: "Removed group"},
			{ID: "b", Type: domain.NodeProcess, Label: "Changed", DurationMS: 1},
		},
		Edges: []domain.Edge{
			{ID: "z", Source: "b", Target: "z"},
		},
	}.Normalize()
	target := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "After",
		Variables: []domain.VariableDefinition{
			{Path: "/z", Type: "string", Description: "changed"},
			{Path: "/a", Type: "string", Required: true},
		},
		Layout: domain.Layout{Mode: "dag"},
		Nodes: []domain.Node{
			{ID: "b", Type: domain.NodeProcess, Label: "Changed", DurationMS: 2},
			{ID: "a", Type: domain.NodeGroup, Label: "New group"},
		},
		Edges: []domain.Edge{
			{ID: "a", Source: "a", Target: "b"},
		},
	}.Normalize()

	result := DiffFlows("flow-1", diffVersionRef("base", "a", 1), base, diffVersionRef("target", "b", 2), target)
	gotOrder := make([]string, 0, len(result.Changes))
	for _, change := range result.Changes {
		gotOrder = append(gotOrder, string(change.EntityType)+":"+change.EntityID)
	}
	wantOrder := []string{
		"flow:", "layout:",
		"variable:/a", "variable:/gone", "variable:/z",
		"node:a", "node:b", "node:z",
		"edge:a", "edge:z",
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("change order\ngot:  %#v\nwant: %#v", gotOrder, wantOrder)
	}
	if result.Summary.ChangeCount != len(wantOrder) {
		t.Fatalf("changeCount = %d, want %d", result.Summary.ChangeCount, len(wantOrder))
	}
	if result.Summary.ByOperation != (domain.DiffCountsByOperation{Added: 3, Removed: 3, Modified: 4}) {
		t.Fatalf("operation counts = %#v", result.Summary.ByOperation)
	}
	if result.Summary.ByEntity != (domain.DiffCountsByEntity{Flow: 1, Layout: 1, Variable: 3, Node: 3, Edge: 2}) {
		t.Fatalf("entity counts = %#v", result.Summary.ByEntity)
	}
	if result.Summary.OverallImpact != domain.DiffImpactBreaking {
		t.Fatalf("overall impact = %q, want breaking", result.Summary.OverallImpact)
	}

	reordered := target.Clone()
	reverseVariables(reordered.Variables)
	reverseNodes(reordered.Nodes)
	reverseEdges(reordered.Edges)
	second := DiffFlows("flow-1", diffVersionRef("base", "a", 1), base, diffVersionRef("target", "b", 2), reordered)
	if !reflect.DeepEqual(result, second) {
		t.Fatalf("same semantic target produced a different diff\nfirst:  %#v\nsecond: %#v", result, second)
	}
}

func diffTestFlow() domain.FlowDefinition {
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Orders",
		Variables: []domain.VariableDefinition{
			{Path: "/customerId", Type: "string"},
			{Path: "/amount", Type: "number"},
		},
		Layout: domain.Layout{Mode: "force"},
		Nodes: []domain.Node{
			{
				ID: "process", Type: domain.NodeProcess, Label: "Process",
				Inputs: []domain.Port{{ID: "in", Label: "Input"}}, Outputs: []domain.Port{{ID: "out"}},
				DurationMS: 10, Position: domain.Position{X: 10, Y: 20},
			},
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "finish", Source: "process", Target: "end"},
			{ID: "loop", Source: "process", Target: "process", Priority: 1},
		},
	}.Normalize()
}

func diffVersionRef(id, checksum string, number int) domain.FlowRevisionRef {
	return domain.FlowRevisionRef{
		Kind: domain.DiffRevisionVersion, VersionID: id, VersionNumber: number, Checksum: checksum,
	}
}

func reverseVariables(values []domain.VariableDefinition) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseNodes(values []domain.Node) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseEdges(values []domain.Edge) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
