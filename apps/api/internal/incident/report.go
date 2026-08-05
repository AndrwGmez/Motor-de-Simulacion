package incident

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

type TimelineFrame struct {
	Sequence      int64          `json:"sequence"`
	OccurredAt    time.Time      `json:"occurredAt"`
	LogicalTimeMS int64          `json:"logicalTimeMs"`
	Type          string         `json:"type"`
	Category      string         `json:"category"`
	NodeID        string         `json:"nodeId,omitempty"`
	EdgeID        string         `json:"edgeId,omitempty"`
	Message       string         `json:"message,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
}

type RootCause struct {
	Sequence int64  `json:"sequence"`
	Type     string `json:"type"`
	NodeID   string `json:"nodeId,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

type Integrity struct {
	Complete      bool    `json:"complete"`
	FirstSequence int64   `json:"firstSequence"`
	LastSequence  int64   `json:"lastSequence"`
	Missing       []int64 `json:"missingSequences"`
	Duplicates    []int64 `json:"duplicateSequences"`
}

type Summary struct {
	EventCount        int      `json:"eventCount"`
	LogicalDurationMS int64    `json:"logicalDurationMs"`
	VisitedNodeIDs    []string `json:"visitedNodeIds"`
	TraversedEdgeIDs  []string `json:"traversedEdgeIds"`
	FailedNodeIDs     []string `json:"failedNodeIds"`
}

type Report struct {
	SchemaVersion  string          `json:"schemaVersion"`
	RunID          string          `json:"runId"`
	TraceID        string          `json:"traceId,omitempty"`
	FlowID         string          `json:"flowId"`
	FlowVersionID  string          `json:"flowVersionId,omitempty"`
	DefinitionETag string          `json:"definitionEtag,omitempty"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
	Error          string          `json:"error,omitempty"`
	Summary        Summary         `json:"summary"`
	Integrity      Integrity       `json:"integrity"`
	RootCause      *RootCause      `json:"rootCause,omitempty"`
	Timeline       []TimelineFrame `json:"timeline"`
}

// Build creates a deterministic, replayable incident view without duplicating
// a full node-state snapshot for every event. Clients apply the ordered deltas
// in Timeline to travel to any sequence.
func Build(run domain.Run) Report {
	events := append([]domain.Event(nil), run.Events...)
	sort.SliceStable(events, func(left, right int) bool {
		if events[left].Sequence != events[right].Sequence {
			return events[left].Sequence < events[right].Sequence
		}
		return events[left].OccurredAt.Before(events[right].OccurredAt)
	})
	report := Report{
		SchemaVersion:  domain.SchemaVersion,
		RunID:          run.ID,
		TraceID:        run.TraceID,
		FlowID:         run.FlowID,
		FlowVersionID:  run.VersionID,
		DefinitionETag: run.DefinitionETag,
		Status:         run.Status,
		CreatedAt:      run.CreatedAt,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
		Error:          run.Error,
		Timeline:       make([]TimelineFrame, 0, len(events)),
		Integrity: Integrity{
			Complete:   true,
			Missing:    []int64{},
			Duplicates: []int64{},
		},
		Summary: Summary{
			VisitedNodeIDs:   []string{},
			TraversedEdgeIDs: []string{},
			FailedNodeIDs:    []string{},
		},
	}
	visited, traversed, failed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenSequences := map[int64]bool{}
	var expected int64 = 1
	for _, event := range events {
		if report.Integrity.FirstSequence == 0 {
			report.Integrity.FirstSequence = event.Sequence
		}
		if seenSequences[event.Sequence] {
			report.Integrity.Duplicates = append(report.Integrity.Duplicates, event.Sequence)
			report.Integrity.Complete = false
		} else {
			for expected < event.Sequence {
				report.Integrity.Missing = append(report.Integrity.Missing, expected)
				report.Integrity.Complete = false
				expected++
			}
			seenSequences[event.Sequence] = true
			if event.Sequence >= expected {
				expected = event.Sequence + 1
			}
		}
		report.Integrity.LastSequence = event.Sequence
		if event.LogicalTimeMS > report.Summary.LogicalDurationMS {
			report.Summary.LogicalDurationMS = event.LogicalTimeMS
		}
		frame := frameFromEvent(event)
		report.Timeline = append(report.Timeline, frame)
		if event.Type == "node.started" && frame.NodeID != "" && !visited[frame.NodeID] {
			visited[frame.NodeID] = true
			report.Summary.VisitedNodeIDs = append(report.Summary.VisitedNodeIDs, frame.NodeID)
		}
		if event.Type == "edge.traversed" && frame.EdgeID != "" && !traversed[frame.EdgeID] {
			traversed[frame.EdgeID] = true
			report.Summary.TraversedEdgeIDs = append(report.Summary.TraversedEdgeIDs, frame.EdgeID)
		}
		if event.Type == "node.failed" && frame.NodeID != "" && !failed[frame.NodeID] {
			failed[frame.NodeID] = true
			report.Summary.FailedNodeIDs = append(report.Summary.FailedNodeIDs, frame.NodeID)
		}
		if report.RootCause == nil && (event.Type == "node.failed" || event.Type == "run.failed" ||
			event.Type == "run.limit_exceeded" || event.Type == "run.interrupted") {
			report.RootCause = rootCauseFromFrame(frame, run.Error)
		}
	}
	report.Summary.EventCount = len(events)
	if len(events) == 0 {
		report.Integrity.Complete = false
	}
	if report.RootCause == nil && run.Error != "" {
		report.RootCause = &RootCause{Type: "run.error", Message: run.Error}
	}
	return report
}

func frameFromEvent(event domain.Event) TimelineFrame {
	payload := cloneMap(event.Payload)
	nodeID, _ := payload["nodeId"].(string)
	edgeID, _ := payload["edgeId"].(string)
	message := firstString(payload, "message", "error", "reason")
	return TimelineFrame{
		Sequence:      event.Sequence,
		OccurredAt:    event.OccurredAt,
		LogicalTimeMS: event.LogicalTimeMS,
		Type:          event.Type,
		Category:      eventCategory(event.Type),
		NodeID:        nodeID,
		EdgeID:        edgeID,
		Message:       message,
		Payload:       payload,
	}
}

func rootCauseFromFrame(frame TimelineFrame, fallback string) *RootCause {
	code, _ := frame.Payload["code"].(string)
	message := frame.Message
	if message == "" {
		message = fallback
	}
	if message == "" {
		message = fmt.Sprintf("Execution stopped at event %s", frame.Type)
	}
	return &RootCause{
		Sequence: frame.Sequence,
		Type:     frame.Type,
		NodeID:   frame.NodeID,
		Code:     code,
		Message:  message,
	}
}

func eventCategory(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "node."):
		return "node"
	case strings.HasPrefix(eventType, "edge."):
		return "edge"
	case eventType == "run.paused" || eventType == "run.resumed":
		return "control"
	case strings.HasPrefix(eventType, "run."):
		return "run"
	default:
		return "system"
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(value))
	for key, entry := range value {
		result[key] = entry
	}
	return result
}
