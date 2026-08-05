package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/flowverse/flowverse-api/internal/contract"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/store"
	"github.com/flowverse/flowverse-api/internal/telemetry"
)

func (s *Server) createProject(c *gin.Context) {
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if err := requiredString(request.Name, "name"); err != nil {
		writeError(c, http.StatusBadRequest, "project.name_required", err.Error(), nil)
		return
	}
	timestamp := now()
	project := domain.Project{
		ID: uuid.NewString(), Name: strings.TrimSpace(request.Name), Description: request.Description,
		OwnerID: currentUser(c).ID, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := s.repository.CreateProject(c.Request.Context(), project); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, projectView(project, domain.RoleOwner))
}

func (s *Server) listProjects(c *gin.Context) {
	projects, err := s.repository.ListProjects(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]gin.H, 0, len(projects))
	for _, project := range projects {
		role, _ := s.repository.ProjectRole(c.Request.Context(), project.ID, currentUser(c).ID)
		items = append(items, projectView(project, role))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getProject(c *gin.Context) {
	id := c.Param("projectId")
	if !s.allowProject(c, id, domain.RoleViewer) {
		return
	}
	project, err := s.repository.ProjectByID(c.Request.Context(), id)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	role, _ := s.repository.ProjectRole(c.Request.Context(), id, currentUser(c).ID)
	c.JSON(http.StatusOK, projectView(project, role))
}

func (s *Server) updateProject(c *gin.Context) {
	id := c.Param("projectId")
	if !s.allowProject(c, id, domain.RoleOwner) {
		return
	}
	project, err := s.repository.ProjectByID(c.Request.Context(), id)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if err := requiredString(request.Name, "name"); err != nil {
		writeError(c, http.StatusBadRequest, "project.name_required", err.Error(), nil)
		return
	}
	project.Name, project.Description, project.UpdatedAt = strings.TrimSpace(request.Name), request.Description, now()
	if err := s.repository.UpdateProject(c.Request.Context(), project); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, projectView(project, domain.RoleOwner))
}

func (s *Server) patchProject(c *gin.Context) {
	id := c.Param("projectId")
	if !s.allowProject(c, id, domain.RoleOwner) {
		return
	}
	project, err := s.repository.ProjectByID(c.Request.Context(), id)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	var request struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if request.Name != nil {
		if err := requiredString(*request.Name, "name"); err != nil {
			writeError(c, http.StatusBadRequest, "project.name_required", err.Error(), nil)
			return
		}
		project.Name = strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		project.Description = *request.Description
	}
	project.UpdatedAt = now()
	if err := s.repository.UpdateProject(c.Request.Context(), project); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, projectView(project, domain.RoleOwner))
}

