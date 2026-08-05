package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/flowverse/flowverse-api/internal/enterprise"
	"github.com/flowverse/flowverse-api/internal/store"
)

const (
	defaultAuditPageLimit = 100
	maxAuditPageLimit     = 200
	maxAuditVerifyEvents  = 100000
)

func (s *Server) listOrganizationAudit(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	after, ok := parseUnsignedQuery(c, "afterSequence", 0)
	if !ok {
		return
	}
	limit, ok := parseAuditLimit(c)
	if !ok {
		return
	}
	events, err := repository.ListAuditEvents(c.Request.Context(), access.Organization.ID, after, limit+1)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	nextAfter := after
	if len(events) > 0 {
		nextAfter = events[len(events)-1].Sequence
	}
	if events == nil {
		events = []enterprise.AuditEvent{}
	}
	c.JSON(http.StatusOK, gin.H{
		"items":             events,
		"afterSequence":     after,
		"nextAfterSequence": nextAfter,
		"limit":             limit,
		"hasMore":           hasMore,
	})
}

func (s *Server) verifyOrganizationAudit(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	events, checkpoint, err := loadStableAuditSnapshot(c, repository, access.Organization.ID)
	if err != nil {
		if errors.Is(err, errAuditVerificationLimit) {
			writeError(c, http.StatusServiceUnavailable, "audit.verification_limit", "Audit chain is too large for synchronous verification", nil)
			return
		}
		mapStoreError(c, err)
		return
	}
	verificationErr := enterprise.VerifyAuditChainAgainst(events, checkpoint)
	response := gin.H{
		"valid":      verificationErr == nil,
		"eventCount": len(events),
		"checkpoint": checkpoint,
	}
	if verificationErr != nil {
		failure := gin.H{"reason": verificationErr.Error()}
		var integrity *enterprise.AuditIntegrityError
		if errors.As(verificationErr, &integrity) {
			failure = gin.H{"index": integrity.Index, "reason": integrity.Reason}
			if integrity.EventID != "" {
				failure["eventId"] = integrity.EventID
			}
		}
		response["failure"] = failure
	}
	c.JSON(http.StatusOK, response)
}

var errAuditVerificationLimit = errors.New("audit verification event limit exceeded")

func loadStableAuditSnapshot(
	c *gin.Context,
	repository store.EnterpriseRepository,
	organizationID string,
) ([]enterprise.AuditEvent, enterprise.AuditCheckpoint, error) {
	var events []enterprise.AuditEvent
	var checkpoint enterprise.AuditCheckpoint
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		events, err = loadAuditChain(c, repository, organizationID)
		if err != nil {
			return nil, enterprise.AuditCheckpoint{}, err
		}
		checkpoint, err = repository.GetAuditCheckpoint(c.Request.Context(), organizationID)
		if err != nil {
			return nil, enterprise.AuditCheckpoint{}, err
		}
		if auditSnapshotReachesCheckpoint(events, checkpoint) {
			return events, checkpoint, nil
		}
	}
	// A persistent mismatch is returned to the verifier so corruption or a
	// truncated tail is reported as valid=false instead of an opaque 500.
	return events, checkpoint, nil
}

func auditSnapshotReachesCheckpoint(events []enterprise.AuditEvent, checkpoint enterprise.AuditCheckpoint) bool {
	if len(events) == 0 {
		return checkpoint.LastSequence == 0 && checkpoint.LastHash == enterprise.GenesisAuditHash
	}
	last := events[len(events)-1]
	return last.OrganizationID == checkpoint.OrganizationID && last.Sequence == checkpoint.LastSequence && last.Hash == checkpoint.LastHash
}

func loadAuditChain(c *gin.Context, repository store.EnterpriseRepository, organizationID string) ([]enterprise.AuditEvent, error) {
	result := make([]enterprise.AuditEvent, 0)
	var after uint64
	for {
		page, err := repository.ListAuditEvents(c.Request.Context(), organizationID, after, store.MaxAuditListLimit)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return result, nil
		}
		if len(result)+len(page) > maxAuditVerifyEvents {
			return nil, errAuditVerificationLimit
		}
		last := page[len(page)-1].Sequence
		if last <= after {
			return nil, errors.New("audit repository returned a non-advancing page")
		}
		result = append(result, page...)
		after = last
		if len(page) < store.MaxAuditListLimit {
			return result, nil
		}
	}
}

func parseUnsignedQuery(c *gin.Context, name string, fallback uint64) (uint64, bool) {
	raw, present := c.GetQuery(name)
	if !present {
		return fallback, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "request.invalid_query", name+" must be an unsigned integer", gin.H{"parameter": name})
		return 0, false
	}
	return value, true
}

func parseAuditLimit(c *gin.Context) (int, bool) {
	raw, present := c.GetQuery("limit")
	if !present {
		return defaultAuditPageLimit, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxAuditPageLimit {
		writeError(c, http.StatusBadRequest, "request.invalid_query", "limit must be between 1 and 200", gin.H{"parameter": "limit"})
		return 0, false
	}
	return value, true
}
