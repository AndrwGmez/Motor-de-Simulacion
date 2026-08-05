package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/copilot"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestAdviseFlowBuildsGroundedDiffAndIncidentEvidence(t *testing.T) {
	fixture := newCopilotFixture(t)
	provider := &capturingCopilotProvider{draft: copilot.Draft{
		Summary: "Review",
		Suggestions: []copilot.Suggestion{{
			Title: "Inspect failure", Explanation: "The incident and diff require review.",
			Severity: "critical", Confidence: "high",
			EvidenceIDs: []string{"incident:root-cause", "diff:summary"},
			Actions:     []copilot.Action{{Kind: "open_incident", TargetID: stringReference(fixture.run.ID), Label: "Open incident"}},
		}},
	}}
	server := New(fixture.repository, auth.New(fixture.repository, auth.Config{}), parser.NewMock(), runtime.NewManager(fixture.repository), Config{CopilotProvider: provider})

	response := copilotRequest(t, server, fixture.viewer, fixture.flow.ID, map[string]any{
		"question": "What failed and what changed?", "baseVersionId": fixture.version.ID, "runId": fixture.run.ID,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if provider.prompt.SafetyIdentifier == "" || strings.Contains(provider.prompt.SafetyIdentifier, fixture.viewer.ID) {
		t.Fatalf("unsafe safety identifier %q", provider.prompt.SafetyIdentifier)
	}
	evidenceRaw, _ := json.Marshal(provider.prompt.Evidence)
	for _, expected := range []string{"diff:summary", "incident:root-cause", "incident:summary"} {
		if !strings.Contains(string(evidenceRaw), expected) {
			t.Fatalf("missing evidence %q: %s", expected, evidenceRaw)
		}
	}
	if strings.Contains(string(evidenceRaw), "sensitive-run-input") || strings.Contains(string(evidenceRaw), "sensitive-event-message") {
		t.Fatalf("runtime payload leaked to provider: %s", evidenceRaw)
	}
	var payload copilot.Response
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Provider != "capture" || len(payload.Suggestions) != 1 || len(payload.Evidence.Items) == 0 {
		t.Fatalf("unexpected response: %#v", payload)
	}
}

func TestAdviseFlowValidatesReferencesAndRejectsUngroundedProviderOutput(t *testing.T) {
	fixture := newCopilotFixture(t)
	provider := &capturingCopilotProvider{draft: copilot.Draft{Suggestions: []copilot.Suggestion{{
		Title: "Invented", Explanation: "Not supported", Severity: "info", Confidence: "high",
		EvidenceIDs: []string{"unknown:evidence"}, Actions: []copilot.Action{{Kind: "none", Label: "Review"}},
	}}}}
	server := New(fixture.repository, auth.New(fixture.repository, auth.Config{}), parser.NewMock(), runtime.NewManager(fixture.repository), Config{CopilotProvider: provider})

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{name: "short question", body: map[string]any{"question": "x"}, wantStatus: http.StatusUnprocessableEntity, wantCode: "copilot.invalid_question"},
		{name: "malformed version", body: map[string]any{"question": "review this", "baseVersionId": "bad"}, wantStatus: http.StatusBadRequest, wantCode: "copilot.invalid_reference"},
		{name: "other flow version", body: map[string]any{"question": "review this", "baseVersionId": fixture.otherVersion.ID}, wantStatus: http.StatusNotFound, wantCode: "resource.not_found"},
		{name: "ungrounded model output", body: map[string]any{"question": "review this"}, wantStatus: http.StatusBadGateway, wantCode: "copilot.ungrounded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := copilotRequest(t, server, fixture.viewer, fixture.flow.ID, test.body)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", payload.Code, test.wantCode)
			}
		})
	}
}

type copilotFixture struct {
	repository   *store.Memory
	viewer       domain.User
	flow         domain.Flow
	version      domain.FlowVersion
	otherVersion domain.FlowVersion
	run          domain.Run
}

func newCopilotFixture(t *testing.T) copilotFixture {
	t.Helper()
	ctx := context.Background()
	repository := store.NewMemory()
	owner := domain.User{ID: uuid.NewString(), Email: "copilot-owner@example.com"}
	viewer := domain.User{ID: uuid.NewString(), Email: "copilot-viewer@example.com"}
	for _, user := range []domain.User{owner, viewer} {
		if err := repository.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{ID: uuid.NewString(), Name: "Copilot", OwnerID: owner.ID}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetProjectMember(ctx, project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	base := diffHandlerDefinition(10)
	draft := diffHandlerDefinition(20)
	flow := domain.Flow{ID: uuid.NewString(), ProjectID: project.ID, Name: "Flow", Draft: draft, DraftETag: checksum(draft)}
	if err := repository.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	version := domain.FlowVersion{ID: uuid.NewString(), FlowID: flow.ID, Number: 1, Definition: base, Checksum: checksum(base), PublishedBy: owner.ID}
	if err := repository.CreateVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	otherFlow := domain.Flow{ID: uuid.NewString(), ProjectID: project.ID, Name: "Other", Draft: base, DraftETag: checksum(base)}
	if err := repository.CreateFlow(ctx, otherFlow); err != nil {
		t.Fatal(err)
	}
	otherVersion := domain.FlowVersion{ID: uuid.NewString(), FlowID: otherFlow.ID, Number: 1, Definition: base, Checksum: checksum(base), PublishedBy: owner.ID}
	if err := repository.CreateVersion(ctx, otherVersion); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{
		ID: uuid.NewString(), ProjectID: project.ID, FlowID: flow.ID, VersionID: version.ID,
		Status: "failed", Input: map[string]any{"secret": "sensitive-run-input"}, Definition: base,
		Events: []domain.Event{{
			SchemaVersion: domain.SchemaVersion, Type: "node.failed", RunID: "run", Sequence: 1,
			Payload: map[string]any{"nodeId": "process", "message": "sensitive-event-message"},
		}},
	}
	if _, _, err := repository.CreateRun(ctx, run, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	return copilotFixture{repository: repository, viewer: viewer, flow: flow, version: version, otherVersion: otherVersion, run: run}
}

type capturingCopilotProvider struct {
	prompt copilot.Prompt
	draft  copilot.Draft
	err    error
}

func (provider *capturingCopilotProvider) Name() string { return "capture" }

func (provider *capturingCopilotProvider) Advise(_ context.Context, prompt copilot.Prompt) (copilot.Draft, error) {
	provider.prompt = prompt
	return provider.draft, provider.err
}

func copilotRequest(t *testing.T, server *Server, user domain.User, flowID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(userContextKey, user)
		c.Next()
	})
	router.POST("/v1/flows/:flowId/copilot", server.adviseFlow)
	request := httptest.NewRequest(http.MethodPost, "/v1/flows/"+flowID+"/copilot", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func stringReference(value string) *string { return &value }
