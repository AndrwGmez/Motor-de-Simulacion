package copilot

import (
	"context"
	"fmt"
)

type Mock struct{}

func NewMock() *Mock { return &Mock{} }

func (mock *Mock) Name() string { return "mock" }

func (mock *Mock) Advise(_ context.Context, prompt Prompt) (Draft, error) {
	byID := make(map[string]EvidenceItem, len(prompt.Evidence))
	for _, item := range prompt.Evidence {
		byID[item.ID] = item
	}
	suggestions := make([]Suggestion, 0, 4)
	for _, item := range prompt.Evidence {
		if item.Kind != "validation" || len(suggestions) >= 2 {
			continue
		}
		action := actionFor(item, byID)
		suggestions = append(suggestions, Suggestion{
			Title: "Resolve " + fmt.Sprint(item.Facts["code"]), Explanation: item.Summary,
			Severity: "critical", Confidence: "high", EvidenceIDs: []string{item.ID}, Actions: []Action{action},
		})
	}
	if item, exists := byID["incident:root-cause"]; exists && len(suggestions) < 4 {
		runID, _ := byID["incident:summary"].Facts["runId"].(string)
		suggestions = append(suggestions, Suggestion{
			Title: "Inspect the first recorded failure", Explanation: item.Summary,
			Severity: "critical", Confidence: "high",
			EvidenceIDs: []string{"incident:root-cause", "incident:summary"},
			Actions:     []Action{{Kind: "open_incident", TargetID: stringPointer(runID), Label: "Open incident timeline"}},
		})
	}
	if item, exists := byID["analysis:reachability"]; exists && len(suggestions) < 4 {
		unreachable, _ := item.Facts["unreachableNodeIds"].([]string)
		if len(unreachable) > 0 {
			suggestions = append(suggestions, Suggestion{
				Title: "Reconnect unreachable nodes", Explanation: "Static analysis found nodes that no trigger can reach.",
				Severity: "warning", Confidence: "high", EvidenceIDs: []string{item.ID},
				Actions: []Action{{Kind: "inspect_node", TargetID: stringPointer(unreachable[0]), Label: "Inspect first unreachable node"}},
			})
		}
	}
	if item, exists := byID["diff:summary"]; exists && len(suggestions) < 4 && fmt.Sprint(item.Facts["overallImpact"]) == domainBreaking {
		suggestions = append(suggestions, Suggestion{
			Title: "Review breaking changes before publishing", Explanation: "The semantic comparison contains at least one breaking change.",
			Severity: "warning", Confidence: "high", EvidenceIDs: []string{item.ID},
			Actions: []Action{{Kind: "none", TargetID: nil, Label: "Review semantic diff"}},
		})
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, Suggestion{
			Title:       "Use the current analysis as a release baseline",
			Explanation: "No validation, reachability, incident, or breaking-diff signal was present in the supplied evidence.",
			Severity:    "info", Confidence: "high",
			EvidenceIDs: []string{"flow:summary", "analysis:topology"},
			Actions:     []Action{{Kind: "none", TargetID: nil, Label: "Keep monitoring"}},
		})
	}
	limitations := []string{"Mock provider ranks deterministic local checks and does not add model interpretation."}
	if prompt.EvidenceTruncated {
		limitations = append(limitations, "The evidence bundle was truncated to the server safety limit.")
	}
	return Draft{
		Summary:     "Evidence-grounded review of the current flow.",
		Suggestions: suggestions, Limitations: limitations,
	}, nil
}

const domainBreaking = "breaking"

func actionFor(item EvidenceItem, known map[string]EvidenceItem) Action {
	if item.NodeID != "" {
		if _, exists := known["node:"+item.NodeID]; !exists {
			return Action{Kind: "none", TargetID: nil, Label: "Review validation details"}
		}
		return Action{Kind: "inspect_node", TargetID: stringPointer(item.NodeID), Label: "Inspect node"}
	}
	if item.EdgeID != "" {
		if _, exists := known["edge:"+item.EdgeID]; !exists {
			return Action{Kind: "none", TargetID: nil, Label: "Review validation details"}
		}
		return Action{Kind: "inspect_edge", TargetID: stringPointer(item.EdgeID), Label: "Inspect edge"}
	}
	return Action{Kind: "none", TargetID: nil, Label: "Review validation details"}
}

func stringPointer(value string) *string { return &value }