func (s *Server) deleteProject(c *gin.Context) {
	id := c.Param("projectId")
	if !s.allowProject(c, id, domain.RoleOwner) {
		return
	}
	if err := s.repository.DeleteProject(c.Request.Context(), id); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) listMembers(c *gin.Context) {
	projectID := c.Param("projectId")
	if !s.allowProject(c, projectID, domain.RoleViewer) {
		return
	}
	roles, err := s.repository.ListProjectMembers(c.Request.Context(), projectID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := []gin.H{}
	for userID, role := range roles {
		user, userErr := s.repository.UserByID(c.Request.Context(), userID)
		if userErr == nil {
			items = append(items, gin.H{"user": user, "role": role, "joinedAt": user.CreatedAt})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["user"].(domain.User).Email < items[j]["user"].(domain.User).Email
	})
	c.JSON(http.StatusOK, items)
}

func (s *Server) addMember(c *gin.Context) {
	projectID := c.Param("projectId")
	if !s.allowProject(c, projectID, domain.RoleOwner) {
		return
	}
	var request struct {
		Email string      `json:"email"`
		Role  domain.Role `json:"role"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if !validRole(request.Role) {
		writeError(c, http.StatusBadRequest, "member.invalid_role", "Role must be editor or viewer", nil)
		return
	}
	user, err := s.repository.UserByEmail(c.Request.Context(), request.Email)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if err := s.repository.SetProjectMember(c.Request.Context(), projectID, user.ID, request.Role); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": user, "role": request.Role, "joinedAt": now()})
}

func (s *Server) removeMember(c *gin.Context) {
	projectID := c.Param("projectId")
	if !s.allowProject(c, projectID, domain.RoleOwner) {
		return
	}
	project, err := s.repository.ProjectByID(c.Request.Context(), projectID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if project.OwnerID == c.Param("userId") {
		writeError(c, http.StatusConflict, "member.owner_cannot_be_removed", "Project owner cannot be removed", nil)
		return
	}
	if err := s.repository.RemoveProjectMember(c.Request.Context(), projectID, c.Param("userId")); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) createFlow(c *gin.Context) {
	var request struct {
		ProjectID   string                 `json:"projectId"`
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Definition  *domain.FlowDefinition `json:"definition,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if request.ProjectID == "" {
		request.ProjectID = c.Param("projectId")
	}
	if !s.allowProject(c, request.ProjectID, domain.RoleEditor) {
		return
	}
	if err := requiredString(request.Name, "name"); err != nil {
		writeError(c, http.StatusBadRequest, "flow.name_required", err.Error(), nil)
		return
	}
	definition := starterFlow(request.Name, request.Description)
	if request.Definition != nil {
		definition = request.Definition.Normalize()
	}
	timestamp := now()
	flow := domain.Flow{
		ID: uuid.NewString(), ProjectID: request.ProjectID, Name: strings.TrimSpace(request.Name),
		Description: request.Description, Draft: definition, DraftETag: checksum(definition),
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	if err := s.repository.CreateFlow(c.Request.Context(), flow); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.JSON(http.StatusCreated, s.flowSummary(c, flow))
}

func (s *Server) listFlows(c *gin.Context) {
	projectID := c.Param("projectId")
	if !s.allowProject(c, projectID, domain.RoleViewer) {
		return
	}
	flows, err := s.repository.ListFlows(c.Request.Context(), projectID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]gin.H, 0, len(flows))
	for _, flow := range flows {
		items = append(items, s.flowSummary(c, flow))
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getFlow(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.JSON(http.StatusOK, s.flowSummary(c, flow))
}

func (s *Server) getFlowDraft(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.Header("X-Draft-Revision", flow.DraftETag)
	c.JSON(http.StatusOK, flow.Draft.Normalize())
}

func (s *Server) replaceFlowDraft(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	expected := c.GetHeader("If-Match")
	if expected == "" {
		writeError(c, http.StatusPreconditionRequired, "draft.if_match_required", "If-Match is required", nil)
		return
	}
	var definition domain.FlowDefinition
	if !bindJSON(c, &definition) {
		return
	}
	definition = definition.Normalize()
	flow.Draft = definition
	flow.Name, flow.Description = definition.Name, definition.Description
	flow.DraftETag, flow.UpdatedAt = checksum(definition), now()
	if err := s.repository.UpdateFlow(c.Request.Context(), flow, expected); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.Header("X-Draft-Revision", flow.DraftETag)
	c.JSON(http.StatusOK, definition)
}

func (s *Server) restoreFlowDraft(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	expected := c.GetHeader("If-Match")
	if expected == "" {
		writeError(c, http.StatusPreconditionRequired, "draft.if_match_required", "If-Match is required", nil)
		return
	}
	var request struct {
		VersionID string `json:"versionId"`
	}
	if !bindJSON(c, &request) {
		return
	}
	request.VersionID = strings.TrimSpace(request.VersionID)
	if _, err := uuid.Parse(request.VersionID); err != nil {
		writeError(c, http.StatusBadRequest, "version.id_invalid", "versionId must be a valid UUID", nil)
		return
	}
	version, err := s.repository.VersionByID(c.Request.Context(), request.VersionID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if version.FlowID != flow.ID {
		writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
		return
	}
	definition := version.Definition.Normalize()
	flow.Draft = definition
	flow.Name, flow.Description = definition.Name, definition.Description
	flow.DraftETag, flow.UpdatedAt = checksum(definition), now()
	if err := s.repository.UpdateFlow(c.Request.Context(), flow, expected); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.Header("X-Draft-Revision", flow.DraftETag)
	c.JSON(http.StatusOK, gin.H{
		"definition":          definition,
		"restoredFromVersion": versionView(version),
	})
}

func (s *Server) patchFlow(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	var request struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if request.Name != nil {
		if err := requiredString(*request.Name, "name"); err != nil {
			writeError(c, http.StatusBadRequest, "flow.name_required", err.Error(), nil)
			return
		}
		flow.Name, flow.Draft.Name = strings.TrimSpace(*request.Name), strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		flow.Description, flow.Draft.Description = *request.Description, *request.Description
	}
	expected := flow.DraftETag
	flow.DraftETag, flow.UpdatedAt = checksum(flow.Draft), now()
	if err := s.repository.UpdateFlow(c.Request.Context(), flow, expected); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, s.flowSummary(c, flow))
}

func (s *Server) updateFlow(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	expected := c.GetHeader("If-Match")
	if expected == "" {
		writeError(c, http.StatusPreconditionRequired, "draft.if_match_required", "If-Match is required", nil)
		return
	}
	var request struct {
		Name        string                `json:"name"`
		Description string                `json:"description"`
		Definition  domain.FlowDefinition `json:"definition"`
	}
	if !bindJSON(c, &request) {
		return
	}
	definition := request.Definition.Normalize()
	if strings.TrimSpace(request.Name) == "" {
		request.Name = definition.Name
	}
	flow.Name, flow.Description, flow.Draft = strings.TrimSpace(request.Name), request.Description, definition
	flow.DraftETag, flow.UpdatedAt = checksum(definition), now()
	if err := s.repository.UpdateFlow(c.Request.Context(), flow, expected); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Header("ETag", flow.DraftETag)
	c.JSON(http.StatusOK, flow)
}

func (s *Server) deleteFlow(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	if err := s.repository.DeleteFlow(c.Request.Context(), flow.ID); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) publishVersion(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	if strings.HasSuffix(c.FullPath(), "/publish") {
		expected := c.GetHeader("If-Match")
		if expected == "" {
			writeError(c, http.StatusPreconditionRequired, "draft.if_match_required", "If-Match is required", nil)
			return
		}
		if !etagMatches(expected, flow.DraftETag) {
			writeError(c, http.StatusPreconditionFailed, "draft.conflict", "Draft changed since it was loaded", nil)
			return
		}
	}
	validation := contract.ValidateFlow(flow.Draft)
	if !validation.Valid {
		writeError(c, http.StatusUnprocessableEntity, "flow.invalid", "Flow must pass validation before publishing", validation.Issues)
		return
	}
	versions, err := s.repository.ListVersions(c.Request.Context(), flow.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	version := domain.FlowVersion{
		ID: uuid.NewString(), FlowID: flow.ID, Number: len(versions) + 1,
		Definition: flow.Draft.Normalize(), Checksum: checksum(flow.Draft), CreatedAt: now(),
		PublishedBy: currentUser(c).ID,
	}
	if err := s.repository.CreateVersion(c.Request.Context(), version); err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, versionView(version))
}

func (s *Server) listVersions(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	versions, err := s.repository.ListVersions(c.Request.Context(), flow.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]gin.H, 0, len(versions))
	for _, version := range versions {
		items = append(items, versionView(version))
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) getVersion(c *gin.Context) {
	version, _, ok := s.versionWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	c.Header("ETag", version.Checksum)
	c.JSON(http.StatusOK, gin.H{"version": versionView(version), "definition": version.Definition})
}

func (s *Server) validateDraft(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if ok {
		c.JSON(http.StatusOK, contract.ValidateFlow(flow.Draft))
	}
}

func (s *Server) analyzeDraft(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if ok {
		c.JSON(http.StatusOK, analysisView(analyzeWithTelemetry(c, flow.Draft)))
	}
}

func (s *Server) validateVersion(c *gin.Context) {
	version, _, ok := s.versionWithAccess(c, domain.RoleViewer)
	if ok {
		c.JSON(http.StatusOK, contract.ValidateFlow(version.Definition))
	}
}

func (s *Server) analyzeVersion(c *gin.Context) {
	version, _, ok := s.versionWithAccess(c, domain.RoleViewer)
	if ok {
		c.JSON(http.StatusOK, analysisView(analyzeWithTelemetry(c, version.Definition)))
	}
}

func analyzeWithTelemetry(c *gin.Context, definition domain.FlowDefinition) domain.Analysis {
	started := time.Now()
	analysis := engine.Analyze(definition)
	telemetry.AnalysisDuration(c.Request.Context(), time.Since(started))
	trace.SpanFromContext(c.Request.Context()).SetAttributes(
		attribute.Int("flowverse.flow.node_count", analysis.NodeCount),
		attribute.Int("flowverse.flow.edge_count", analysis.EdgeCount),
		attribute.Int("flowverse.flow.cyclomatic_complexity", analysis.CyclomaticComplexity),
		attribute.Bool("flowverse.flow.paths_truncated", analysis.PathsTruncated),
	)
	return analysis
}

func (s *Server) importFlow(c *gin.Context) {
	var raw json.RawMessage
	if !bindJSON(c, &raw) {
		return
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		writeError(c, http.StatusBadRequest, "import.invalid_json", "Imported JSON must be an object", gin.H{"reason": err.Error()})
		return
	}
	var wrapper struct {
		Definition json.RawMessage `json:"definition"`
	}
	definitionRaw := raw
	if _, wrapped := shape["definition"]; wrapped {
		if err := decodeStrict(raw, &wrapper); err != nil {
			writeError(c, http.StatusBadRequest, "import.invalid_wrapper", "Import wrapper contains unknown or invalid properties", gin.H{"reason": err.Error()})
			return
		}
		definitionRaw = wrapper.Definition
	}
	var definition domain.FlowDefinition
	if err := decodeStrict(definitionRaw, &definition); err != nil {
		writeError(c, http.StatusBadRequest, "import.invalid_definition", "Imported flow does not match the contract", gin.H{"reason": err.Error()})
		return
	}
	definition = definition.Normalize()
	report := contract.ValidateFlow(definition)
	if !report.Valid {
		writeError(c, http.StatusUnprocessableEntity, "import.invalid_flow", "Imported flow failed validation", report.Issues)
		return
	}
	c.JSON(http.StatusOK, gin.H{"definition": definition, "report": report})
}

func (s *Server) parseText(c *gin.Context) {
	var request struct {
		Text   string `json:"text"`
		Locale string `json:"locale,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	length := len([]rune(request.Text))
	if length < 10 || length > 20000 {
		writeError(c, http.StatusUnprocessableEntity, "parser.invalid_text_length", "Text must contain between 10 and 20000 characters", nil)
		return
	}
	result, err := s.parser.Parse(c.Request.Context(), request.Text)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "parser.failed", "Text could not be converted into a flow", gin.H{"reason": err.Error()})
		return
	}
	result.Proposal = result.Proposal.Normalize()
	c.JSON(http.StatusOK, result)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func (s *Server) flowWithAccess(c *gin.Context, required domain.Role) (domain.Flow, bool) {
	flow, err := s.repository.FlowByID(c.Request.Context(), c.Param("flowId"))
	if err != nil {
		mapStoreError(c, err)
		return domain.Flow{}, false
	}
	if !s.allowProject(c, flow.ProjectID, required) {
		return domain.Flow{}, false
	}
	return flow, true
}

func (s *Server) versionWithAccess(c *gin.Context, required domain.Role) (domain.FlowVersion, domain.Flow, bool) {
	version, err := s.repository.VersionByID(c.Request.Context(), c.Param("versionId"))
	if err != nil {
		mapStoreError(c, err)
		return domain.FlowVersion{}, domain.Flow{}, false
	}
	flow, err := s.repository.FlowByID(c.Request.Context(), version.FlowID)
	if err != nil {
		mapStoreError(c, err)
		return domain.FlowVersion{}, domain.Flow{}, false
	}
	if !s.allowProject(c, flow.ProjectID, required) {
		return domain.FlowVersion{}, domain.Flow{}, false
	}
	return version, flow, true
}

func (s *Server) flowSummary(c *gin.Context, flow domain.Flow) gin.H {
	versions, _ := s.repository.ListVersions(c.Request.Context(), flow.ID)
	return gin.H{
		"id": flow.ID, "projectId": flow.ProjectID, "name": flow.Name,
		"description": flow.Description, "draftEtag": flow.DraftETag,
		"publishedVersionCount": len(versions), "createdAt": flow.CreatedAt, "updatedAt": flow.UpdatedAt,
	}
}

func versionView(version domain.FlowVersion) gin.H {
	return gin.H{
		"id": version.ID, "flowId": version.FlowID, "number": version.Number, "checksum": version.Checksum,
		"publishedAt": version.CreatedAt, "publishedBy": version.PublishedBy,
	}
}

func projectView(project domain.Project, role domain.Role) gin.H {
	return gin.H{
		"id": project.ID, "name": project.Name, "description": project.Description,
		"role": role, "createdAt": project.CreatedAt, "updatedAt": project.UpdatedAt,
	}
}

func analysisView(analysis domain.Analysis) gin.H {
	cycles := make([][]string, 0, len(analysis.Cycles))
	for _, cycle := range analysis.Cycles {
		cycles = append(cycles, cycle.NodeIDs)
	}
	var criticalPath any
	if analysis.CriticalPathApplies {
		criticalPath = analysis.CriticalPathNodeIDs
	}
	return gin.H{
		"nodeCount": analysis.NodeCount, "edgeCount": analysis.EdgeCount, "maxDepth": analysis.MaxDepth,
		"unreachableNodeIds": analysis.UnreachableNodeIDs, "cycles": cycles,
		"complexity":         analysis.CyclomaticComplexity,
		"paths":              gin.H{"count": analysis.PathCount, "truncated": analysis.PathsTruncated},
		"staticCriticalPath": criticalPath,
	}
}

func starterFlow(name, description string) domain.FlowDefinition {
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: name, Description: description,
		Variables: []domain.VariableDefinition{}, Layout: domain.Layout{Mode: "directional"},
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Inicio", Inputs: []domain.Port{}, Outputs: []domain.Port{{ID: "output", Label: "Salida"}}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{}, Position: domain.Position{X: -120}},
			{ID: "end", Type: domain.NodeEnd, Label: "Fin", Inputs: []domain.Port{{ID: "input", Label: "Entrada"}}, Outputs: []domain.Port{}, ActivationMode: domain.ActivationEach, Configuration: map[string]any{"result": "success"}, Position: domain.Position{X: 120}},
		},
		Edges: []domain.Edge{{ID: "start_end", Source: "start", Target: "end", SourcePort: "output", TargetPort: "input", Priority: 0, Default: false}},
	}
}

func ignoreStoreNotFound(err error) bool { return err == store.ErrNotFound }
