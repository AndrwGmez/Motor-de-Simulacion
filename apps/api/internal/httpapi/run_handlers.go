package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/contract"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
	"github.com/flowverse/flowverse-api/internal/telemetry"
)

type simulationRequest struct {
	TriggerNodeID string               `json:"triggerNodeId"`
	Input         map[string]any       `json:"input"`
	Overrides     []simulationOverride `json:"overrides,omitempty"`
	Limits        *simulationLimits    `json:"limits,omitempty"`
}

type simulationOverride struct {
	Type    string `json:"type"`
	EdgeID  string `json:"edgeId,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type simulationLimits struct {
	MaxSteps         *int `json:"maxSteps,omitempty"`
	MaxVisitsPerNode *int `json:"maxVisitsPerNode,omitempty"`
}

func (s *Server) createRun(c *gin.Context) {
	version, flow, ok := s.versionWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(c)
	if !ok {
		return
	}
	var request simulationRequest
	if !bindJSON(c, &request) {
		return
	}
	if err := validateSimulationRequest(request); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "run.invalid_request", "Simulation request violates the contract", gin.H{"reason": err.Error()})
		return
	}
	overrides, err := convertOverrides(version.Definition, request)
	if err != nil {
		writeError(c, http.StatusBadRequest, "run.invalid_override", err.Error(), nil)
		return
	}
	runID := runtime.NewRunID()
	timestamp := now()
	run := domain.Run{
		ID: runID, TraceID: telemetry.TraceID(c.Request.Context()),
		ProjectID: flow.ProjectID, FlowID: flow.ID, VersionID: version.ID,
		Status: "created", Input: request.Input, TriggerID: request.TriggerNodeID,
		Definition: version.Definition, DefinitionETag: version.Checksum, CreatedAt: timestamp,
	}
	stored, wasCreated, err := s.repository.CreateRun(c.Request.Context(), run, store.RunIdempotency{
		UserID:      currentUser(c).ID,
		TargetType:  "flow_version",
		TargetID:    version.ID,
		Key:         idempotencyKey,
		RequestHash: canonicalRunRequestHash(request),
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyMismatch) {
			writeIdempotencyMismatch(c)
			return
		}
		mapStoreError(c, err)
		return
	}
	if !wasCreated {
		c.JSON(http.StatusOK, runView(stored))
		return
	}
	simulationStarted := time.Now()
	result, err := s.simulator.Run(version.Definition, engine.RunOptions{
		RunID: runID, TriggerID: request.TriggerNodeID, Input: request.Input, Overrides: overrides,
		MaxSteps: request.maxSteps(), MaxVisitsPerNode: request.maxVisitsPerNode(),
		StartedAt: timestamp,
	})
	recordSimulationTelemetry(c, simulationStarted, result, err)
	if err != nil {
		run.Status, run.Error = "failed", err.Error()
		completed := now()
		run.CompletedAt = &completed
		_ = s.repository.UpdateRun(c.Request.Context(), run)
		writeError(c, http.StatusUnprocessableEntity, "run.invalid_flow", "Simulation could not start", gin.H{"reason": err.Error()})
		return
	}
	if err := s.runs.Start(c.Request.Context(), run, result); err != nil {
		mapStoreError(c, err)
		return
	}
	created, _ := s.repository.RunByID(c.Request.Context(), runID)
	c.JSON(http.StatusCreated, runView(created))
}

// createDraftRun is the editor extension to the immutable-version API. The
// exact draft and ETag are snapshotted before simulation, so subsequent saves
// cannot alter the meaning or replay of this run.
func (s *Server) createDraftRun(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(c)
	if !ok {
		return
	}
	var request simulationRequest
	if !bindJSON(c, &request) {
		return
	}
	if err := validateSimulationRequest(request); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "run.invalid_request", "Simulation request violates the contract", gin.H{"reason": err.Error()})
		return
	}
	definition := flow.Draft.Clone().Normalize()
	if validation := contract.ValidateFlow(definition); !validation.Valid {
		writeError(c, http.StatusUnprocessableEntity, "run.invalid_flow", "Draft must pass validation before simulation", validation.Issues)
		return
	}
	overrides, err := convertOverrides(definition, request)
	if err != nil {
		writeError(c, http.StatusBadRequest, "run.invalid_override", err.Error(), nil)
		return
	}
	runID, timestamp := runtime.NewRunID(), now()
	run := domain.Run{
		ID: runID, TraceID: telemetry.TraceID(c.Request.Context()),
		ProjectID: flow.ProjectID, FlowID: flow.ID, Status: "created",
		Input: request.Input, TriggerID: request.TriggerNodeID, Definition: definition,
		DefinitionETag: flow.DraftETag, CreatedAt: timestamp,
	}
	stored, wasCreated, err := s.repository.CreateRun(c.Request.Context(), run, store.RunIdempotency{
		UserID:         currentUser(c).ID,
		TargetType:     "flow_draft",
		TargetID:       flow.ID,
		TargetRevision: flow.DraftETag,
		Key:            idempotencyKey,
		RequestHash:    canonicalRunRequestHash(request),
	})
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyMismatch) {
			writeIdempotencyMismatch(c)
			return
		}
		mapStoreError(c, err)
		return
	}
	if !wasCreated {
		c.JSON(http.StatusOK, runView(stored))
		return
	}
	simulationStarted := time.Now()
	result, err := s.simulator.Run(definition, engine.RunOptions{
		RunID: runID, TriggerID: request.TriggerNodeID, Input: request.Input, Overrides: overrides,
		MaxSteps: request.maxSteps(), MaxVisitsPerNode: request.maxVisitsPerNode(), StartedAt: timestamp,
	})
	recordSimulationTelemetry(c, simulationStarted, result, err)
	if err != nil {
		run.Status, run.Error = "failed", err.Error()
		completed := now()
		run.CompletedAt = &completed
		_ = s.repository.UpdateRun(c.Request.Context(), run)
		writeError(c, http.StatusUnprocessableEntity, "run.invalid_flow", "Simulation could not start", gin.H{"reason": err.Error()})
		return
	}
	if err := s.runs.Start(c.Request.Context(), run, result); err != nil {
		mapStoreError(c, err)
		return
	}
	created, _ := s.repository.RunByID(c.Request.Context(), runID)
	c.JSON(http.StatusCreated, runView(created))
}

func requireIdempotencyKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		writeError(c, http.StatusBadRequest, "idempotency.key_required", "Idempotency-Key is required", nil)
		return "", false
	}
	if key != strings.TrimSpace(key) {
		writeError(c, http.StatusBadRequest, "idempotency.key_invalid", "Idempotency-Key must not start or end with whitespace", nil)
		return "", false
	}
	length := utf8.RuneCountInString(key)
	if length < 8 || length > 128 {
		writeError(c, http.StatusBadRequest, "idempotency.key_invalid", "Idempotency-Key must contain between 8 and 128 characters", nil)
		return "", false
	}
	return key, true
}

func canonicalRunRequestHash(request simulationRequest) string {
	return strings.Trim(checksum(request), `"`)
}

