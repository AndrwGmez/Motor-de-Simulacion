// Package copilot builds evidence-grounded recommendations for FlowVerse
// flows. Providers may rank and explain local facts, but they cannot create
// evidence: every returned suggestion is checked against the server-built
// bundle before it is exposed by the API.
package copilot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/flowverse/flowverse-api/internal/contract"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/incident"
)

const (
	SchemaVersion       = "1.0"
	maxEvidenceItems    = 600
	maxTimelineEvidence = 100
	maxValidationItems  = 200
	maxDiffItems        = 200
	maxNodeItems        = 250
	maxEdgeItems        = 150
	maxFindingIDs       = 100
)

var ErrUngrounded = errors.New("copilot response did not contain grounded suggestions")

type EvidenceItem struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Summary string         `json:"summary"`
	NodeID  string         `json:"nodeId,omitempty"`
	EdgeID  string         `json:"edgeId,omitempty"`
	Facts   map[string]any `json:"facts"`
}

type EvidenceBundle struct {
	SchemaVersion string         `json:"schemaVersion"`
	FlowID        string         `json:"flowId"`
	Items         []EvidenceItem `json:"items"`
	Truncated     bool           `json:"truncated"`
}

type Action struct {
	Kind     string  `json:"kind"`
	TargetID *string `json:"targetId"`
	Label    string  `json:"label"`
}

type Suggestion struct {
	Title       string   `json:"title"`
	Explanation string   `json:"explanation"`
	Severity    string   `json:"severity"`
	Confidence  string   `json:"confidence"`
	EvidenceIDs []string `json:"evidenceIds"`
	Actions     []Action `json:"actions"`
}

// Draft is the only shape a provider is allowed to produce. Evidence is
// attached by Service after grounding succeeds.
type Draft struct {
	Summary     string       `json:"summary"`
	Suggestions []Suggestion `json:"suggestions"`
	Limitations []string     `json:"limitations"`
}

type Prompt struct {
	Question          string         `json:"question"`
	Evidence          []EvidenceItem `json:"evidence"`
	EvidenceTruncated bool           `json:"evidenceTruncated"`
	SafetyIdentifier  string         `json:"-"`
}

type Provider interface {
	Advise(context.Context, Prompt) (Draft, error)
	Name() string
}

type Request struct {
	Question         string
	FlowID           string
	Definition       domain.FlowDefinition
	Baseline         *domain.FlowDefinition
	BaselineRef      *domain.FlowRevisionRef
	TargetRef        domain.FlowRevisionRef
	Incident         *incident.Report
	SafetyIdentifier string
}

type Response struct {
	SchemaVersion string         `json:"schemaVersion"`
	Provider      string         `json:"provider"`
	Summary       string         `json:"summary"`
	Suggestions   []Suggestion   `json:"suggestions"`
	Limitations   []string       `json:"limitations"`
	Evidence      EvidenceBundle `json:"evidence"`
}

type Service struct {
	provider Provider
}

func NewService(provider Provider) *Service {
	if provider == nil {
		provider = NewMock()
	}
	return &Service{provider: provider}
}

func (service *Service) Advise(ctx context.Context, request Request) (Response, error) {
	bundle := BuildEvidence(request)
	draft, err := service.provider.Advise(ctx, Prompt{
		Question:          strings.TrimSpace(request.Question),
		Evidence:          bundle.Items,
		EvidenceTruncated: bundle.Truncated,
		SafetyIdentifier:  request.SafetyIdentifier,
	})
	if err != nil {
		return Response{}, err
	}
	draft, err = Ground(draft, bundle)
	if err != nil {
		return Response{}, err
	}
	return Response{
		SchemaVersion: SchemaVersion,
		Provider:      service.provider.Name(),
		Summary:       draft.Summary,
		Suggestions:   draft.Suggestions,
		Limitations:   draft.Limitations,
		Evidence:      bundle,
	}, nil
}

