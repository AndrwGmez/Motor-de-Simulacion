package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestRunPayloadKeepsHistoryInNormalizedTables(t *testing.T) {
	run := domain.Run{
		ID:       "run-normalized",
		Events:   []domain.Event{{Sequence: 1, Type: "run.started"}},
		NodeRuns: []domain.NodeRun{{NodeID: "start", TokenID: "token-1"}},
	}
	raw, err := marshalRunPayload(run)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"events"`)) || bytes.Contains(raw, []byte(`"nodeRuns"`)) {
		t.Fatalf("run payload contains normalized history: %s", raw)
	}
}

func TestDecodeRunMergesPartialLegacyEventHistory(t *testing.T) {
	legacy := domain.Run{ID: "run-legacy", Events: []domain.Event{
		{Sequence: 1, Type: "run.started"},
		{Sequence: 2, Type: "node.started"},
	}}
	payload, _ := json.Marshal(legacy)
	normalized, _ := json.Marshal([]domain.Event{
		{Sequence: 2, Type: "node.completed"},
		{Sequence: 3, Type: "run.completed"},
	})
	run, err := decodeRun(payload, legacy.Status, nil, nil, nil, "", normalized, []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Events) != 3 || run.Events[0].Type != "run.started" ||
		run.Events[1].Type != "node.completed" || run.Events[2].Type != "run.completed" {
		t.Fatalf("merged events = %+v", run.Events)
	}
}

func TestMemoryAppendRunEventRequiresNextSequence(t *testing.T) {
	repository := NewMemory()
	run := domain.Run{ID: "run-sequence", Status: "created", CreatedAt: time.Now().UTC()}
	if _, _, err := repository.CreateRun(context.Background(), run, RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	event := domain.Event{RunID: run.ID, Sequence: 2, Type: "run.started", OccurredAt: time.Now().UTC()}
	if err := repository.AppendRunEvent(context.Background(), run, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("gap append error = %v, want ErrConflict", err)
	}
	stored, err := repository.RunByID(context.Background(), run.ID)
	if err != nil || len(stored.Events) != 0 {
		t.Fatalf("rejected event became visible: run=%+v err=%v", stored, err)
	}
}
