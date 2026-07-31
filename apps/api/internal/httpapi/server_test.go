package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

type testClient struct {
	router  http.Handler
	cookies map[string]*http.Cookie
	csrf    string
}

func newTestServer() (*testClient, *store.Memory) {
	repository := store.NewMemory()
	authService := auth.New(repository, auth.Config{})
	manager := runtime.NewManager(repository)
	server := New(repository, authService, parser.NewMock(), manager, Config{PublicOrigin: "http://localhost:3000"})
	return &testClient{router: server.Router(), cookies: map[string]*http.Cookie{}}, repository
}

func (client *testClient) request(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if method != http.MethodGet && client.csrf != "" {
		request.Header.Set("X-CSRF-Token", client.csrf)
	}
	for _, cookie := range client.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	client.router.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		client.cookies[cookie.Name] = cookie
	}
	return recorder
}

func (client *testClient) register(t *testing.T, email string) {
	t.Helper()
	response := client.request(t, http.MethodPost, "/v1/auth/register", map[string]any{
		"email": email, "password": "very secure password", "displayName": "Test User",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register status %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		CSRF string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	client.csrf = payload.CSRF
	if client.cookies["flowverse_access"] == nil || client.cookies["flowverse_refresh"] == nil {
		t.Fatal("canonical session cookies were not set")
	}
}

func TestCanonicalHealthAndDraftLifecycle(t *testing.T) {
	client, _ := newTestServer()
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := client.request(t, http.MethodGet, path, nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
	client.register(t, "owner@example.com")
	refreshResponse := client.request(t, http.MethodPost, "/v1/auth/refresh", nil, nil)
	if refreshResponse.Code != http.StatusOK || !strings.Contains(refreshResponse.Body.String(), `"displayName":"Test User"`) {
		t.Fatalf("refresh status %d: %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshed struct {
		CSRF string `json:"csrfToken"`
	}
	_ = json.Unmarshal(refreshResponse.Body.Bytes(), &refreshed)
	client.csrf = refreshed.CSRF
	projectResponse := client.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Demo"}, nil)
	if projectResponse.Code != http.StatusCreated {
		t.Fatalf("project status %d: %s", projectResponse.Code, projectResponse.Body.String())
	}
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	flowResponse := client.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Orders"}, nil)
	if flowResponse.Code != http.StatusCreated {
		t.Fatalf("flow status %d: %s", flowResponse.Code, flowResponse.Body.String())
	}
	var summary struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(flowResponse.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	draftResponse := client.request(t, http.MethodGet, "/v1/flows/"+summary.ID+"/draft", nil, nil)
	if draftResponse.Code != http.StatusOK {
		t.Fatalf("draft status %d: %s", draftResponse.Code, draftResponse.Body.String())
	}
	etag := draftResponse.Header().Get("ETag")
	if etag == "" || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("invalid ETag %q", etag)
	}
	var draft domain.FlowDefinition
	if err := json.Unmarshal(draftResponse.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Name = "Orders v2"
	saveResponse := client.request(t, http.MethodPut, "/v1/flows/"+summary.ID+"/draft", draft, map[string]string{"If-Match": etag})
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("save status %d: %s", saveResponse.Code, saveResponse.Body.String())
	}
	nextETag := saveResponse.Header().Get("ETag")
	if nextETag == "" || nextETag == etag {
		t.Fatalf("ETag was not advanced: old=%s new=%s", etag, nextETag)
	}
	staleResponse := client.request(t, http.MethodPut, "/v1/flows/"+summary.ID+"/draft", draft, map[string]string{"If-Match": etag})
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale status %d: %s", staleResponse.Code, staleResponse.Body.String())
	}
	var apiError map[string]any
	_ = json.Unmarshal(staleResponse.Body.Bytes(), &apiError)
	if apiError["code"] != "draft.conflict" || apiError["error"] != nil {
		t.Fatalf("non-canonical error: %#v", apiError)
	}
	publish := client.request(t, http.MethodPost, "/v1/flows/"+summary.ID+"/publish", nil, map[string]string{"If-Match": nextETag})
	if publish.Code != http.StatusCreated {
		t.Fatalf("publish status %d: %s", publish.Code, publish.Body.String())
	}
	var published struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(publish.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	duplicateShare := client.request(t, http.MethodPost, "/v1/flows/"+summary.ID+"/share-links", map[string]any{
		"versionId": published.ID, "runIds": []string{"duplicate", "duplicate"},
	}, nil)
	if duplicateShare.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate share status %d: %s", duplicateShare.Code, duplicateShare.Body.String())
	}
	tooManyRunIDs := make([]string, 21)
	for index := range tooManyRunIDs {
		tooManyRunIDs[index] = "run-" + strings.Repeat("x", index+1)
	}
	tooManyShare := client.request(t, http.MethodPost, "/v1/flows/"+summary.ID+"/share-links", map[string]any{
		"versionId": published.ID, "runIds": tooManyRunIDs,
	}, nil)
	if tooManyShare.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized share status %d: %s", tooManyShare.Code, tooManyShare.Body.String())
	}
	shareResponse := client.request(t, http.MethodPost, "/v1/flows/"+summary.ID+"/share-links", map[string]any{
		"versionId": published.ID, "runIds": []string{},
	}, nil)
	if shareResponse.Code != http.StatusCreated {
		t.Fatalf("share status %d: %s", shareResponse.Code, shareResponse.Body.String())
	}
	var shared struct {
		Token     string `json:"token"`
		PublicURL string `json:"publicUrl"`
	}
	_ = json.Unmarshal(shareResponse.Body.Bytes(), &shared)
	if shared.PublicURL != "http://localhost:3000/compartir/"+shared.Token {
		t.Fatalf("publicUrl = %q", shared.PublicURL)
	}
	if public := client.request(t, http.MethodGet, "/public/v1/shares/"+shared.Token, nil, nil); public.Code != http.StatusOK {
		t.Fatalf("public share status %d: %s", public.Code, public.Body.String())
	}
	draftRun := client.request(t, http.MethodPost, "/v1/flows/"+summary.ID+"/runs", map[string]any{
		"triggerNodeId": "start", "input": map[string]any{},
	}, map[string]string{"Idempotency-Key": "draft-run-test"})
	if draftRun.Code != http.StatusCreated {
		t.Fatalf("draft run status %d: %s", draftRun.Code, draftRun.Body.String())
	}
	var runPayload map[string]any
	_ = json.Unmarshal(draftRun.Body.Bytes(), &runPayload)
	if runPayload["flowVersionId"] != "" || runPayload["status"] != "queued" && runPayload["status"] != "running" {
		t.Fatalf("draft run is not an immutable snapshot response: %#v", runPayload)
	}
	if deleted := client.request(t, http.MethodDelete, "/v1/flows/"+summary.ID, nil, nil); deleted.Code != http.StatusNoContent {
		t.Fatalf("delete flow status %d: %s", deleted.Code, deleted.Body.String())
	}
	if public := client.request(t, http.MethodGet, "/public/v1/shares/"+shared.Token, nil, nil); public.Code != http.StatusNotFound {
		t.Fatalf("deleted-flow share status %d: %s", public.Code, public.Body.String())
	}
}

func TestProjectIsolationReturnsNotFound(t *testing.T) {
	owner, _ := newTestServer()
	owner.register(t, "first@example.com")
	projectResponse := owner.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Private"}, nil)
	var project domain.Project
	_ = json.Unmarshal(projectResponse.Body.Bytes(), &project)
	flowResponse := owner.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Secret"}, nil)
	var flow struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(flowResponse.Body.Bytes(), &flow)

	// Use a second client against the same router/repository.
	intruder := &testClient{router: owner.router, cookies: map[string]*http.Cookie{}}
	intruder.register(t, "second@example.com")
	response := intruder.request(t, http.MethodGet, "/v1/flows/"+flow.ID+"/draft", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-project read status %d: %s", response.Code, response.Body.String())
	}
}

func TestRunCreationIdempotencyContract(t *testing.T) {
	owner, repository := newTestServer()
	owner.register(t, "idempotency-owner@example.com")

	projectResponse := owner.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Idempotency"}, nil)
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	flowResponse := owner.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": "Runs"}, nil)
	var flow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(flowResponse.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	publishVersion := func() string {
		t.Helper()
		response := owner.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/versions", nil, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("publish status %d: %s", response.Code, response.Body.String())
		}
		var version struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &version); err != nil {
			t.Fatal(err)
		}
		return version.ID
	}
	versionOne := publishVersion()
	body := map[string]any{"triggerNodeId": "start", "input": map[string]any{"order": "A"}}

	for _, path := range []string{
		"/v1/flow-versions/" + versionOne + "/runs",
		"/v1/flows/" + flow.ID + "/runs",
	} {
		response := owner.request(t, http.MethodPost, path, body, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("missing key for %s status %d: %s", path, response.Code, response.Body.String())
		}
		assertErrorCode(t, response, "idempotency.key_required")
	}
	for _, invalidKey := range []string{strings.Repeat("x", 7), strings.Repeat("x", 129), " invalid-key "} {
		response := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs", body,
			map[string]string{"Idempotency-Key": invalidKey})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid key length=%d status %d: %s", len(invalidKey), response.Code, response.Body.String())
		}
		assertErrorCode(t, response, "idempotency.key_invalid")
	}
	for _, validKey := range []string{strings.Repeat("a", 8), strings.Repeat("b", 128)} {
		response := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs", body,
			map[string]string{"Idempotency-Key": validKey})
		if response.Code != http.StatusCreated {
			t.Fatalf("valid key length=%d status %d: %s", len(validKey), response.Code, response.Body.String())
		}
	}

	const key = "order-request-0001"
	first := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if first.Code != http.StatusCreated {
		t.Fatalf("first version run status %d: %s", first.Code, first.Body.String())
	}
	firstID := responseID(t, first)
	replay := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if replay.Code != http.StatusOK || responseID(t, replay) != firstID {
		t.Fatalf("version replay status=%d id=%q want=%q: %s", replay.Code, responseID(t, replay), firstID, replay.Body.String())
	}
	mismatch := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs",
		map[string]any{"triggerNodeId": "start", "input": map[string]any{"order": "B"}},
		map[string]string{"Idempotency-Key": key})
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("version mismatch status %d: %s", mismatch.Code, mismatch.Body.String())
	}
	assertErrorCode(t, mismatch, "idempotency.payload_mismatch")

	versionTwo := publishVersion()
	otherVersion := owner.request(t, http.MethodPost, "/v1/flow-versions/"+versionTwo+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if otherVersion.Code != http.StatusCreated || responseID(t, otherVersion) == firstID {
		t.Fatalf("other version status=%d id=%q original=%q: %s",
			otherVersion.Code, responseID(t, otherVersion), firstID, otherVersion.Body.String())
	}

	draftFirst := owner.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if draftFirst.Code != http.StatusCreated {
		t.Fatalf("first draft run status %d: %s", draftFirst.Code, draftFirst.Body.String())
	}
	draftFirstID := responseID(t, draftFirst)
	draftReplay := owner.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if draftReplay.Code != http.StatusOK || responseID(t, draftReplay) != draftFirstID {
		t.Fatalf("draft replay status=%d id=%q want=%q: %s",
			draftReplay.Code, responseID(t, draftReplay), draftFirstID, draftReplay.Body.String())
	}
	draftMismatch := owner.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/runs",
		map[string]any{"triggerNodeId": "start", "input": map[string]any{"order": "C"}},
		map[string]string{"Idempotency-Key": key})
	if draftMismatch.Code != http.StatusConflict {
		t.Fatalf("draft mismatch status %d: %s", draftMismatch.Code, draftMismatch.Body.String())
	}
	assertErrorCode(t, draftMismatch, "idempotency.payload_mismatch")

	draftResponse := owner.request(t, http.MethodGet, "/v1/flows/"+flow.ID+"/draft", nil, nil)
	var draft domain.FlowDefinition
	if err := json.Unmarshal(draftResponse.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Name = "Runs changed"
	saved := owner.request(t, http.MethodPut, "/v1/flows/"+flow.ID+"/draft", draft,
		map[string]string{"If-Match": draftResponse.Header().Get("ETag")})
	if saved.Code != http.StatusOK {
		t.Fatalf("save new draft revision status %d: %s", saved.Code, saved.Body.String())
	}
	newRevisionRun := owner.request(t, http.MethodPost, "/v1/flows/"+flow.ID+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if newRevisionRun.Code != http.StatusCreated || responseID(t, newRevisionRun) == draftFirstID {
		t.Fatalf("new draft revision status=%d id=%q original=%q: %s",
			newRevisionRun.Code, responseID(t, newRevisionRun), draftFirstID, newRevisionRun.Body.String())
	}

	editor := &testClient{router: owner.router, cookies: map[string]*http.Cookie{}}
	editor.register(t, "idempotency-editor@example.com")
	editorUser, err := repository.UserByEmail(context.Background(), "idempotency-editor@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetProjectMember(context.Background(), project.ID, editorUser.ID, domain.RoleEditor); err != nil {
		t.Fatal(err)
	}
	otherUser := editor.request(t, http.MethodPost, "/v1/flow-versions/"+versionOne+"/runs", body,
		map[string]string{"Idempotency-Key": key})
	if otherUser.Code != http.StatusCreated || responseID(t, otherUser) == firstID {
		t.Fatalf("other user status=%d id=%q original=%q: %s",
			otherUser.Code, responseID(t, otherUser), firstID, otherUser.Body.String())
	}
}

func responseID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.ID
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != expected {
		t.Fatalf("error code %q, want %q: %s", payload.Code, expected, response.Body.String())
	}
}

