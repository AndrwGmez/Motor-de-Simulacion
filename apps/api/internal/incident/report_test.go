package incident

import (
	"reflect"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestBuildCreatesReplayableRootCauseReport(t *testing.T) {
	base := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	run := domain.Run{
		ID: "run-1", FlowID: "flow-1", VersionID: "version-1", Status: "failed",
		CreatedAt: base, Error: "payment provider timeout", DefinitionETag: "etag-1",
		Events: []domain.Event{
			{Sequence: 4, Type: "run.failed", OccurredAt: base.Add(time.Second), LogicalTimeMS: 120, Payload: map[string]any{"error": "payment provider timeout"}},
			{Sequence: 1, Type: "run.started", OccurredAt: base, Payload: map[string]any{}},
			{Sequence: 2, Type: "node.started", OccurredAt: base, LogicalTimeMS: 10, Payload: map[string]any{"nodeId": "payment"}},
			{Sequence: 3, Type: "node.failed", OccurredAt: base, LogicalTimeMS: 120, Payload: map[string]any{"nodeId": "payment", "code": "provider.timeout", "message": "Provider timed out"}},
		},
	}

	report := Build(run)

	if !report.Integrity.Complete || report.Summary.EventCount != 4 || report.Summary.LogicalDurationMS != 120 {
		t.Fatalf("unexpected report summary: %+v %+v", report.Integrity, report.Summary)
	}
	if !reflect.DeepEqual(report.Summary.VisitedNodeIDs, []string{"payment"}) ||
		!reflect.DeepEqual(report.Summary.FailedNodeIDs, []string{"payment"}) {
		t.Fatalf("unexpected paths: %+v", report.Summary)
	}
	if report.RootCause == nil || report.RootCause.Sequence != 3 || report.RootCause.Code != "provider.timeout" {
		t.Fatalf("unexpected root cause: %+v", report.RootCause)
	}
	for index, frame := range report.Timeline {
		if frame.Sequence != int64(index+1) {
			t.Fatalf("timeline is not ordered: %+v", report.Timeline)
		}
	}
}

func TestBuildReportsGapsAndDuplicates(t *testing.T) {
	report := Build(domain.Run{ID: "run-2", Events: []domain.Event{
		{Sequence: 1, Type: "run.started"},
		{Sequence: 3, Type: "node.started"},
		{Sequence: 3, Type: "node.completed"},
	}})

	if report.Integrity.Complete || !reflect.DeepEqual(report.Integrity.Missing, []int64{2}) ||
		!reflect.DeepEqual(report.Integrity.Duplicates, []int64{3}) {
		t.Fatalf("unexpected integrity: %+v", report.Integrity)
	}
}

func TestBuildDoesNotMutateEventPayload(t *testing.T) {
	run := domain.Run{Events: []domain.Event{{Sequence: 1, Type: "node.failed", Payload: map[string]any{"nodeId": "a"}}}}
	report := Build(run)
	report.Timeline[0].Payload["nodeId"] = "changed"
	if run.Events[0].Payload["nodeId"] != "a" {
		t.Fatal("Build aliased the source payload")
	}
}
