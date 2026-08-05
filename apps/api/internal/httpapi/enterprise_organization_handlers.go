package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
	"github.com/flowverse/flowverse-api/internal/store"
)

func (s *Server) createOrganization(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	timestamp := now()
	organization := enterprise.Organization{
		ID: uuid.NewString(), Slug: strings.TrimSpace(request.Slug), Name: strings.TrimSpace(request.Name),
		Status: enterprise.OrganizationActive, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
	joinedAt := timestamp
	owner := enterprise.OrganizationMembership{
		OrganizationID: organization.ID, UserID: currentUser(c).ID,
		Role: enterprise.OrganizationOwner, Status: enterprise.MembershipActive,
		CreatedAt: timestamp, JoinedAt: &joinedAt,
	}
	if err := organization.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	if err := owner.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, organization.ID, "organization.create", "organization", organization.ID,
		map[string]any{"slug": organization.Slug})
	if err := repository.CreateOrganizationWithOwner(ctx, organization, owner); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, organization)
}

func (s *Server) listOrganizations(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	organizations, err := repository.ListOrganizations(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]enterprise.Organization, 0, len(organizations))
	for _, organization := range organizations {
		if organization.Status != enterprise.OrganizationActive {
			continue
		}
		membership, membershipErr := repository.GetMembership(c.Request.Context(), organization.ID, currentUser(c).ID)
		if membershipErr == nil && membership.Status == enterprise.MembershipActive {
			items = append(items, organization)
		}
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].Name == items[right].Name {
			return items[left].ID < items[right].ID
		}
		return items[left].Name < items[right].Name
	})
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getOrganization(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository)
	if ok {
		c.JSON(http.StatusOK, access.Organization)
	}
}

func (s *Server) listOrganizationMembers(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	memberships, err := repository.ListMemberships(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	items := make([]gin.H, 0, len(memberships))
	for _, membership := range memberships {
		user, userErr := s.repository.UserByID(c.Request.Context(), membership.UserID)
		if userErr != nil {
			mapStoreError(c, userErr)
			return
		}
		items = append(items, enterpriseMemberView(user, membership))
	}
	sort.Slice(items, func(left, right int) bool {
		leftEmail, _ := items[left]["email"].(string)
		rightEmail, _ := items[right]["email"].(string)
		if leftEmail == rightEmail {
			return items[left]["userId"].(string) < items[right]["userId"].(string)
		}
		return leftEmail < rightEmail
	})
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) setOrganizationMember(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	var request struct {
		Email  string                      `json:"email"`
		Role   enterprise.OrganizationRole `json:"role"`
		Status enterprise.MembershipStatus `json:"status,omitempty"`
	}
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(request.Email))
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email || len(email) > 320 {
		writeError(c, http.StatusUnprocessableEntity, "member.invalid_email", "email must be a canonical email address", nil)
		return
	}
	if !request.Role.Valid() {
		writeError(c, http.StatusUnprocessableEntity, "member.invalid_role", "role must be owner, admin, member, or auditor", nil)
		return
	}
	if request.Status == "" {
		request.Status = enterprise.MembershipActive
	}
	if request.Status != enterprise.MembershipActive && request.Status != enterprise.MembershipSuspended {
		writeError(c, http.StatusUnprocessableEntity, "member.invalid_status", "status must be active or suspended", nil)
		return
	}

	target, err := s.repository.UserByEmail(c.Request.Context(), email)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	existing, getErr := repository.GetMembership(c.Request.Context(), access.Organization.ID, target.ID)
	exists := getErr == nil
	if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
		mapStoreError(c, getErr)
		return
	}
	if access.Membership.Role == enterprise.OrganizationAdmin {
		if request.Role == enterprise.OrganizationOwner || (exists && existing.Role == enterprise.OrganizationOwner) {
			writeEnterpriseNotFound(c)
			return
		}
	}
	removesActiveOwner := exists && existing.Role == enterprise.OrganizationOwner && existing.Status == enterprise.MembershipActive &&
		(request.Role != enterprise.OrganizationOwner || request.Status != enterprise.MembershipActive)
	if removesActiveOwner {
		memberships, listErr := repository.ListMemberships(c.Request.Context(), access.Organization.ID)
		if listErr != nil {
			mapStoreError(c, listErr)
			return
		}
		activeOwners := 0
		for _, membership := range memberships {
			if membership.Role == enterprise.OrganizationOwner && membership.Status == enterprise.MembershipActive {
				activeOwners++
			}
		}
		if activeOwners <= 1 {
			if _, audited := s.appendEnterpriseAudit(c, repository, access.Organization.ID,
				"organization.member.set", "organization_member", target.ID, enterprise.AuditDenied,
				map[string]any{"role": request.Role, "status": request.Status, "reason": "last_owner"}); !audited {
				return
			}
			writeError(c, http.StatusConflict, "organization.last_owner", "The last active owner cannot be demoted or suspended", nil)
			return
		}
	}

	timestamp := now()
	membership := enterprise.OrganizationMembership{
		OrganizationID: access.Organization.ID, UserID: target.ID,
		Role: request.Role, Status: request.Status, CreatedAt: timestamp,
	}
	if exists {
		membership.CreatedAt = existing.CreatedAt
		membership.JoinedAt = existing.JoinedAt
	}
	if membership.Status == enterprise.MembershipActive && membership.JoinedAt == nil {
		joinedAt := timestamp
		membership.JoinedAt = &joinedAt
	}
	if err := membership.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.member.set", "organization_member", target.ID,
		map[string]any{"role": membership.Role, "status": membership.Status})
	if err := repository.SetMembership(ctx, membership); err != nil {
		if errors.Is(err, store.ErrLastOrganizationOwner) {
			if _, audited := s.appendEnterpriseAudit(c, repository, access.Organization.ID,
				"organization.member.set", "organization_member", target.ID, enterprise.AuditDenied,
				map[string]any{"role": request.Role, "status": request.Status, "reason": "last_owner"}); !audited {
				return
			}
		}
		writeEnterpriseMutationError(c, err)
		return
	}
	status := http.StatusCreated
	if exists {
		status = http.StatusOK
	}
	c.JSON(status, enterpriseMemberView(target, membership))
}

func (s *Server) listOrganizationProjects(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository)
	if !ok {
		return
	}
	projects, err := repository.ListOrganizationProjects(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if projects == nil {
		projects = []domain.Project{}
	}
	c.JSON(http.StatusOK, gin.H{"items": projects})
}

func (s *Server) attachOrganizationProject(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	projectID, ok := canonicalUUIDParam(c, "projectId")
	if !ok {
		return
	}
	role, err := s.repository.ProjectRole(c.Request.Context(), projectID, currentUser(c).ID)
	if err != nil || role != domain.RoleOwner {
		writeEnterpriseNotFound(c)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.project.attach", "project", projectID, nil)
	if err := repository.AttachProjectToOrganization(ctx, access.Organization.ID, projectID); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	project, err := s.repository.ProjectByID(c.Request.Context(), projectID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, project)
}
