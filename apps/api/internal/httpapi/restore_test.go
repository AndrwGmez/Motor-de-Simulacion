package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestRestoreFlowDraft(t *testing.T) {
	owner, repository := newTestServer()
	owner.register(t, "restore-owner@example.com")

	projectResponse := owner.request(t, http.MethodPost, "/v1/projects", map[string]any{"name": "Restore"}, nil)
	if projectResponse.Code != http.StatusCreated {
		t.Fatalf("project status %d: %s", projectResponse.Code, projectResponse.Body.String())
	}
	var project domain.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	createFlow := func(name string) string {
		t.Helper()
		response := owner.request(t, http.MethodPost, "/v1/projects/"+project.ID+"/flows", map[string]any{"name": name}, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("create flow %q status %d: %s", name, response.Code, response.Body.String())
		}
		var flow struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &flow); err != nil {
			t.Fatal(err)
		}
		return flow.ID
	}
	publish := func(flowID string) string {
		t.Helper()
		response := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/versions", nil, nil)
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

	flowID := createFlow("Orders")
	draftResponse := owner.request(t, http.MethodGet, "/v1/flows/"+flowID+"/draft", nil, nil)
	if draftResponse.Code != http.StatusOK {
		t.Fatalf("draft status %d: %s", draftResponse.Code, draftResponse.Body.String())
	}
	var publishedDefinition domain.FlowDefinition
	if err := json.Unmarshal(draftResponse.Body.Bytes(), &publishedDefinition); err != nil {
		t.Fatal(err)
	}
	publishedDefinition.Name = "Orders stable"
	publishedDefinition.Description = "Published baseline"
	publishedDefinition.Nodes[0].Label = "Published trigger"
	savedPublished := owner.request(t, http.MethodPut, "/v1/flows/"+flowID+"/draft", publishedDefinition,
		map[string]string{"If-Match": draftResponse.Header().Get("ETag")})
	if savedPublished.Code != http.StatusOK {
		t.Fatalf("save published draft status %d: %s", savedPublished.Code, savedPublished.Body.String())
	}
	versionID := publish(flowID)

	currentDefinition := publishedDefinition
	currentDefinition.Name = "Orders experimental"
	currentDefinition.Description = "Unpublished changes"
	savedCurrent := owner.request(t, http.MethodPut, "/v1/flows/"+flowID+"/draft", currentDefinition,
		map[string]string{"If-Match": savedPublished.Header().Get("ETag")})
	if savedCurrent.Code != http.StatusOK {
		t.Fatalf("save current draft status %d: %s", savedCurrent.Code, savedCurrent.Body.String())
	}
	currentETag := savedCurrent.Header().Get("ETag")
	if currentETag == "" || currentETag == checksum(publishedDefinition.Normalize()) {
		t.Fatalf("current ETag did not change: %q", currentETag)
	}

	missingPrecondition := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": versionID}, nil)
	if missingPrecondition.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status %d: %s", missingPrecondition.Code, missingPrecondition.Body.String())
	}
	assertErrorCode(t, missingPrecondition, "draft.if_match_required")

	invalidBodies := []struct {
		name string
		body map[string]any
	}{
		{name: "missing version", body: map[string]any{}},
		{name: "invalid version", body: map[string]any{"versionId": "not-a-uuid"}},
		{name: "unknown field", body: map[string]any{"versionId": versionID, "force": true}},
	}
	for _, test := range invalidBodies {
		t.Run(test.name, func(t *testing.T) {
			response := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore", test.body,
				map[string]string{"If-Match": currentETag})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
		})
	}

	otherFlowID := createFlow("Other")
	otherVersionID := publish(otherFlowID)
	wrongFlow := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": otherVersionID}, map[string]string{"If-Match": currentETag})
	if wrongFlow.Code != http.StatusNotFound {
		t.Fatalf("other-flow version status %d: %s", wrongFlow.Code, wrongFlow.Body.String())
	}
	assertErrorCode(t, wrongFlow, "resource.not_found")

	viewer := &testClient{router: owner.router, cookies: map[string]*http.Cookie{}}
	viewer.register(t, "restore-viewer@example.com")
	viewerUser, err := repository.UserByEmail(context.Background(), "restore-viewer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetProjectMember(context.Background(), project.ID, viewerUser.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewerResponse := viewer.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": versionID}, map[string]string{"If-Match": currentETag})
	if viewerResponse.Code != http.StatusNotFound {
		t.Fatalf("viewer status %d: %s", viewerResponse.Code, viewerResponse.Body.String())
	}

	intruder := &testClient{router: owner.router, cookies: map[string]*http.Cookie{}}
	intruder.register(t, "restore-intruder@example.com")
	intruderResponse := intruder.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": versionID}, map[string]string{"If-Match": currentETag})
	if intruderResponse.Code != http.StatusNotFound {
		t.Fatalf("intruder status %d: %s", intruderResponse.Code, intruderResponse.Body.String())
	}

	restored := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": versionID}, map[string]string{"If-Match": currentETag})
	if restored.Code != http.StatusOK {
		t.Fatalf("restore status %d: %s", restored.Code, restored.Body.String())
	}
	var payload struct {
		Definition          domain.FlowDefinition `json:"definition"`
		RestoredFromVersion struct {
			ID     string `json:"id"`
			FlowID string `json:"flowId"`
		} `json:"restoredFromVersion"`
	}
	if err := json.Unmarshal(restored.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantDefinition := publishedDefinition.Normalize()
	if !reflect.DeepEqual(payload.Definition, wantDefinition) {
		t.Fatalf("restored definition mismatch\ngot:  %+v\nwant: %+v", payload.Definition, wantDefinition)
	}
	if payload.RestoredFromVersion.ID != versionID || payload.RestoredFromVersion.FlowID != flowID {
		t.Fatalf("restored version = %+v", payload.RestoredFromVersion)
	}
	wantETag := checksum(wantDefinition)
	if restored.Header().Get("ETag") != wantETag || restored.Header().Get("X-Draft-Revision") != wantETag {
		t.Fatalf("restore headers ETag=%q revision=%q want=%q",
			restored.Header().Get("ETag"), restored.Header().Get("X-Draft-Revision"), wantETag)
	}
	storedFlow, err := repository.FlowByID(context.Background(), flowID)
	if err != nil {
		t.Fatal(err)
	}
	if storedFlow.Name != wantDefinition.Name || storedFlow.Description != wantDefinition.Description ||
		!reflect.DeepEqual(storedFlow.Draft, wantDefinition) {
		t.Fatalf("stored restored flow = %+v", storedFlow)
	}

	stale := owner.request(t, http.MethodPost, "/v1/flows/"+flowID+"/draft/restore",
		map[string]any{"versionId": versionID}, map[string]string{"If-Match": currentETag})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale ETag status %d: %s", stale.Code, stale.Body.String())
	}
	assertErrorCode(t, stale, "draft.conflict")
}
