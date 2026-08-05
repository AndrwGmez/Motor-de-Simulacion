package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/enterprise"
)

type ssoConnectionRequest struct {
	Name                   string                 `json:"name"`
	Protocol               enterprise.SSOProtocol `json:"protocol"`
	IssuerURL              string                 `json:"issuerUrl,omitempty"`
	MetadataURL            string                 `json:"metadataUrl,omitempty"`
	EntityID               string                 `json:"entityId,omitempty"`
	SignInURL              string                 `json:"signInUrl,omitempty"`
	CertificateFingerprint string                 `json:"certificateFingerprint,omitempty"`
	Domains                []string               `json:"domains"`
	Enabled                *bool                  `json:"enabled"`
}

func (s *Server) listSSOConnections(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	connections, err := repository.ListSSOConnections(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if connections == nil {
		connections = []enterprise.SSOConnection{}
	}
	c.JSON(http.StatusOK, gin.H{"items": connections})
}

func (s *Server) getSSOConnection(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository,
		enterprise.OrganizationOwner, enterprise.OrganizationAdmin, enterprise.OrganizationAuditor)
	if !ok {
		return
	}
	connectionID, ok := canonicalUUIDParam(c, "connectionId")
	if !ok {
		return
	}
	connection, err := repository.GetSSOConnection(c.Request.Context(), access.Organization.ID, connectionID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, connection)
}

func (s *Server) createSSOConnection(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	var request ssoConnectionRequest
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if request.Enabled == nil {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{"field": "enabled", "reason": "is required"})
		return
	}
	timestamp := now()
	connection := ssoConnectionFromRequest(request)
	connection.ID, connection.OrganizationID = uuid.NewString(), access.Organization.ID
	connection.CreatedAt, connection.UpdatedAt = timestamp, timestamp
	connection = connection.Normalize()
	if err := connection.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.sso.create", "sso_connection", connection.ID,
		map[string]any{"protocol": connection.Protocol, "enabled": connection.Enabled, "domainCount": len(connection.Domains)})
	if err := repository.SaveSSOConnection(ctx, connection); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, connection)
}

func (s *Server) updateSSOConnection(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	connectionID, ok := canonicalUUIDParam(c, "connectionId")
	if !ok {
		return
	}
	existing, err := repository.GetSSOConnection(c.Request.Context(), access.Organization.ID, connectionID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	var request ssoConnectionRequest
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if request.Enabled == nil {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{"field": "enabled", "reason": "is required"})
		return
	}
	connection := ssoConnectionFromRequest(request)
	connection.ID, connection.OrganizationID = existing.ID, existing.OrganizationID
	connection.CreatedAt, connection.UpdatedAt = existing.CreatedAt, now()
	connection = connection.Normalize()
	if err := connection.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.sso.update", "sso_connection", connection.ID,
		map[string]any{"protocol": connection.Protocol, "enabled": connection.Enabled, "domainCount": len(connection.Domains)})
	if err := repository.SaveSSOConnection(ctx, connection); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, connection)
}

func ssoConnectionFromRequest(request ssoConnectionRequest) enterprise.SSOConnection {
	return enterprise.SSOConnection{
		Name: request.Name, Protocol: request.Protocol,
		IssuerURL: request.IssuerURL, MetadataURL: request.MetadataURL,
		EntityID: request.EntityID, SignInURL: request.SignInURL,
		CertificateFingerprint: request.CertificateFingerprint,
		Domains:                request.Domains, Enabled: *request.Enabled,
	}
}

type pluginRegistrationRequest struct {
	PluginKey    string                  `json:"pluginKey"`
	Version      string                  `json:"version"`
	Status       enterprise.PluginStatus `json:"status,omitempty"`
	SourceURL    string                  `json:"sourceUrl"`
	Checksum     string                  `json:"checksum"`
	Capabilities []string                `json:"capabilities"`
}

func (s *Server) listPluginRegistrations(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository)
	if !ok {
		return
	}
	registrations, err := repository.ListPluginRegistrations(c.Request.Context(), access.Organization.ID)
	if err != nil {
		mapStoreError(c, err)
		return
	}
	if registrations == nil {
		registrations = []enterprise.PluginRegistration{}
	}
	c.JSON(http.StatusOK, gin.H{"items": registrations})
}

func (s *Server) getPluginRegistration(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository)
	if !ok {
		return
	}
	registrationID, ok := canonicalUUIDParam(c, "registrationId")
	if !ok {
		return
	}
	registration, err := repository.GetPluginRegistration(c.Request.Context(), access.Organization.ID, registrationID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, registration)
}

func (s *Server) createPluginRegistration(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	var request pluginRegistrationRequest
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if request.Capabilities == nil {
		writeError(c, http.StatusUnprocessableEntity, "enterprise.invalid", "Enterprise resource violates the contract", gin.H{"field": "capabilities", "reason": "is required"})
		return
	}
	if request.Status == "" {
		request.Status = enterprise.PluginDisabled
	}
	if request.Status == enterprise.PluginRevoked {
		writeError(c, http.StatusUnprocessableEntity, "plugin.invalid_status", "New plugins cannot start revoked", nil)
		return
	}
	timestamp := now()
	registration := enterprise.PluginRegistration{
		ID: uuid.NewString(), OrganizationID: access.Organization.ID,
		PluginKey: request.PluginKey, Version: request.Version, Status: request.Status,
		SourceURL: request.SourceURL, Checksum: request.Checksum, Capabilities: request.Capabilities,
		InstalledBy: currentUser(c).ID, CreatedAt: timestamp, UpdatedAt: timestamp,
	}.Normalize()
	if err := registration.Validate(); err != nil {
		writeEnterpriseValidation(c, err)
		return
	}
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.plugin.register", "plugin_registration", registration.ID,
		map[string]any{"pluginKey": registration.PluginKey, "version": registration.Version, "status": registration.Status})
	if err := repository.SavePluginRegistration(ctx, registration); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, registration)
}

func (s *Server) updatePluginRegistrationStatus(c *gin.Context) {
	repository, ok := s.enterpriseRepository(c)
	if !ok {
		return
	}
	access, ok := s.organizationAccess(c, repository, enterprise.OrganizationOwner, enterprise.OrganizationAdmin)
	if !ok {
		return
	}
	registrationID, ok := canonicalUUIDParam(c, "registrationId")
	if !ok {
		return
	}
	existing, err := repository.GetPluginRegistration(c.Request.Context(), access.Organization.ID, registrationID)
	if err != nil {
		writeEnterpriseAccessError(c, err)
		return
	}
	if existing.Status == enterprise.PluginRevoked {
		writeError(c, http.StatusConflict, "plugin.revoked", "Revoked plugin registrations are immutable", nil)
		return
	}
	var request struct {
		Status enterprise.PluginStatus `json:"status"`
	}
	if !bindEnterpriseJSON(c, &request) {
		return
	}
	if !request.Status.Valid() {
		writeError(c, http.StatusUnprocessableEntity, "plugin.invalid_status", "status must be active, disabled, or revoked", nil)
		return
	}
	updatedAt := now()
	ctx := s.enterpriseMutationContext(c, access.Organization.ID, "organization.plugin.status", "plugin_registration", existing.ID,
		map[string]any{"pluginKey": existing.PluginKey, "previousStatus": existing.Status, "status": request.Status})
	if err := repository.SetPluginRegistrationStatus(ctx, access.Organization.ID, existing.ID, request.Status, updatedAt); err != nil {
		writeEnterpriseMutationError(c, err)
		return
	}
	existing.Status, existing.UpdatedAt = request.Status, updatedAt
	c.JSON(http.StatusOK, existing)
}
