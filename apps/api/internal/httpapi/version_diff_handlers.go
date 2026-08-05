package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
)

// diffFlowRevisions compares a published version with the current draft, the
// draft with a published version, or two published versions. An omitted side
// means draft; at least one side must name a version.
func (s *Server) diffFlowRevisions(c *gin.Context) {
	flow, ok := s.flowWithAccess(c, domain.RoleViewer)
	if !ok {
		return
	}

	baseVersionID, targetVersionID, ok := parseDiffVersionIDs(c)
	if !ok {
		return
	}
	baseRef, baseDefinition, ok := s.resolveDiffRevision(c, flow, baseVersionID)
	if !ok {
		return
	}
	targetRef, targetDefinition, ok := s.resolveDiffRevision(c, flow, targetVersionID)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, engine.DiffFlows(
		flow.ID,
		baseRef,
		baseDefinition,
		targetRef,
		targetDefinition,
	))
}

func parseDiffVersionIDs(c *gin.Context) (string, string, bool) {
	baseRaw, hasBase := c.GetQuery("baseVersionId")
	targetRaw, hasTarget := c.GetQuery("targetVersionId")
	if !hasBase && !hasTarget {
		writeError(c, http.StatusBadRequest, "diff.revision_required", "At least one revision must be a published version", nil)
		return "", "", false
	}

	baseID, ok := parseDiffVersionID(c, "baseVersionId", baseRaw, hasBase)
	if !ok {
		return "", "", false
	}
	targetID, ok := parseDiffVersionID(c, "targetVersionId", targetRaw, hasTarget)
	if !ok {
		return "", "", false
	}
	return baseID, targetID, true
}

func parseDiffVersionID(c *gin.Context, parameter, raw string, present bool) (string, bool) {
	if !present {
		return "", true
	}
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		writeError(c, http.StatusBadRequest, "diff.invalid_version_id", parameter+" must be a valid UUID", gin.H{"parameter": parameter})
		return "", false
	}
	return parsed.String(), true
}

func (s *Server) resolveDiffRevision(
	c *gin.Context,
	flow domain.Flow,
	versionID string,
) (domain.FlowRevisionRef, domain.FlowDefinition, bool) {
	if versionID == "" {
		return domain.FlowRevisionRef{
			Kind:     domain.DiffRevisionDraft,
			Checksum: flow.DraftETag,
		}, flow.Draft, true
	}

	version, err := s.repository.VersionByID(c.Request.Context(), versionID)
	if err != nil {
		mapStoreError(c, err)
		return domain.FlowRevisionRef{}, domain.FlowDefinition{}, false
	}
	if version.FlowID != flow.ID {
		writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
		return domain.FlowRevisionRef{}, domain.FlowDefinition{}, false
	}
	return domain.FlowRevisionRef{
		Kind:          domain.DiffRevisionVersion,
		Checksum:      version.Checksum,
		VersionID:     version.ID,
		VersionNumber: version.Number,
	}, version.Definition, true
}
