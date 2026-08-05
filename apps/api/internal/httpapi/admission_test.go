package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestAdmissionGateHasStrictConcurrentCapacity(t *testing.T) {
	gate := newAdmissionGate(2)
	if !gate.acquire() || !gate.acquire() {
		t.Fatal("gate rejected work below capacity")
	}
	if gate.acquire() {
		t.Fatal("gate accepted work above capacity")
	}
	gate.release()
	if !gate.acquire() {
		t.Fatal("released capacity was not reusable")
	}
	gate.release()
	gate.release()
}

func TestAnalyzeAndRunRoutesAreRateLimitedPerAuthenticatedUser(t *testing.T) {
	server, client, flowID := newProtectedRoutesTestServer(t, "route-rate-limit@example.com")
	server.ratePolicies["flows.analyze"] = ratePolicy{Limit: 1, Window: time.Minute}
	server.ratePolicies["runs.create"] = ratePolicy{Limit: 1, Window: time.Minute}

	if response := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/analyze", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("first analysis status=%d: %s", response.Code, response.Body.String())
	}
	blockedAnalysis := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/analyze", nil, nil)
	if blockedAnalysis.Code != http.StatusTooManyRequests || blockedAnalysis.Header().Get("Retry-After") == "" {
		t.Fatalf("analysis status=%d retry=%q: %s", blockedAnalysis.Code, blockedAnalysis.Header().Get("Retry-After"), blockedAnalysis.Body.String())
	}
	assertErrorCode(t, blockedAnalysis, "rate_limit.exceeded")

	body := map[string]any{"triggerNodeId": "start", "input": map[string]any{}}
	if response := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/runs", body,
		map[string]string{"Idempotency-Key": "rate-limit-run-01"}); response.Code != http.StatusCreated {
		t.Fatalf("first run status=%d: %s", response.Code, response.Body.String())
	}
	blockedRun := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/runs", body,
		map[string]string{"Idempotency-Key": "rate-limit-run-02"})
	if blockedRun.Code != http.StatusTooManyRequests {
		t.Fatalf("run status=%d: %s", blockedRun.Code, blockedRun.Body.String())
	}
	assertErrorCode(t, blockedRun, "rate_limit.exceeded")
}

func TestAnalyzeAndRunRoutesRejectExcessConcurrentWork(t *testing.T) {
	server, client, flowID := newProtectedRoutesTestServer(t, "route-admission@example.com")

	server.admissions["flows.analyze"] = newAdmissionGate(1)
	analyzeGate := server.admissions["flows.analyze"]
	if !analyzeGate.acquire() {
		t.Fatal("could not fill analysis gate")
	}
	blockedAnalysis := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/analyze", nil, nil)
	if blockedAnalysis.Code != http.StatusServiceUnavailable || blockedAnalysis.Header().Get("Retry-After") != "1" {
		t.Fatalf("analysis status=%d retry=%q: %s", blockedAnalysis.Code, blockedAnalysis.Header().Get("Retry-After"), blockedAnalysis.Body.String())
	}
	assertErrorCode(t, blockedAnalysis, "admission.overloaded")
	analyzeGate.release()
	if response := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/analyze", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("analysis did not recover after release: %d %s", response.Code, response.Body.String())
	}

	runGate := newAdmissionGate(1)
	server.admissions["runs.create"] = runGate
	if !runGate.acquire() {
		t.Fatal("could not fill run gate")
	}
	body := map[string]any{"triggerNodeId": "start", "input": map[string]any{}}
	blockedRun := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/runs", body,
		map[string]string{"Idempotency-Key": "admission-run-01"})
	if blockedRun.Code != http.StatusServiceUnavailable || blockedRun.Header().Get("Retry-After") != "1" {
		t.Fatalf("run status=%d retry=%q: %s", blockedRun.Code, blockedRun.Header().Get("Retry-After"), blockedRun.Body.String())
	}
	assertErrorCode(t, blockedRun, "admission.overloaded")
	runGate.release()
	if response := client.request(t, http.MethodPost, "/v1/flows/"+flowID+"/runs", body,
		map[string]string{"Idempotency-Key": "admission-run-01"}); response.Code != http.StatusCreated {
		t.Fatalf("run did not recover after release: %d %s", response.Code, response.Body.String())
	}
}

func newProtectedRoutesTestServer(t *testing.T, email string) (*Server, *testClient, string) {
	t.Helper()
	repository := store.NewMemory()
	authService := auth.New(repository, auth.Config{})
	server := New(repository, authService, parser.NewMock(), runtime.NewManager(repository), Config{PublicOrigin: "http://localhost:3000"})
	client := &testClient{router: server.Router(), cookies: map[string]*http.Cookie{}}
	client.register(t, email)

	projectResponse := client.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Protected routes"}, nil)
	if projectResponse.Code != http.StatusCreated {
		t.Fatalf("project status=%d: %s", projectResponse.Code, projectResponse.Body.String())
	}
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	flowResponse := client.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Protected"}, nil)
	if flowResponse.Code != http.StatusCreated {
		t.Fatalf("flow status=%d: %s", flowResponse.Code, flowResponse.Body.String())
	}
	var flow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(flowResponse.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	return server, client, flow.ID
}