func TestCanonicalRunRequestHashIgnoresObjectOrder(t *testing.T) {
	first := simulationRequest{
		TriggerNodeID: "start",
		Input:         map[string]any{"customer": map[string]any{"name": "Ada", "id": float64(7)}, "paid": true},
	}
	second := simulationRequest{
		TriggerNodeID: "start",
		Input:         map[string]any{"paid": true, "customer": map[string]any{"id": float64(7), "name": "Ada"}},
	}
	if canonicalRunRequestHash(first) != canonicalRunRequestHash(second) {
		t.Fatal("canonical request hash depends on JSON object property order")
	}
	second.Input["paid"] = false
	if canonicalRunRequestHash(first) == canonicalRunRequestHash(second) {
		t.Fatal("canonical request hash ignored a semantic body change")
	}
}

func TestCORSExposesDraftConcurrencyHeaders(t *testing.T) {
	client, _ := newTestServer()
	request := httptest.NewRequest(http.MethodOptions, "/v1/flows/example/draft", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	recorder := httptest.NewRecorder()

	client.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status %d: %s", recorder.Code, recorder.Body.String())
	}
	exposed := recorder.Header().Get("Access-Control-Expose-Headers")
	for _, header := range []string{"ETag", "X-Draft-Revision", "X-Request-ID", "Retry-After"} {
		if !strings.Contains(exposed, header) {
			t.Fatalf("Access-Control-Expose-Headers = %q, missing %s", exposed, header)
		}
	}
}

