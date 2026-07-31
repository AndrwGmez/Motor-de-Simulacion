package contract

import (
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestValidateCanonicalNodeConfigurations(t *testing.T) {
	base := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "Canonical", Variables: []domain.VariableDefinition{},
		Layout: domain.Layout{Mode: "directional"},
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start", Inputs: []domain.Port{}, Outputs: []domain.Port{{ID: "out", Label: "Out"}}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}},
			{ID: "data", Type: domain.NodeData, Label: "Data", Inputs: []domain.Port{{ID: "in", Label: "In"}}, Outputs: []domain.Port{{ID: "out", Label: "Out"}}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{
				"operations": []any{map[string]any{"op": "set", "path": "/ready", "value": true}},
			}},
			{ID: "end", Type: domain.NodeEnd, Label: "End", Inputs: []domain.Port{{ID: "in", Label: "In"}}, Outputs: []domain.Port{}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{"result": "success"}},
		},
		Edges: []domain.Edge{
			{ID: "start_data", Source: "start", Target: "data", SourcePort: "out", TargetPort: "in"},
			{ID: "data_end", Source: "data", Target: "end", SourcePort: "out", TargetPort: "in"},
		},
	}
	if result := ValidateFlow(base); !result.Valid {
		t.Fatalf("canonical flow rejected: %+v", result.Issues)
	}
	tests := []struct {
		name string
		edit func(*domain.FlowDefinition)
		code string
	}{
		{"missing data operations", func(flow *domain.FlowDefinition) { flow.Nodes[1].Configuration = map[string]any{} }, "operations.required"},
		{"missing edge ports", func(flow *domain.FlowDefinition) { flow.Edges[0].SourcePort = "" }, "edge.port_required"},
		{"unknown config", func(flow *domain.FlowDefinition) { flow.Nodes[0].Configuration["javascript"] = "alert(1)" }, "node.unknown_configuration"},
		{"unknown metadata", func(flow *domain.FlowDefinition) { flow.Metadata = map[string]any{"script": "alert(1)"} }, "metadata.unknown_property"},
		{"invalid node color", func(flow *domain.FlowDefinition) { flow.Nodes[0].Metadata = map[string]any{"color": "red"} }, "node_metadata.invalid_color"},
		{"invalid variable pointer", func(flow *domain.FlowDefinition) {
			flow.Variables = []domain.VariableDefinition{{Path: "not/a/pointer", Type: "string", Required: true}}
		}, "variable.invalid_path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flow := base.Clone()
			test.edit(&flow)
			result := ValidateFlow(flow)
			if result.Valid || !hasCode(result.Issues, test.code) {
				t.Fatalf("issues = %+v, want %s", result.Issues, test.code)
			}
		})
	}
}

func hasCode(issues []domain.ValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
