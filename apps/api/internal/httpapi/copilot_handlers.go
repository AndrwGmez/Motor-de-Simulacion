package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/copilot"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/incident"
)

func (s *Server) adviseFlow(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}
	var body struct {
		Question      string `json:"question"`
		BaseVersionID string `json:"baseVersionId,omitempty"`
		RunID         string `json:"runId,omitempty"`
	}
	if !bindJSON(c, &body) {
		return
	}
	body.Question = strings.TrimSpace(body.Question)
	questionLength := len([]rune(body.Question))
	if questionLength < 3 || questionLength > 4000 {
		writeError(c, http.StatusUnprocessableEntity, "copilot.invalid_question", "question must contain between 3 and 4000 characters", nil)
		return
	}

	request := copilot.Request{
		Question: body.Question, FlowID: flow.ID, Definition: flow.Draft,
		TargetRef:        domain.FlowRevisionRef{Kind: domain.DiffRevisionDraft, Checksum: flow.DraftETag},
		SafetyIdentifier: copilotSafetyIdentifier(currentUser(c).ID),
	}
	if strings.TrimSpace(body.BaseVersionID) != "" {
		versionID, valid := parseCopilotUUID(c, "baseVersionId", body.BaseVersionID)
		if !valid {
			return
		}
		version, err := s.repository.VersionByID(c.Request.Context(), versionID)
		if err != nil {
			mapStoreError(c, err)
			return
		}
		if version.FlowID != flow.ID {
			writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
			return
		}
		request.Baseline = &version.Definition
		request.BaselineRef = &domain.FlowRevisionRef{
			Kind: domain.DiffRevisionVersion, Checksum: version.Checksum,
			VersionID: version.ID, VersionNumber: version.Number,
		}
	}
	if strings.TrimSpace(body.RunID) != "" {
		runID, valid := parseCopilotUUID(c, "runId", body.RunID)
		if !valid {
			return
		}
		run, err := s.repository.RunByID(c.Request.Context(), runID)
		if err != nil {
			mapStoreError(c, err)
			return
		}
		if run.FlowID != flow.ID {
			writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
			return
		}
		report := incident.Build(run)
		request.Incident = &report
	}

	response, err := s.copilot.Advise(c.Request.Context(), request)
	if err != nil {
		if errors.Is(err, copilot.ErrUngrounded) {
			writeError(c, http.StatusBadGateway, "copilot.ungrounded", "Copilot did not return evidence-grounded recommendations", nil)
			return
		}
		writeError(c, http.StatusBadGateway, "copilot.unavailable", "Copilot is temporarily unavailable", nil)
		return
	}
	c.JSON(http.StatusOK, response)
}

func parseCopilotUUID(c *gin.Context, parameter, value string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		writeError(c, http.StatusBadRequest, "copilot.invalid_reference", parameter+" must be a valid UUID", gin.H{"parameter": parameter})
		return "", false
	}
	return parsed.String(), true
}

func copilotSafetyIdentifier(userID string) string {
	sum := sha256.Sum256([]byte("flowverse-copilot:" + userID))
	return "fv_" + hex.EncodeToString(sum[:16])
}
