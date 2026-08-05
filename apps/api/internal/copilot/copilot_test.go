package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/incident"
)

func TestBuildEvidenceIsDeterministicAndExcludesSensitiveValues(t *testing.T) {
	definition := evidenceTestDefinition()
	report := incident.Build(domain.Run{
		ID: "run-1", FlowID: "flow-1", Status: "failed",
		Input:  map[string]any{"secret": "run-input-must-not-leak"},
		Output: map[string]any{"token": "run-output-must-not-leak"},
		Events: []domain.Event{{
			SchemaVersion: domain.SchemaVersion, Type: "node.failed", RunID: "run-1", Sequence: 1,
			Payload: map[string]any{"nodeId": "work", "error": "event-payload-must-not-leak"},
		}},
	})
	bundle := BuildEvidence(Request{FlowID: "flow-1", Definition: definition, Incident: &report})
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, secret := range []string{
		"configuration-secret-must-not-leak", "variable-default-must-not-leak",
		"run-input-must-not-leak", "run-output-must-not-leak", "event-payload-must-not-leak",
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("evidence leaked %q: %s", secret, serialized)
		}
	}
	for _, expected := range []string{"configurationKeys", "service", "node:work", "incident:root-cause"} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("evidence omitted structural fact %q: %s", expected, serialized)
		}
	}

	again, _ := json.Marshal(BuildEvidence(Request{FlowID: "flow-1", Definition: definition, Incident: &report}))
	if string(again) != serialized {
		t.Fatalf("evidence is not deterministic\nfirst:  %s\nsecond: %s", serialized, again)
	}
}

func TestGroundDropsUnknownCitationsAndRejectsUngroundedOutput(t *testing.T) {
	bundle := EvidenceBundle{Items: []EvidenceItem{
		{ID: "flow:summary", Kind: "flow", Facts: map[string]any{}},
		{ID: "node:work", Kind: "node", NodeID: "work", Facts: map[string]any{}},
	}}
	draft := Draft{Summary: "review", Suggestions: []Suggestion{
		{
			Title: "invented", Explanation: "unsupported", Severity: "warning", Confidence: "high",
			EvidenceIDs: []string{"node:missing"}, Actions: []Action{{Kind: "none", Label: "Review"}},
		},
		{
			Title: "grounded", Explanation: "supported", Severity: "info", Confidence: "high",
			EvidenceIDs: []string{"flow:summary", "flow:summary"},
			Actions:     []Action{{Kind: "inspect_node", TargetID: stringPointer("work"), Label: "Inspect"}},
		},
	}}
	grounded, err := Ground(draft, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(grounded.Suggestions) != 1 || grounded.Suggestions[0].Title != "grounded" {
		t.Fatalf("unexpected grounded suggestions: %#v", grounded.Suggestions)
	}
	if len(grounded.Suggestions[0].EvidenceIDs) != 1 {
		t.Fatalf("citations were not deduplicated: %#v", grounded.Suggestions[0].EvidenceIDs)
	}
	if !strings.Contains(strings.Join(grounded.Limitations, " "), "model interpretations") {
		t.Fatalf("semantic grounding limitation is missing: %#v", grounded.Limitations)
	}

	_, err = Ground(Draft{Suggestions: []Suggestion{draft.Suggestions[0]}}, bundle)
	if !errors.Is(err, ErrUngrounded) {
		t.Fatalf("error = %v, want ErrUngrounded", err)
	}
}

func TestServiceGroundsProviderOutput(t *testing.T) {
	provider := providerFunc(func(context.Context, Prompt) (Draft, error) {
		return Draft{Suggestions: []Suggestion{
			{Title: "bad", Explanation: "bad", Severity: "info", Confidence: "high", EvidenceIDs: []string{"made:up"}},
			{
				Title: "good", Explanation: "good", Severity: "info", Confidence: "high",
				EvidenceIDs: []string{"analysis:topology"}, Actions: []Action{{Kind: "none", Label: "Review"}},
			},
		}}, nil
	})
	response, err := NewService(provider).Advise(context.Background(), Request{
		FlowID: "flow-1", Definition: evidenceTestDefinition(), Question: "What should I improve?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "test" || len(response.Suggestions) != 1 || response.Suggestions[0].Title != "good" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

type providerFunc func(context.Context, Prompt) (Draft, error)

func (provider providerFunc) Advise(ctx context.Context, prompt Prompt) (Draft, error) {
	return provider(ctx, prompt)
}

func (providerFunc) Name() string { return "test" }

func evidenceTestDefinition() domain.FlowDefinition {
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "Evidence",
		Variables: []domain.VariableDefinition{{Path: "/token", Type: "string", Default: "variable-default-must-not-leak"}},
		Layout:    domain.Layout{Mode: "force"},
		Nodes: []domain.Node{
			{
				ID: "start", Type: domain.NodeTrigger, Label: "Start", Inputs: []domain.Port{},
				Outputs: []domain.Port{{ID: "out", Label: "Out"}}, ActivationMode: domain.ActivationEach,
				Configuration: map[string]any{"eventName": "configuration-secret-must-not-leak"},
			},
			{
				ID: "work", Type: domain.NodeIntegration, Label: "Work",
				Inputs: []domain.Port{{ID: "in", Label: "In"}}, Outputs: []domain.Port{{ID: "out", Label: "Out"}},
				ActivationMode: domain.ActivationEach,
				Configuration: map[string]any{
					"service": "configuration-secret-must-not-leak", "latencyMs": float64(20), "outcome": "success",
				},
			},
			{
				ID: "end", Type: domain.NodeEnd, Label: "End", Inputs: []domain.Port{{ID: "in", Label: "In"}},
				Outputs: []domain.Port{}, ActivationMode: domain.ActivationEach,
				Configuration: map[string]any{"result": "success"},
			},
		},
		Edges: []domain.Edge{
			{ID: "start-work", Source: "start", Target: "work", SourcePort: "out", TargetPort: "in"},
			{ID: "work-end", Source: "work", Target: "end", SourcePort: "out", TargetPort: "in"},
		},
	}
}
