package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestDiffFlowRevisionsSupportsDraftAndVersionPairsForViewer(t *testing.T) {
	fixture := newVersionDiffFixture(t)
	tests := []struct {
		name           string
		query          string
		wantBaseKind   domain.DiffRevisionKind
		wantTargetKind domain.DiffRevisionKind
		wantBaseID     string
		wantTargetID   string
	}{
		{
			name:           "version to draft",
			query:          "?baseVersionId=" + fixture.baseVersion.ID,
			wantBaseKind:   domain.DiffRevisionVersion,
			wantTargetKind: domain.DiffRevisionDraft,
			wantBaseID:     fixture.baseVersion.ID,
		},
		{
			name:           "draft to version",
			query:          "?targetVersionId=" + fixture.targetVersion.ID,
			wantBaseKind:   domain.DiffRevisionDraft,
			wantTargetKind: domain.DiffRevisionVersion,
			wantTargetID:   fixture.targetVersion.ID,
		},
		{
			name:           "version to version",
			query:          "?baseVersionId=" + fixture.baseVersion.ID + "&targetVersionId=" + fixture.targetVersion.ID,
			wantBaseKind:   domain.DiffRevisionVersion,
			wantTargetKind: domain.DiffRevisionVersion,
			wantBaseID:     fixture.baseVersion.ID,
			wantTargetID:   fixture.targetVersion.ID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(fixture.viewer, fixture.flow.ID, test.query)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var result domain.FlowDiff
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.FlowID != fixture.flow.ID {
				t.Fatalf("flowId = %q, want %q", result.FlowID, fixture.flow.ID)
			}
			if result.Base.Kind != test.wantBaseKind || result.Target.Kind != test.wantTargetKind {
				t.Fatalf("revision kinds = %q -> %q, want %q -> %q", result.Base.Kind, result.Target.Kind, test.wantBaseKind, test.wantTargetKind)
			}
			if result.Base.VersionID != test.wantBaseID || result.Target.VersionID != test.wantTargetID {
				t.Fatalf("revision IDs = %q -> %q, want %q -> %q", result.Base.VersionID, result.Target.VersionID, test.wantBaseID, test.wantTargetID)
			}
			if result.Summary.SemanticMatch || result.Summary.OverallImpact != domain.DiffImpactBehavioral {
				t.Fatalf("unexpected summary: %#v", result.Summary)
			}
		})
	}
}

func TestDiffFlowRevisionsValidatesSelectorsAndFlowOwnership(t *testing.T) {
	fixture := newVersionDiffFixture(t)
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantCode   string
	}{
		{name: "both revisions omitted", query: "", wantStatus: http.StatusBadRequest, wantCode: "diff.revision_required"},
		{name: "malformed base UUID", query: "?baseVersionId=not-a-uuid", wantStatus: http.StatusBadRequest, wantCode: "diff.invalid_version_id"},
		{name: "empty target UUID", query: "?targetVersionId=", wantStatus: http.StatusBadRequest, wantCode: "diff.invalid_version_id"},
		{name: "unknown version", query: "?baseVersionId=" + uuid.NewString(), wantStatus: http.StatusNotFound, wantCode: "resource.not_found"},
		{name: "version from another flow", query: "?baseVersionId=" + fixture.otherVersion.ID, wantStatus: http.StatusNotFound, wantCode: "resource.not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(fixture.viewer, fixture.flow.ID, test.query)
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
				t.Fatalf("error code = %q, want %q", payload.Code, test.wantCode)
			}
		})
	}
}

func TestDiffFlowRevisionsHidesFlowFromNonMember(t *testing.T) {
	fixture := newVersionDiffFixture(t)
	intruder := domain.User{ID: uuid.NewString(), Email: "diff-intruder@example.com", DisplayName: "Intruder"}
	if err := fixture.repository.CreateUser(context.Background(), intruder); err != nil {
		t.Fatal(err)
	}

	response := fixture.request(intruder, fixture.flow.ID, "?baseVersionId="+fixture.baseVersion.ID)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
	}
}

type versionDiffFixture struct {
	repository    *store.Memory
	server        *Server
	viewer        domain.User
	flow          domain.Flow
	baseVersion   domain.FlowVersion
	targetVersion domain.FlowVersion
	otherVersion  domain.FlowVersion
}

func newVersionDiffFixture(t *testing.T) versionDiffFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	repository := store.NewMemory()
	owner := domain.User{ID: uuid.NewString(), Email: "diff-owner@example.com", DisplayName: "Owner"}
	viewer := domain.User{ID: uuid.NewString(), Email: "diff-viewer@example.com", DisplayName: "Viewer"}
	for _, user := range []domain.User{owner, viewer} {
		if err := repository.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{ID: uuid.NewString(), Name: "Diff", OwnerID: owner.ID}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetProjectMember(ctx, project.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}

	baseDefinition := diffHandlerDefinition(10)
	targetDefinition := diffHandlerDefinition(20)
	draftDefinition := diffHandlerDefinition(30)
	flow := domain.Flow{
		ID: uuid.NewString(), ProjectID: project.ID, Name: "Orders",
		Draft: draftDefinition, DraftETag: checksum(draftDefinition),
	}
	if err := repository.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	baseVersion := domain.FlowVersion{
		ID: uuid.NewString(), FlowID: flow.ID, Number: 1,
		Definition: baseDefinition, Checksum: checksum(baseDefinition), PublishedBy: owner.ID,
	}
	targetVersion := domain.FlowVersion{
		ID: uuid.NewString(), FlowID: flow.ID, Number: 2,
		Definition: targetDefinition, Checksum: checksum(targetDefinition), PublishedBy: owner.ID,
	}
	for _, version := range []domain.FlowVersion{baseVersion, targetVersion} {
		if err := repository.CreateVersion(ctx, version); err != nil {
			t.Fatal(err)
		}
	}

	otherFlow := domain.Flow{
		ID: uuid.NewString(), ProjectID: project.ID, Name: "Other",
		Draft: baseDefinition, DraftETag: checksum(baseDefinition),
	}
	if err := repository.CreateFlow(ctx, otherFlow); err != nil {
		t.Fatal(err)
	}
	otherVersion := domain.FlowVersion{
		ID: uuid.NewString(), FlowID: otherFlow.ID, Number: 1,
		Definition: baseDefinition, Checksum: checksum(baseDefinition), PublishedBy: owner.ID,
	}
	if err := repository.CreateVersion(ctx, otherVersion); err != nil {
		t.Fatal(err)
	}

	return versionDiffFixture{
		repository: repository,
		server:     &Server{repository: repository},
		viewer:     viewer, flow: flow,
		baseVersion: baseVersion, targetVersion: targetVersion, otherVersion: otherVersion,
	}
}

func (fixture versionDiffFixture) request(user domain.User, flowID, query string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(userContextKey, user)
		c.Next()
	})
	router.GET("/v1/flows/:flowId/diff", fixture.server.diffFlowRevisions)
	request := httptest.NewRequest(http.MethodGet, "/v1/flows/"+flowID+"/diff"+query, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func diffHandlerDefinition(duration int64) domain.FlowDefinition {
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Orders",
		Variables:     []domain.VariableDefinition{},
		Layout:        domain.Layout{Mode: "force"},
		Nodes: []domain.Node{
			{ID: "process", Type: domain.NodeProcess, Label: "Process", DurationMS: duration},
		},
		Edges: []domain.Edge{},
	}.Normalize()
}
