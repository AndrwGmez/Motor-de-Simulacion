package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
	"github.com/flowverse/flowverse-api/internal/store"
)

const maxEnterpriseBodyBytes = 64 << 10

type enterpriseAccess struct {
	Organization enterprise.Organization
	Membership   enterprise.OrganizationMembership
}

func (s *Server) enterpriseRepository(c *gin.Context) (store.EnterpriseRepository, bool) {
	repository, ok := s.repository.(store.EnterpriseRepository)
	if !ok {
		writeError(c, http.StatusServiceUnavailable, "enterprise.unavailable", "Enterprise control plane is unavailable", nil)
		return nil, false
	}
	return repository, true
}

func (s *Server) organizationAccess(
	c *gin.Context,
	repository store.EnterpriseRepository,
	allowedRoles ...enterprise.OrganizationRole,
) (enterpriseAccess, bool) {
	organizationID, ok := canonicalUUIDParam(c, "organizationId")
	if !ok {
		return enterpriseAccess{}, false
	}
	membership, err := repository.GetMembership(c.Request.Context(), organizationID, currentUser(c).ID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return enterpriseAccess{}, false
	}
	if membership.Status != enterprise.MembershipActive || !enterpriseRoleAllowed(membership.Role, allowedRoles) {
		writeEnterpriseNotFound(c)
		return enterpriseAccess{}, false
	}
	organization, err := repository.GetOrganization(c.Request.Context(), organizationID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return enterpriseAccess{}, false
	}
	if organization.Status != enterprise.OrganizationActive {
		writeEnterpriseNotFound(c)
		return enterpriseAccess{}, false
	}
	return enterpriseAccess{Organization: organization, Membership: membership}, true
}

func enterpriseRoleAllowed(role enterprise.OrganizationRole, allowed []enterprise.OrganizationRole) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}

func canonicalUUIDParam(c *gin.Context, name string) (string, bool) {
	raw := c.Param(name)
	parsed, err := uuid.Parse(raw)
	if err != nil || raw != parsed.String() {
		writeError(c, http.StatusBadRequest, "request.invalid_uuid", name+" must be a canonical UUID", gin.H{"parameter": name})
		return "", false
	}
	return parsed.String(), true
}

func bindEnterpriseJSON(c *gin.Context, target any) bool {
	if c.Request.Body == nil {
		writeError(c, http.StatusBadRequest, "request.invalid_json", "Request body is required", nil)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxEnterpriseBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(c, http.StatusRequestEntityTooLarge, "request.too_large", "Enterprise request exceeds 64 KiB", nil)
			return false
		}
		writeError(c, http.StatusBadRequest, "request.invalid_json", "Request body is invalid", gin.H{"reason": err.Error()})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(c, http.StatusBadRequest, "request.invalid_json", "Request body must contain exactly one JSON document", nil)
		return false
	}
	return true
}

func writeEnterpriseValidation(c *gin.Context, err error) {
	var validation *enterprise.ValidationError
	if errors.As(err, &validation) {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{
			"field": validation.Field, "reason": validation.Message,
		})
		return
	}
	writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", nil)
}

func writeEnterpriseAccessError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeEnterpriseNotFound(c)
		return
	}
	mapStoreError(c, err)
}

func writeEnterpriseNotFound(c *gin.Context) {
	writeError(c, http.StatusNotFound, "resource.not_found", "Resource not found", nil)
}

func writeEnterpriseMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, enterprise.ErrPluginRevoked):
		writeError(c, http.StatusConflict, "plugin.revoked", "Revoked plugin registrations are immutable", nil)
	case errors.Is(err, store.ErrLastOrganizationOwner):
		writeError(c, http.StatusConflict, "organization.last_owner", "The last active owner cannot be demoted or suspended", nil)
	default:
		mapStoreError(c, err)
	}
}

func (s *Server) appendEnterpriseAudit(
	c *gin.Context,
	repository store.EnterpriseRepository,
	organizationID, action, resourceType, resourceID string,
	outcome enterprise.AuditOutcome,
	metadata map[string]any,
) (enterprise.AuditEvent, bool) {
	event := s.enterpriseAuditDraft(c, organizationID, action, resourceType, resourceID, outcome, metadata)
	sealed, err := repository.AppendAuditEvent(c.Request.Context(), event)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "enterprise.audit_append_failed", "Operation could not be confirmed because its audit record was not persisted", nil)
		return enterprise.AuditEvent{}, false
	}
	return sealed, true
}

func (s *Server) enterpriseMutationContext(
	c *gin.Context,
	organizationID, action, resourceType, resourceID string,
	metadata map[string]any,
) context.Context {
	event := s.enterpriseAuditDraft(c, organizationID, action, resourceType, resourceID, enterprise.AuditSucceeded, metadata)
	return store.WithEnterpriseAudit(c.Request.Context(), event)
}

func (s *Server) enterpriseAuditDraft(
	c *gin.Context,
	organizationID, action, resourceType, resourceID string,
	outcome enterprise.AuditOutcome,
	metadata map[string]any,
) enterprise.AuditEvent {
	sourceIP := requestIP(c.Request)
	if net.ParseIP(sourceIP) == nil {
		sourceIP = ""
	}
	return enterprise.AuditEvent{
		ID:             uuid.NewString(),
		OrganizationID: organizationID,
		ActorID:        currentUser(c).ID,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Outcome:        outcome,
		RequestID:      strings.TrimSpace(c.Writer.Header().Get("X-Request-ID")),
		SourceIP:       sourceIP,
		Metadata:       metadata,
		OccurredAt:     now(),
	}
}

func enterpriseMemberView(user domain.User, membership enterprise.OrganizationMembership) gin.H {
	view := gin.H{
		"organizationId": membership.OrganizationID,
		"userId":         membership.UserID,
		"email":          user.Email,
		"displayName":    user.DisplayName,
		"role":           membership.Role,
		"status":         membership.Status,
		"createdAt":      membership.CreatedAt,
	}
	if membership.JoinedAt != nil {
		view["joinedAt"] = *membership.JoinedAt
	}
	return view
}
