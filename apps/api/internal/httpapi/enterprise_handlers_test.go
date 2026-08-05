package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestEnterpriseControlPlaneLifecycleAndRBAC(t *testing.T) {
	owner, _ := newTestServer()
	owner.register(t, "enterprise-owner@example.com")
	admin := enterpriseClient(t, owner, "enterprise-admin@example.com")
	auditor := enterpriseClient(t, owner, "enterprise-auditor@example.com")
	member := enterpriseClient(t, owner, "enterprise-member@example.com")
	intruder := enterpriseClient(t, owner, "enterprise-intruder@example.com")

	unknownField := owner.request(t, http.MethodPost, "/v1/organizations", map[string]any{
		"slug": "invalid", "name": "Invalid", "secret": "must be rejected",
	}, nil)
	assertEnterpriseStatus(t, unknownField, http.StatusBadRequest)
	oversized := owner.request(t, http.MethodPost, "/v1/organizations", map[string]any{
		"slug": "oversized", "name": strings.Repeat("x", maxEnterpriseBodyBytes),
	}, nil)
	assertEnterpriseStatus(t, oversized, http.StatusRequestEntityTooLarge)

	created := owner.request(t, http.MethodPost, "/v1/organizations", map[string]any{
		"slug": "acme-platform", "name": "Acme Platform",
	}, nil)
	assertEnterpriseStatus(t, created, http.StatusCreated)
	var organization enterprise.Organization
	decodeEnterpriseResponse(t, created.Body.Bytes(), &organization)
	if organization.ID == "" || organization.Status != enterprise.OrganizationActive {
		t.Fatalf("invalid organization: %#v", organization)
	}
	base := "/v1/organizations/" + organization.ID

	assertEnterpriseStatus(t, owner.request(t, http.MethodGet, "/v1/organizations", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, owner.request(t, http.MethodGet, base, nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, owner.request(t, http.MethodGet, "/v1/organizations/not-a-uuid", nil, nil), http.StatusBadRequest)

	setMember := func(email, role string) {
		t.Helper()
		response := owner.request(t, http.MethodPost, base+"/members", map[string]any{
			"email": email, "role": role,
		}, nil)
		assertEnterpriseStatus(t, response, http.StatusCreated)
	}
	setMember("enterprise-admin@example.com", "admin")
	setMember("enterprise-auditor@example.com", "auditor")
	setMember("enterprise-member@example.com", "member")

	assertEnterpriseStatus(t, owner.request(t, http.MethodGet, base+"/members", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base, nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/members", nil, nil), http.StatusNotFound)
	assertEnterpriseStatus(t, intruder.request(t, http.MethodGet, base, nil, nil), http.StatusNotFound)
	assertEnterpriseStatus(t, intruder.request(t, http.MethodGet, base+"/plugins", nil, nil), http.StatusNotFound)

	adminPromotesOwner := admin.request(t, http.MethodPost, base+"/members", map[string]any{
		"email": "enterprise-member@example.com", "role": "owner",
	}, nil)
	assertEnterpriseStatus(t, adminPromotesOwner, http.StatusNotFound)
	adminChangesOwner := admin.request(t, http.MethodPost, base+"/members", map[string]any{
		"email": "enterprise-owner@example.com", "role": "admin",
	}, nil)
	assertEnterpriseStatus(t, adminChangesOwner, http.StatusNotFound)
	lastOwner := owner.request(t, http.MethodPost, base+"/members", map[string]any{
		"email": "enterprise-owner@example.com", "role": "member",
	}, nil)
	assertEnterpriseError(t, lastOwner, http.StatusConflict, "organization.last_owner")

	ssoBody := map[string]any{
		"name": "Corporate OIDC", "protocol": "oidc",
		"issuerUrl": "https://identity.example.com", "domains": []string{"example.com"}, "enabled": true,
	}
	ssoCreated := owner.request(t, http.MethodPost, base+"/sso-connections", ssoBody, nil)
	assertEnterpriseStatus(t, ssoCreated, http.StatusCreated)
	var sso enterprise.SSOConnection
	decodeEnterpriseResponse(t, ssoCreated.Body.Bytes(), &sso)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/sso-connections", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/sso-connections/"+sso.ID, nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodPost, base+"/sso-connections", ssoBody, nil), http.StatusNotFound)
	missingEnabled := owner.request(t, http.MethodPost, base+"/sso-connections", map[string]any{
		"name": "Incomplete OIDC", "protocol": "oidc", "issuerUrl": "https://incomplete.example.com", "domains": []string{"incomplete.example.com"},
	}, nil)
	assertEnterpriseStatus(t, missingEnabled, http.StatusUnprocessableEntity)
	ssoBody["enabled"] = false
	assertEnterpriseStatus(t, admin.request(t, http.MethodPut, base+"/sso-connections/"+sso.ID, ssoBody, nil), http.StatusOK)
	secretAttempt := owner.request(t, http.MethodPost, base+"/sso-connections", map[string]any{
		"name": "Unsafe", "protocol": "oidc", "issuerUrl": "https://unsafe.example.com",
		"domains": []string{"unsafe.example.com"}, "enabled": true, "clientSecret": "never",
	}, nil)
	assertEnterpriseStatus(t, secretAttempt, http.StatusBadRequest)

	allowRule := owner.request(t, http.MethodPost, base+"/policy-rules", map[string]any{
		"description": "Members can read projects", "effect": "allow",
		"actions": []string{"project.read"}, "resources": []string{"project:**"},
		"conditions": map[string]any{"roles": []string{"member"}}, "disabled": false,
	}, nil)
	assertEnterpriseStatus(t, allowRule, http.StatusCreated)
	var allow enterprise.PolicyRule
	decodeEnterpriseResponse(t, allowRule.Body.Bytes(), &allow)
	missingPolicyFields := owner.request(t, http.MethodPost, base+"/policy-rules", map[string]any{
		"effect": "allow", "actions": []string{"project.read"}, "resources": []string{"project:**"},
	}, nil)
	assertEnterpriseStatus(t, missingPolicyFields, http.StatusUnprocessableEntity)
	denyRule := owner.request(t, http.MethodPost, base+"/policy-rules", map[string]any{
		"description": "Restricted project", "effect": "deny",
		"actions": []string{"project.read"}, "resources": []string{"project:restricted"},
		"conditions": map[string]any{"roles": []string{"member"}}, "disabled": false,
	}, nil)
	assertEnterpriseStatus(t, denyRule, http.StatusCreated)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/policy-rules", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/policy-rules/"+allow.ID, nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/policy-rules", nil, nil), http.StatusNotFound)

	decisionResponse := member.request(t, http.MethodPost, base+"/policy/evaluate", map[string]any{
		"action": "project.read", "resource": "project:restricted",
	}, nil)
	assertEnterpriseStatus(t, decisionResponse, http.StatusOK)
	var decision enterprise.PolicyDecision
	decodeEnterpriseResponse(t, decisionResponse.Body.Bytes(), &decision)
	if decision.Allowed || decision.Reason != enterprise.DecisionExplicitDeny || len(decision.MatchedRuleIDs) != 2 {
		t.Fatalf("deny precedence was not applied: %#v", decision)
	}
	forgedRole := member.request(t, http.MethodPost, base+"/policy/evaluate", map[string]any{
		"action": "project.read", "resource": "project:public", "role": "owner",
	}, nil)
	assertEnterpriseStatus(t, forgedRole, http.StatusBadRequest)

	pluginCreated := owner.request(t, http.MethodPost, base+"/plugins", map[string]any{
		"pluginKey": "acme.guard", "version": "1.2.3",
		"sourceUrl":    "oci://registry.example.com/flowverse/acme-guard",
		"checksum":     "sha256:" + strings.Repeat("a", 64),
		"capabilities": []string{"project.read"},
	}, nil)
	assertEnterpriseStatus(t, pluginCreated, http.StatusCreated)
	var plugin enterprise.PluginRegistration
	decodeEnterpriseResponse(t, pluginCreated.Body.Bytes(), &plugin)
	if plugin.Status != enterprise.PluginDisabled {
		t.Fatalf("default plugin status = %q", plugin.Status)
	}
	missingCapabilities := owner.request(t, http.MethodPost, base+"/plugins", map[string]any{
		"pluginKey": "acme.incomplete", "version": "1.0.0",
		"sourceUrl": "oci://registry.example.com/flowverse/incomplete", "checksum": "sha256:" + strings.Repeat("b", 64),
	}, nil)
	assertEnterpriseStatus(t, missingCapabilities, http.StatusUnprocessableEntity)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/plugins", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/plugins/"+plugin.ID, nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, admin.request(t, http.MethodPatch, base+"/plugins/"+plugin.ID, map[string]any{"status": "active"}, nil), http.StatusOK)
	assertEnterpriseStatus(t, owner.request(t, http.MethodPatch, base+"/plugins/"+plugin.ID, map[string]any{"status": "revoked"}, nil), http.StatusOK)
	revoked := owner.request(t, http.MethodPatch, base+"/plugins/"+plugin.ID, map[string]any{"status": "disabled"}, nil)
	assertEnterpriseError(t, revoked, http.StatusConflict, "plugin.revoked")

	projectCreated := owner.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Enterprise Project"}, nil)
	assertEnterpriseStatus(t, projectCreated, http.StatusCreated)
	var project domain.Project
	decodeEnterpriseResponse(t, projectCreated.Body.Bytes(), &project)
	assertEnterpriseStatus(t, admin.request(t, http.MethodPost, base+"/projects/"+project.ID+"/attach", nil, nil), http.StatusNotFound)
	assertEnterpriseStatus(t, owner.request(t, http.MethodPost, base+"/projects/"+project.ID+"/attach", nil, nil), http.StatusOK)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/projects", nil, nil), http.StatusOK)
	secondOrganization := owner.request(t, http.MethodPost, "/v1/organizations", map[string]any{
		"slug": "acme-secondary", "name": "Acme Secondary",
	}, nil)
	assertEnterpriseStatus(t, secondOrganization, http.StatusCreated)
	var otherOrganization enterprise.Organization
	decodeEnterpriseResponse(t, secondOrganization.Body.Bytes(), &otherOrganization)
	moveProject := owner.request(t, http.MethodPost, "/v1/organizations/"+otherOrganization.ID+"/projects/"+project.ID+"/attach", nil, nil)
	assertEnterpriseStatus(t, moveProject, http.StatusConflict)

	auditPage := auditor.request(t, http.MethodGet, base+"/audit?afterSequence=0&limit=2", nil, nil)
	assertEnterpriseStatus(t, auditPage, http.StatusOK)
	var page struct {
		Items             []enterprise.AuditEvent `json:"items"`
		NextAfterSequence uint64                  `json:"nextAfterSequence"`
		HasMore           bool                    `json:"hasMore"`
	}
	decodeEnterpriseResponse(t, auditPage.Body.Bytes(), &page)
	if len(page.Items) != 2 || !page.HasMore || page.NextAfterSequence != page.Items[1].Sequence {
		t.Fatalf("invalid audit page: %#v", page)
	}
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/audit?limit=201", nil, nil), http.StatusBadRequest)
	assertEnterpriseStatus(t, auditor.request(t, http.MethodGet, base+"/audit?afterSequence=-1", nil, nil), http.StatusBadRequest)
	assertEnterpriseStatus(t, member.request(t, http.MethodGet, base+"/audit", nil, nil), http.StatusNotFound)
	verify := auditor.request(t, http.MethodGet, base+"/audit/verify", nil, nil)
	assertEnterpriseStatus(t, verify, http.StatusOK)
	var verification struct {
		Valid      bool `json:"valid"`
		EventCount int  `json:"eventCount"`
	}
	decodeEnterpriseResponse(t, verify.Body.Bytes(), &verification)
	if !verification.Valid || verification.EventCount < 10 {
		t.Fatalf("unexpected audit verification: %#v", verification)
	}

	assertEnterpriseStatus(t, owner.request(t, http.MethodDelete, base+"/policy-rules/"+allow.ID, nil, nil), http.StatusNoContent)
}

func TestEnterpriseRoutesAndBudgetsAreWired(t *testing.T) {
	repository := store.NewMemory()
	authService := auth.New(repository, auth.Config{})
	server := New(repository, authService, parser.NewMock(), runtime.NewManager(repository), Config{})
	routes := map[string]bool{}
	for _, route := range server.Router().Routes() {
		if strings.HasPrefix(route.Path, "/v1/organizations") {
			routes[route.Method+" "+route.Path] = true
		}
	}
	want := []string{
		"POST /v1/organizations",
		"GET /v1/organizations",
		"GET /v1/organizations/:organizationId",
		"GET /v1/organizations/:organizationId/members",
		"POST /v1/organizations/:organizationId/members",
		"GET /v1/organizations/:organizationId/sso-connections",
		"POST /v1/organizations/:organizationId/sso-connections",
		"GET /v1/organizations/:organizationId/sso-connections/:connectionId",
		"PUT /v1/organizations/:organizationId/sso-connections/:connectionId",
		"GET /v1/organizations/:organizationId/policy-rules",
		"POST /v1/organizations/:organizationId/policy-rules",
		"GET /v1/organizations/:organizationId/policy-rules/:ruleId",
		"PUT /v1/organizations/:organizationId/policy-rules/:ruleId",
		"DELETE /v1/organizations/:organizationId/policy-rules/:ruleId",
		"POST /v1/organizations/:organizationId/policy/evaluate",
		"GET /v1/organizations/:organizationId/plugins",
		"POST /v1/organizations/:organizationId/plugins",
		"GET /v1/organizations/:organizationId/plugins/:registrationId",
		"PATCH /v1/organizations/:organizationId/plugins/:registrationId",
		"GET /v1/organizations/:organizationId/audit",
		"GET /v1/organizations/:organizationId/audit/verify",
		"GET /v1/organizations/:organizationId/projects",
		"POST /v1/organizations/:organizationId/projects/:projectId/attach",
	}
	if len(routes) != len(want) {
		t.Fatalf("enterprise route count = %d, want %d: %#v", len(routes), len(want), routes)
	}
	for _, route := range want {
		if !routes[route] {
			t.Errorf("missing enterprise route %s", route)
		}
	}
	for _, budget := range []string{"enterprise.write", "enterprise.eval", "enterprise.verify"} {
		policy := server.ratePolicies[budget]
		if policy.Limit <= 0 || policy.Window <= 0 || server.admissions[budget] == nil {
			t.Errorf("missing rate/admission budget %q", budget)
		}
	}
}

func TestEnterpriseLastOwnerRepositoryRaceIsDeniedAndAudited(t *testing.T) {
	repository := &lastOwnerRaceRepository{Memory: store.NewMemory()}
	owner := enterpriseServerWithRepository(repository)
	owner.register(t, "race-owner@example.com")
	enterpriseClient(t, owner, "race-second-owner@example.com")

	created := owner.request(t, http.MethodPost, "/v1/organizations", map[string]any{
		"slug": "race-org", "name": "Race Org",
	}, nil)
	assertEnterpriseStatus(t, created, http.StatusCreated)
	var organization enterprise.Organization
	decodeEnterpriseResponse(t, created.Body.Bytes(), &organization)
	base := "/v1/organizations/" + organization.ID

	addOwner := owner.request(t, http.MethodPost, base+"/members", map[string]any{
		"email": "race-second-owner@example.com", "role": "owner",
	}, nil)
	assertEnterpriseStatus(t, addOwner, http.StatusCreated)
	repository.failMembership = true
	raced := owner.request(t, http.MethodPost, base+"/members", map[string]any{
		"email": "race-second-owner@example.com", "role": "member",
	}, nil)
	assertEnterpriseError(t, raced, http.StatusConflict, "organization.last_owner")

	events, err := repository.ListAuditEvents(context.Background(), organization.ID, 0, store.MaxAuditListLimit)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Action != "organization.member.set" || last.Outcome != enterprise.AuditDenied || last.Metadata["reason"] != "last_owner" {
		t.Fatalf("repository race was not audited: %#v", last)
	}
	if membership, err := repository.GetMembership(context.Background(), organization.ID, currentEnterpriseUser(t, repository.Memory)); err != nil || membership.Role != enterprise.OrganizationOwner {
		t.Fatalf("membership changed during failed race: %#v, %v", membership, err)
	}
}

type lastOwnerRaceRepository struct {
	*store.Memory
	failMembership bool
}

func (repository *lastOwnerRaceRepository) SetMembership(ctx context.Context, membership enterprise.OrganizationMembership) error {
	if repository.failMembership {
		repository.failMembership = false
		return store.ErrLastOrganizationOwner
	}
	return repository.Memory.SetMembership(ctx, membership)
}

func enterpriseServerWithRepository(repository store.Repository) *testClient {
	authService := auth.New(repository, auth.Config{})
	manager := runtime.NewManager(repository)
	server := New(repository, authService, parser.NewMock(), manager, Config{PublicOrigin: "http://localhost:3000"})
	return &testClient{router: server.Router(), cookies: map[string]*http.Cookie{}}
}

func enterpriseClient(t *testing.T, root *testClient, email string) *testClient {
	t.Helper()
	client := &testClient{router: root.router, cookies: map[string]*http.Cookie{}}
	client.register(t, email)
	return client
}

func currentEnterpriseUser(t *testing.T, repository *store.Memory) string {
	t.Helper()
	// The membership assertion only needs the deterministic user selected by
	// the email used in this test; resolving it through the repository avoids
	// coupling to the opaque session cookie format.
	user, err := repository.UserByEmail(context.Background(), "race-second-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func assertEnterpriseStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func assertEnterpriseError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	assertEnterpriseStatus(t, response, status)
	var payload struct {
		Code string `json:"code"`
	}
	decodeEnterpriseResponse(t, response.Body.Bytes(), &payload)
	if payload.Code != code {
		t.Fatalf("error code = %q, want %q: %s", payload.Code, code, response.Body.String())
	}
}

func decodeEnterpriseResponse(t *testing.T, raw []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode response: %v: %s", err, raw)
	}
}