// BuildEvidence derives a bounded, deterministic and data-minimized bundle.
// Configuration values, variable defaults, run input/output and event payloads
// are intentionally excluded.
func BuildEvidence(request Request) EvidenceBundle {
	definition := request.Definition.Normalize()
	validation := contract.ValidateFlow(definition)
	analysis := engine.Analyze(definition)
	truncated := len(analysis.UnreachableNodeIDs) > maxFindingIDs ||
		len(analysis.DisconnectedNodeIDs) > maxFindingIDs ||
		len(analysis.CriticalPathNodeIDs) > maxFindingIDs ||
		len(analysis.BottleneckNodeIDs) > maxFindingIDs
	items := []EvidenceItem{
		{
			ID: "flow:summary", Kind: "flow", Summary: "Flow structure summary",
			Facts: map[string]any{
				"nodeCount": len(definition.Nodes), "edgeCount": len(definition.Edges),
				"variableCount": len(definition.Variables), "valid": validation.Valid,
			},
		},
		{
			ID: "analysis:topology", Kind: "analysis", Summary: "Static topology analysis",
			Facts: map[string]any{
				"triggerCount": analysis.TriggerCount, "endCount": analysis.EndCount,
				"maxDepth": analysis.MaxDepth, "cyclomaticComplexity": analysis.CyclomaticComplexity,
				"pathCount": analysis.PathCount, "pathsTruncated": analysis.PathsTruncated,
			},
		},
		{
			ID: "analysis:reachability", Kind: "analysis", Summary: "Reachability findings",
			Facts: map[string]any{
				"unreachableNodeIds":    boundedStrings(analysis.UnreachableNodeIDs, maxFindingIDs),
				"unreachableNodeCount":  len(analysis.UnreachableNodeIDs),
				"disconnectedNodeIds":   boundedStrings(analysis.DisconnectedNodeIDs, maxFindingIDs),
				"disconnectedNodeCount": len(analysis.DisconnectedNodeIDs),
			},
		},
		{
			ID: "analysis:critical-path", Kind: "analysis", Summary: "Critical path and bottlenecks",
			Facts: map[string]any{
				"applies": analysis.CriticalPathApplies, "durationMs": analysis.CriticalPathMS,
				"nodeIds":             boundedStrings(analysis.CriticalPathNodeIDs, maxFindingIDs),
				"nodeCount":           len(analysis.CriticalPathNodeIDs),
				"bottleneckNodeIds":   boundedStrings(analysis.BottleneckNodeIDs, maxFindingIDs),
				"bottleneckNodeCount": len(analysis.BottleneckNodeIDs),
			},
		},
	}
	if len(analysis.Cycles) > 0 {
		cycles := make([]map[string]any, 0, len(analysis.Cycles))
		for index, cycle := range analysis.Cycles {
			if index >= 25 {
				truncated = true
				break
			}
			if len(cycle.NodeIDs) > 50 {
				truncated = true
			}
			cycles = append(cycles, map[string]any{
				"nodeIds": boundedStrings(cycle.NodeIDs, 50), "nodeCount": len(cycle.NodeIDs), "hasExit": cycle.HasExit,
			})
		}
		items = append(items, EvidenceItem{
			ID: "analysis:cycles", Kind: "analysis", Summary: "Detected cycles",
			Facts: map[string]any{"cycles": cycles},
		})
	}

	for index, issue := range validation.Issues {
		if index >= maxValidationItems {
			truncated = true
			break
		}
		items = append(items, EvidenceItem{
			ID: fmt.Sprintf("issue:%04d", index+1), Kind: "validation", Summary: issue.Message,
			NodeID: issue.NodeID, EdgeID: issue.EdgeID,
			Facts: map[string]any{"code": issue.Code, "severity": issue.Severity, "path": issue.Path},
		})
	}

	nodes := append([]domain.Node(nil), definition.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for index, node := range nodes {
		if index >= maxNodeItems {
			truncated = true
			break
		}
		configurationKeys := make([]string, 0, len(node.Configuration))
		for key := range node.Configuration {
			configurationKeys = append(configurationKeys, key)
		}
		sort.Strings(configurationKeys)
		items = append(items, EvidenceItem{
			ID: "node:" + node.ID, Kind: "node", Summary: "Flow node", NodeID: node.ID,
			Facts: map[string]any{
				"type": node.Type, "activationMode": node.EffectiveActivationMode(),
				"durationMs": node.DurationMS, "inputPortCount": len(node.Inputs),
				"outputPortCount": len(node.Outputs), "configurationKeys": configurationKeys,
			},
		})
	}

	edges := append([]domain.Edge(nil), definition.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	for index, edge := range edges {
		if index >= maxEdgeItems {
			truncated = true
			break
		}
		items = append(items, EvidenceItem{
			ID: "edge:" + edge.ID, Kind: "edge", Summary: "Flow edge", EdgeID: edge.ID,
			Facts: map[string]any{
				"source": edge.Source, "target": edge.Target, "hasCondition": edge.Condition != nil,
				"priority": edge.Priority, "isDefault": edge.Default,
			},
		})
	}

	if request.Baseline != nil && request.BaselineRef != nil {
		diff := engine.DiffFlows(request.FlowID, *request.BaselineRef, *request.Baseline, request.TargetRef, definition)
		items = append(items, EvidenceItem{
			ID: "diff:summary", Kind: "diff", Summary: "Semantic comparison summary",
			Facts: map[string]any{
				"overallImpact": diff.Summary.OverallImpact, "changeCount": diff.Summary.ChangeCount,
				"fieldChangeCount":       diff.Summary.FieldChangeCount,
				"behaviorallyEquivalent": diff.Summary.BehaviorallyEquivalent,
			},
		})
		for index, change := range diff.Changes {
			if index >= maxDiffItems {
				truncated = true
				break
			}
			items = append(items, EvidenceItem{
				ID: fmt.Sprintf("diff:%04d", index+1), Kind: "diff", Summary: "Semantic change",
				NodeID: entityNodeID(change), EdgeID: entityEdgeID(change),
				Facts: map[string]any{
					"code": change.Code, "entityType": change.EntityType, "entityId": change.EntityID,
					"operation": change.Operation, "impact": change.Impact, "fieldCount": len(change.Fields),
				},
			})
		}
	}

	if request.Incident != nil {
		report := request.Incident
		if len(report.Summary.FailedNodeIDs) > maxFindingIDs {
			truncated = true
		}
		items = append(items, EvidenceItem{
			ID: "incident:summary", Kind: "incident", Summary: "Execution incident summary",
			Facts: map[string]any{
				"runId": report.RunID, "status": report.Status, "eventCount": report.Summary.EventCount,
				"logicalDurationMs": report.Summary.LogicalDurationMS,
				"failedNodeIds":     boundedStrings(report.Summary.FailedNodeIDs, maxFindingIDs),
				"failedNodeCount":   len(report.Summary.FailedNodeIDs), "integrityComplete": report.Integrity.Complete,
			},
		})
		if report.RootCause != nil {
			items = append(items, EvidenceItem{
				ID: "incident:root-cause", Kind: "incident", Summary: "First recorded failure signal",
				NodeID: report.RootCause.NodeID,
				Facts: map[string]any{
					"sequence": report.RootCause.Sequence, "type": report.RootCause.Type, "code": report.RootCause.Code,
				},
			})
		}
		for index, frame := range report.Timeline {
			if index >= maxTimelineEvidence {
				break
			}
			items = append(items, EvidenceItem{
				ID: fmt.Sprintf("event:%d", frame.Sequence), Kind: "event", Summary: frame.Type,
				NodeID: frame.NodeID, EdgeID: frame.EdgeID,
				Facts: map[string]any{
					"sequence": frame.Sequence, "logicalTimeMs": frame.LogicalTimeMS,
					"type": frame.Type, "category": frame.Category,
				},
			})
		}
	}

	items = prioritizeEvidence(items)
	bundle := EvidenceBundle{SchemaVersion: SchemaVersion, FlowID: request.FlowID, Items: items, Truncated: truncated}
	if len(bundle.Items) > maxEvidenceItems {
		bundle.Items = bundle.Items[:maxEvidenceItems]
		bundle.Truncated = true
	}
	if request.Incident != nil && len(request.Incident.Timeline) > maxTimelineEvidence {
		bundle.Truncated = true
	}
	return bundle
}

func prioritizeEvidence(items []EvidenceItem) []EvidenceItem {
	// Keep aggregate findings, validation, diffs and incidents ahead of the
	// potentially large entity catalog. Order remains stable within each tier.
	prioritized := make([]EvidenceItem, 0, len(items))
	for _, item := range items {
		if item.Kind != "node" && item.Kind != "edge" {
			prioritized = append(prioritized, item)
		}
	}
	for _, item := range items {
		if item.Kind == "node" || item.Kind == "edge" {
			prioritized = append(prioritized, item)
		}
	}
	return prioritized
}

func Ground(draft Draft, evidence EvidenceBundle) (Draft, error) {
	known := make(map[string]EvidenceItem, len(evidence.Items))
	for _, item := range evidence.Items {
		known[item.ID] = item
	}
	grounded := make([]Suggestion, 0, len(draft.Suggestions))
	for _, suggestion := range draft.Suggestions {
		if len(grounded) >= 20 {
			break
		}
		if !validSuggestionShape(suggestion) {
			continue
		}
		seen := map[string]bool{}
		citations := make([]string, 0, len(suggestion.EvidenceIDs))
		valid := true
		for _, id := range suggestion.EvidenceIDs {
			if _, exists := known[id]; !exists {
				valid = false
				break
			}
			if !seen[id] {
				seen[id] = true
				citations = append(citations, id)
			}
		}
		if !valid || len(citations) == 0 {
			continue
		}
		suggestion.Title = strings.TrimSpace(suggestion.Title)
		suggestion.Explanation = strings.TrimSpace(suggestion.Explanation)
		suggestion.EvidenceIDs = citations
		if !groundActions(&suggestion, known) {
			continue
		}
		grounded = append(grounded, suggestion)
	}
	if len(grounded) == 0 {
		return Draft{}, ErrUngrounded
	}
	draft.Summary = trimRunes(strings.TrimSpace(draft.Summary), 2000)
	if draft.Summary == "" {
		draft.Summary = "Recommendations grounded in the supplied FlowVerse evidence."
	}
	draft.Suggestions = grounded
	providerLimit := 9
	if evidence.Truncated {
		providerLimit = 8
	}
	limitations := make([]string, 0, min(len(draft.Limitations), providerLimit))
	for _, limitation := range draft.Limitations {
		limitation = trimRunes(strings.TrimSpace(limitation), 500)
		if limitation != "" {
			limitations = appendUnique(limitations, limitation)
		}
		if len(limitations) >= providerLimit {
			break
		}
	}
	draft.Limitations = limitations
	draft.Limitations = appendUnique(draft.Limitations,
		"Recommendation narratives are model interpretations; verify each claim against the cited evidence facts.")
	if evidence.Truncated {
		draft.Limitations = appendUnique(draft.Limitations, "The evidence bundle was truncated to the server safety limit.")
	}
	return draft, nil
}

func validSuggestionShape(suggestion Suggestion) bool {
	if strings.TrimSpace(suggestion.Title) == "" || len([]rune(suggestion.Title)) > 160 ||
		strings.TrimSpace(suggestion.Explanation) == "" || len([]rune(suggestion.Explanation)) > 2000 ||
		len(suggestion.EvidenceIDs) == 0 || len(suggestion.EvidenceIDs) > 20 || len(suggestion.Actions) > 5 {
		return false
	}
	if suggestion.Severity != "info" && suggestion.Severity != "warning" && suggestion.Severity != "critical" {
		return false
	}
	return suggestion.Confidence == "low" || suggestion.Confidence == "medium" || suggestion.Confidence == "high"
}

func boundedStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func trimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func groundActions(suggestion *Suggestion, known map[string]EvidenceItem) bool {
	for index := range suggestion.Actions {
		action := &suggestion.Actions[index]
		action.Label = strings.TrimSpace(action.Label)
		if action.Label == "" || len([]rune(action.Label)) > 160 {
			return false
		}
		switch action.Kind {
		case "none":
			if action.TargetID != nil {
				return false
			}
		case "inspect_node":
			if action.TargetID == nil {
				return false
			}
			if _, exists := known["node:"+*action.TargetID]; !exists {
				return false
			}
		case "inspect_edge":
			if action.TargetID == nil {
				return false
			}
			if _, exists := known["edge:"+*action.TargetID]; !exists {
				return false
			}
		case "open_incident":
			if action.TargetID == nil {
				return false
			}
			item, exists := known["incident:summary"]
			if !exists || item.Facts["runId"] != *action.TargetID {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func entityNodeID(change domain.SemanticChange) string {
	if change.EntityType == domain.DiffEntityNode {
		return change.EntityID
	}
	return ""
}

func entityEdgeID(change domain.SemanticChange) string {
	if change.EntityType == domain.DiffEntityEdge {
		return change.EntityID
	}
	return ""
}
