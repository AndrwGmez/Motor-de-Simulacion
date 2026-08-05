package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestRunIncidentTimeMachineHonorsAccessAndOrdersTimeline(t *testing.T) {
	owner, repository := newTestServer()
	owner.register(t, "incident-owner@example.com")
	projectResponse := owner.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Incidents"}, nil)
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	flowResponse := owner.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Payments"}, nil)
	var flow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(flowResponse.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	created := time.Now().UTC()
	run := domain.Run{
		ID: runID, TraceID: "00112233445566778899aabbccddeeff", ProjectID: project.ID,
		FlowID: flow.ID, Status: "failed", CreatedAt: created, Error: "gateway timeout",
		Events: []domain.Event{
			{SchemaVersion: domain.SchemaVersion, RunID: runID, Sequence: 2, Type: "node.failed", OccurredAt: created, Payload: map[string]any{"nodeId": "charge", "code": "gateway.timeout", "message": "Gateway timeout"}},
			{SchemaVersion: domain.SchemaVersion, RunID: runID, Sequence: 1, Type: "run.started", OccurredAt: created, Payload: map[string]any{}},
		},
	}
	if _, createdRun, err := repository.CreateRun(context.Background(), run, store.RunIdempotency{}); err != nil || !createdRun {
		t.Fatalf("create run: created=%v err=%v", createdRun, err)
	}

	response := owner.request(t, http.MethodGet, "/v1/runs/"+runID+"/incident", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("incident status %d: %s", response.Code, response.Body.String())
	}
	var report struct {
		RunID     string `json:"runId"`
		RootCause struct {
			Sequence int64  `json:"sequence"`
			Code     string `json:"code"`
		} `json:"rootCause"`
		Timeline []struct {
			Sequence int64 `json:"sequence"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.RunID != runID || report.RootCause.Sequence != 2 || report.RootCause.Code != "gateway.timeout" ||
		len(report.Timeline) != 2 || report.Timeline[0].Sequence != 1 {
		t.Fatalf("unexpected incident report: %+v", report)
	}

	intruder := &testClient{router: owner.router, cookies: map[string]*http.Cookie{}}
	intruder.register(t, "incident-intruder@example.com")
	denied := intruder.request(t, http.MethodGet, "/v1/runs/"+runID+"/incident", nil, nil)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-project incident status %d: %s", denied.Code, denied.Body.String())
	}
}