func writeIdempotencyMismatch(c *gin.Context) {
	writeError(c, http.StatusConflict, "idempotency.payload_mismatch",
		"Idempotency-Key was already used with a different request", nil)
}

func convertOverrides(flow domain.FlowDefinition, request simulationRequest) (engine.RunOverrides, error) {
	result := engine.RunOverrides{ForcedEdges: map[string]string{}, FailedNodes: map[string]string{}}
	edgeSource := map[string]string{}
	nodes := map[string]bool{}
	for _, edge := range flow.Edges {
		edgeSource[edge.ID] = edge.Source
	}
	for _, node := range flow.Nodes {
		nodes[node.ID] = true
	}
	for _, override := range request.Overrides {
		switch override.Type {
		case "force_edge":
			source, ok := edgeSource[override.EdgeID]
			if !ok {
				return result, errors.New("forced edge does not exist")
			}
			result.ForcedEdges[source] = override.EdgeID
		case "fail_node":
			if !nodes[override.NodeID] {
				return result, errors.New("failed node does not exist")
			}
			message := override.Message
			if message == "" {
				message = override.Code
			}
			result.FailedNodes[override.NodeID] = message
		default:
			return result, errors.New("override type must be force_edge or fail_node")
		}
	}
	return result, nil
}

func (s *Server) listRuns(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	runs, err := s.repository.ListRuns(c.Request.Context(), flow.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": runs})
}

func (s *Server) getRun(c *gin.Context) {
	run, ok := s.runWithAccess(c, domain.RoleViewer)
	if ok {
		c.JSON(http.StatusOK, runView(run))
	}
}

func (s *Server) getRunEvents(c *gin.Context) {
	run, ok := s.runWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	after := parseAfterSequence(c)
	events := []domain.Event{}
	for _, event := range run.Events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	c.JSON(http.StatusOK, events)
}

func (s *Server) pauseRun(c *gin.Context) {
	s.controlRun(c, s.runs.Pause)
}

func (s *Server) resumeRun(c *gin.Context) {
	s.controlRun(c, s.runs.Resume)
}

func (s *Server) stepRun(c *gin.Context) {
	s.controlRun(c, s.runs.Step)
}

func (s *Server) cancelRun(c *gin.Context) {
	s.controlRun(c, s.runs.Cancel)
}

func (s *Server) speedRun(c *gin.Context) {
	run, ok := s.runWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	var request struct {
		Speed      float64 `json:"speed,omitempty"`
		Multiplier float64 `json:"multiplier,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	speed := request.Multiplier
	if speed == 0 {
		speed = request.Speed
	}
	if err := s.runs.SetSpeed(run.ID, speed); err != nil {
		writeError(c, http.StatusConflict, "run.control_unavailable", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"multiplier": speed})
}

func (s *Server) controlRun(c *gin.Context, control func(string) error) {
	run, ok := s.runWithAccess(c, domain.RoleEditor)
	if !ok {
		return
	}
	if err := control(run.ID); err != nil {
		writeError(c, http.StatusConflict, "run.control_unavailable", err.Error(), nil)
		return
	}
	updated, _ := s.repository.RunByID(c.Request.Context(), run.ID)
	c.JSON(http.StatusAccepted, runView(updated))
}

func (s *Server) liveTicket(c *gin.Context) {
	run, ok := s.runWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	ticket, err := s.runs.NewTicket(run.ID, currentUser(c).ID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "ticket.failed", "Live ticket could not be created", nil)
		return
	}
	expiresAt := now().Add(30 * time.Second)
	websocketOrigin := strings.TrimRight(s.config.PublicWebSocketOrigin, "/")
	if websocketOrigin == "" {
		// Backwards-compatible fallback for embedded/test servers. Production
		// startup always supplies the externally visible, validated origin.
		websocketOrigin = "ws://" + c.Request.Host
	}
	liveURL := websocketOrigin + "/v1/runs/" + run.ID + "/live?ticket=" + url.QueryEscape(ticket)
	c.JSON(http.StatusCreated, gin.H{"ticket": ticket, "expiresAt": expiresAt, "url": liveURL})
}

func (s *Server) liveRun(c *gin.Context) {
	runID := c.Param("runId")
	userID, err := s.runs.ConsumeTicket(c.Query("ticket"), runID)
	if err != nil || userID == "" {
		writeError(c, http.StatusUnauthorized, "ticket.invalid", "Live ticket is invalid or expired", nil)
		return
	}
	run, err := s.repository.RunByID(c.Request.Context(), runID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if _, err := s.repository.ProjectRole(c.Request.Context(), run.ProjectID, userID); err != nil {
		writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(request *http.Request) bool {
			origin := request.Header.Get("Origin")
			return origin == "" || origin == s.config.PublicOrigin
		},
	}
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	events, unsubscribe, err := s.runs.Subscribe(runID, parseAfterSequence(c))
	if err != nil {
		_ = connection.WriteJSON(gin.H{"error": err.Error()})
		return
	}
	defer unsubscribe()
	_ = connection.SetReadDeadline(time.Now().Add(70 * time.Second))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	go func() {
		for {
			if _, _, readErr := connection.ReadMessage(); readErr != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, open := <-events:
			if !open {
				return
			}
			if err := connection.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (s *Server) createShare(c *gin.Context) {
	var request struct {
		VersionID string     `json:"versionId"`
		RunIDs    []string   `json:"runIds"`
		ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	}
	if !bindJSON(c, &request) {
		return
	}
	if len(request.RunIDs) > 20 {
		writeError(c, http.StatusUnprocessableEntity, "share.too_many_runs", "A share link can include at most 20 runs", nil)
		return
	}
	seenRunIDs := make(map[string]bool, len(request.RunIDs))
	for _, runID := range request.RunIDs {
		if seenRunIDs[runID] {
			writeError(c, http.StatusUnprocessableEntity, "share.duplicate_run", "runIds must not contain duplicates", gin.H{"runId": runID})
			return
		}
		seenRunIDs[runID] = true
	}
	version, err := s.repository.VersionByID(c.Request.Context(), request.VersionID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	flow, err := s.repository.FlowByID(c.Request.Context(), version.FlowID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if !s.allowProject(c, flow.ProjectID, domain.RoleOwner) {
		return
	}
	if pathFlowID := c.Param("flowId"); pathFlowID != "" && pathFlowID != flow.ID {
		writeError(c, http.StatusBadRequest, "share.version_mismatch", "Version does not belong to this flow", nil)
		return
	}
	if request.ExpiresAt != nil && !request.ExpiresAt.After(now()) {
		writeError(c, http.StatusBadRequest, "share.invalid_expiry", "expiresAt must be in the future", nil)
		return
	}
	for _, runID := range request.RunIDs {
		run, runErr := s.repository.RunByID(c.Request.Context(), runID)
		if runErr != nil || run.VersionID != version.ID || run.Status != "completed" && run.Status != "failed" {
			writeError(c, http.StatusBadRequest, "share.invalid_run", "Shared runs must be completed and belong to the selected version", gin.H{"runId": runID})
			return
		}
	}
	rawToken, err := auth.NewToken()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "share.failed", "Share link could not be created", nil)
		return
	}
	share := domain.ShareLink{
		ID: uuid.NewString(), ProjectID: flow.ProjectID, FlowID: flow.ID, VersionID: version.ID, RunIDs: request.RunIDs,
		TokenHash: auth.HashToken(rawToken), CreatedBy: currentUser(c).ID, CreatedAt: now(), ExpiresAt: request.ExpiresAt,
	}
	if err := s.repository.CreateShare(c.Request.Context(), share); err != nil {
		mapStoreError(c, err)
		return
	}
	publicURL := strings.TrimRight(s.config.PublicOrigin, "/") + "/compartir/" + rawToken
	response := shareView(share)
	response["token"], response["publicUrl"] = rawToken, publicURL
	c.JSON(http.StatusCreated, response)
}

func (s *Server) listShareLinks(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleOwner)
	if !ok {
		return
	}
	shares, err := s.repository.ListShares(c.Request.Context(), flow.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]gin.H, 0, len(shares))
	for _, share := range shares {
		items = append(items, shareView(share))
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) revokeShare(c *gin.Context) {
	share, err := s.repository.ShareByID(c.Request.Context(), c.Param("shareId"))
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if !s.allowProject(c, share.ProjectID, domain.RoleOwner) {
		return
	}
	if err := s.repository.RevokeShare(c.Request.Context(), share.ID); err != nil {
		mapStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) publicShare(c *gin.Context) {
	share, err := s.repository.ShareByTokenHash(c.Request.Context(), auth.HashToken(c.Param("token")))
	if err != nil || share.RevokedAt != nil || share.ExpiresAt != nil && !share.ExpiresAt.After(now()) {
		writeError(c, http.StatusNotFound, "share.not_found", "Share link not found", nil)
		return
	}
	if _, err := s.repository.FlowByID(c.Request.Context(), share.FlowID); err != nil {
		writeError(c, http.StatusNotFound, "share.not_found", "Share link not found", nil)
		return
	}
	version, err := s.repository.VersionByID(c.Request.Context(), share.VersionID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	runs := []gin.H{}
	for _, runID := range share.RunIDs {
		run, runErr := s.repository.RunByID(c.Request.Context(), runID)
		if runErr == nil {
			runs = append(runs, publicRun(run))
		}
	}
	c.JSON(http.StatusOK, gin.H{"definition": version.Definition, "runs": runs})
}

func publicRun(run domain.Run) gin.H {
	path := make([]string, 0, len(run.NodeRuns))
	timings := map[string]int64{}
	for _, nodeRun := range run.NodeRuns {
		if nodeRun.Status != "skipped" {
			path = append(path, nodeRun.NodeID)
		}
		timings[nodeRun.NodeID] += nodeRun.CompletedMS - nodeRun.StartedMS
	}
	return gin.H{"id": run.ID, "status": run.Status, "path": path, "timings": timings}
}

func shareView(share domain.ShareLink) gin.H {
	return gin.H{
		"id": share.ID, "flowId": share.FlowID, "versionId": share.VersionID, "runIds": share.RunIDs,
		"createdAt": share.CreatedAt, "expiresAt": share.ExpiresAt, "revoked": share.RevokedAt != nil,
	}
}

func (s *Server) runWithAccess(c *gin.Context, required domain.Role) (domain.Run, bool) {
	run, err := s.repository.RunByID(c.Request.Context(), c.Param("runId"))
	if err != nil {
		mapStoreError(c, err)
		return domain.Run{}, false
	}
	if !s.allowProject(c, run.ProjectID, required) {
		return domain.Run{}, false
	}
	return run, true
}

func runView(run domain.Run) gin.H {
	logicalTime := int64(0)
	if len(run.Events) > 0 {
		logicalTime = run.Events[len(run.Events)-1].LogicalTimeMS
	}
	var result any
	if run.Output != nil {
		result = run.Output
	}
	view := gin.H{
		"id": run.ID, "flowVersionId": run.VersionID, "status": run.Status,
		"triggerNodeId": run.TriggerID, "logicalTimeMs": logicalTime, "playbackSpeed": 1,
		"createdAt": run.CreatedAt, "startedAt": run.StartedAt, "finishedAt": run.CompletedAt,
		"result": result,
	}
	if run.TraceID != "" {
		view["traceId"] = run.TraceID
	}
	return view
}

func recordSimulationTelemetry(c *gin.Context, started time.Time, result engine.SimulationResult, runErr error) {
	status := result.Status
	if runErr != nil {
		status = "rejected"
	}
	telemetry.SimulationDuration(c.Request.Context(), time.Since(started), status)
	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(
		attribute.String("flowverse.run.plan_status", status),
		attribute.Int("flowverse.run.event_count", len(result.Events)),
		attribute.Int("flowverse.run.node_visit_count", len(result.NodeRuns)),
	)
	if runErr != nil {
		span.RecordError(runErr)
	}
}