func TestImportIsStrictAndParseTextHonoursContractBounds(t *testing.T) {
	client, _ := newTestServer()
	client.register(t, "importer@example.com")
	canonical := starterFlow("Imported", "")
	toObject := func(value any) map[string]any {
		raw, _ := json.Marshal(value)
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	tests := []struct {
		name    string
		payload any
		status  int
	}{
		{"valid", canonical, http.StatusOK},
		{"unknown top level", func() any {
			value := toObject(canonical)
			value["executeCode"] = true
			return value
		}(), http.StatusBadRequest},
		{"unknown nested node", func() any {
			value := toObject(canonical)
			nodes := value["nodes"].([]any)
			nodes[0].(map[string]any)["executeCode"] = true
			return value
		}(), http.StatusBadRequest},
		{"unknown wrapper property", map[string]any{"definition": canonical, "persist": true}, http.StatusBadRequest},
		{"unknown metadata property", func() any {
			value := toObject(canonical)
			value["metadata"] = map[string]any{"executeCode": true}
			return value
		}(), http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := client.request(t, http.MethodPost, "/v1/flows/import", test.payload, nil)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
	canonicalRaw, _ := json.Marshal(canonical)
	trailingRequest := httptest.NewRequest(http.MethodPost, "/v1/flows/import", strings.NewReader(string(canonicalRaw)+" {}"))
	trailingRequest.Header.Set("Content-Type", "application/json")
	trailingRequest.Header.Set("X-CSRF-Token", client.csrf)
	for _, cookie := range client.cookies {
		trailingRequest.AddCookie(cookie)
	}
	trailingResponse := httptest.NewRecorder()
	client.router.ServeHTTP(trailingResponse, trailingRequest)
	if trailingResponse.Code != http.StatusBadRequest {
		t.Fatalf("trailing import status=%d: %s", trailingResponse.Code, trailingResponse.Body.String())
	}
	short := client.request(t, http.MethodPost, "/v1/flows/parse-text", map[string]any{"text": "corto", "locale": "es"}, nil)
	if short.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short parse status=%d: %s", short.Code, short.Body.String())
	}
	valid := client.request(t, http.MethodPost, "/v1/flows/parse-text", map[string]any{
		"text": "Validar el pago y finalizar el proceso.", "locale": "es",
	}, nil)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid parse status=%d: %s", valid.Code, valid.Body.String())
	}
}
