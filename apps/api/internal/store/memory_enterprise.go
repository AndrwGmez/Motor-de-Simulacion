package store

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
)

type enterpriseMemoryState struct {
	organizations        map[string]enterprise.Organization
	organizationBySlug   map[string]string
	memberships          map[string]map[string]enterprise.OrganizationMembership
	ssoConnections       map[string]map[string]enterprise.SSOConnection
	ssoLocations         map[string]string
	ssoNames             map[string]map[string]string
	policyRules          map[string]map[string]enterprise.PolicyRule
	policyLocations      map[string]string
	plugins              map[string]map[string]enterprise.PluginRegistration
	pluginLocations      map[string]string
	pluginKeys           map[string]map[string]string
	auditEvents          map[string][]enterprise.AuditEvent
	auditLocations       map[string]string
	projectOrganizations map[string]string
}

func newEnterpriseMemoryState() enterpriseMemoryState {
	return enterpriseMemoryState{
		organizations:        map[string]enterprise.Organization{},
		organizationBySlug:   map[string]string{},
		memberships:          map[string]map[string]enterprise.OrganizationMembership{},
		ssoConnections:       map[string]map[string]enterprise.SSOConnection{},
		ssoLocations:         map[string]string{},
		ssoNames:             map[string]map[string]string{},
		policyRules:          map[string]map[string]enterprise.PolicyRule{},
		policyLocations:      map[string]string{},
		plugins:              map[string]map[string]enterprise.PluginRegistration{},
		pluginLocations:      map[string]string{},
		pluginKeys:           map[string]map[string]string{},
		auditEvents:          map[string][]enterprise.AuditEvent{},
		auditLocations:       map[string]string{},
		projectOrganizations: map[string]string{},
	}
}

func (m *Memory) ensureEnterpriseStateLocked() {
	if m.enterprise.organizations == nil {
		m.enterprise = newEnterpriseMemoryState()
	}
}

func (m *Memory) CreateOrganization(ctx context.Context, organization enterprise.Organization) error {
	if err := organization.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[organization.ID]; exists {
		return ErrConflict
	}
	if _, exists := m.enterprise.organizationBySlug[organization.Slug]; exists {
		return ErrConflict
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organization.ID)
	if err != nil {
		return err
	}
	m.enterprise.organizations[organization.ID] = organization
	m.enterprise.organizationBySlug[organization.Slug] = organization.ID
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) CreateOrganizationWithOwner(
	ctx context.Context,
	organization enterprise.Organization,
	owner enterprise.OrganizationMembership,
) error {
	if err := organization.Validate(); err != nil {
		return err
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	if owner.OrganizationID != organization.ID {
		return &enterprise.ValidationError{Field: "organizationId", Message: "owner membership must belong to the new organization"}
	}
	if owner.Role != enterprise.OrganizationOwner {
		return &enterprise.ValidationError{Field: "role", Message: "initial membership must be owner"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.users[owner.UserID]; !exists {
		return ErrNotFound
	}
	if _, exists := m.enterprise.organizations[organization.ID]; exists {
		return ErrConflict
	}
	if _, exists := m.enterprise.organizationBySlug[organization.Slug]; exists {
		return ErrConflict
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organization.ID)
	if err != nil {
		return err
	}
	m.enterprise.organizations[organization.ID] = organization
	m.enterprise.organizationBySlug[organization.Slug] = organization.ID
	m.enterprise.memberships[organization.ID] = map[string]enterprise.OrganizationMembership{
		owner.UserID: cloneMembership(owner),
	}
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) ListOrganizations(_ context.Context, userID string) ([]enterprise.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []enterprise.Organization{}
	for organizationID, memberships := range m.enterprise.memberships {
		if _, exists := memberships[userID]; !exists {
			continue
		}
		if organization, exists := m.enterprise.organizations[organizationID]; exists {
			result = append(result, organization)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result, nil
}

func (m *Memory) GetOrganization(_ context.Context, organizationID string) (enterprise.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	organization, exists := m.enterprise.organizations[organizationID]
	if !exists {
		return enterprise.Organization{}, ErrNotFound
	}
	return organization, nil
}

func (m *Memory) GetMembership(_ context.Context, organizationID, userID string) (enterprise.OrganizationMembership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	membership, exists := m.enterprise.memberships[organizationID][userID]
	if !exists {
		return enterprise.OrganizationMembership{}, ErrNotFound
	}
	return cloneMembership(membership), nil
}

func (m *Memory) ListMemberships(_ context.Context, organizationID string) ([]enterprise.OrganizationMembership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]enterprise.OrganizationMembership, 0, len(m.enterprise.memberships[organizationID]))
	for _, membership := range m.enterprise.memberships[organizationID] {
		result = append(result, cloneMembership(membership))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].UserID < result[right].UserID
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result, nil
}

func (m *Memory) SetMembership(ctx context.Context, membership enterprise.OrganizationMembership) error {
	if err := membership.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[membership.OrganizationID]; !exists {
		return ErrNotFound
	}
	if _, exists := m.users[membership.UserID]; !exists {
		return ErrNotFound
	}
	memberships := m.enterprise.memberships[membership.OrganizationID]
	if memberships == nil {
		memberships = map[string]enterprise.OrganizationMembership{}
		m.enterprise.memberships[membership.OrganizationID] = memberships
	}
	if current, exists := memberships[membership.UserID]; exists && !current.CreatedAt.Equal(membership.CreatedAt) {
		return ErrConflict
	}
	if membership.Role != enterprise.OrganizationOwner || membership.Status != enterprise.MembershipActive {
		activeOwners := 0
		for userID, current := range memberships {
			if userID != membership.UserID && current.Role == enterprise.OrganizationOwner && current.Status == enterprise.MembershipActive {
				activeOwners++
			}
		}
		if activeOwners == 0 {
			return ErrLastOrganizationOwner
		}
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, membership.OrganizationID)
	if err != nil {
		return err
	}
	memberships[membership.UserID] = cloneMembership(membership)
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) SaveSSOConnection(ctx context.Context, connection enterprise.SSOConnection) error {
	connection = connection.Normalize()
	if err := connection.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[connection.OrganizationID]; !exists {
		return ErrNotFound
	}
	if location, exists := m.enterprise.ssoLocations[connection.ID]; exists && location != connection.OrganizationID {
		return ErrConflict
	}
	connections := m.enterprise.ssoConnections[connection.OrganizationID]
	if connections == nil {
		connections = map[string]enterprise.SSOConnection{}
		m.enterprise.ssoConnections[connection.OrganizationID] = connections
	}
	names := m.enterprise.ssoNames[connection.OrganizationID]
	if names == nil {
		names = map[string]string{}
		m.enterprise.ssoNames[connection.OrganizationID] = names
	}
	if ownerID, exists := names[connection.Name]; exists && ownerID != connection.ID {
		return ErrConflict
	}
	previousName := ""
	if current, exists := connections[connection.ID]; exists {
		if !current.CreatedAt.Equal(connection.CreatedAt) || connection.UpdatedAt.Before(current.UpdatedAt) {
			return ErrConflict
		}
		previousName = current.Name
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, connection.OrganizationID)
	if err != nil {
		return err
	}
	connections[connection.ID] = cloneSSOConnection(connection)
	m.enterprise.ssoLocations[connection.ID] = connection.OrganizationID
	if previousName != "" && previousName != connection.Name {
		delete(names, previousName)
	}
	names[connection.Name] = connection.ID
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) GetSSOConnection(_ context.Context, organizationID, connectionID string) (enterprise.SSOConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	connection, exists := m.enterprise.ssoConnections[organizationID][connectionID]
	if !exists {
		return enterprise.SSOConnection{}, ErrNotFound
	}
	return cloneSSOConnection(connection), nil
}

func (m *Memory) ListSSOConnections(_ context.Context, organizationID string) ([]enterprise.SSOConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]enterprise.SSOConnection, 0, len(m.enterprise.ssoConnections[organizationID]))
	for _, connection := range m.enterprise.ssoConnections[organizationID] {
		result = append(result, cloneSSOConnection(connection))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name == result[right].Name {
			return result[left].ID < result[right].ID
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func (m *Memory) SavePolicyRule(ctx context.Context, rule enterprise.PolicyRule) error {
	rule = rule.Normalize()
	if err := rule.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[rule.OrganizationID]; !exists {
		return ErrNotFound
	}
	if location, exists := m.enterprise.policyLocations[rule.ID]; exists && location != rule.OrganizationID {
		return ErrConflict
	}
	rules := m.enterprise.policyRules[rule.OrganizationID]
	if rules == nil {
		rules = map[string]enterprise.PolicyRule{}
		m.enterprise.policyRules[rule.OrganizationID] = rules
	}
	if current, exists := rules[rule.ID]; exists &&
		(!current.CreatedAt.Equal(rule.CreatedAt) || rule.UpdatedAt.Before(current.UpdatedAt)) {
		return ErrConflict
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, rule.OrganizationID)
	if err != nil {
		return err
	}
	rules[rule.ID] = clonePolicyRule(rule)
	m.enterprise.policyLocations[rule.ID] = rule.OrganizationID
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) GetPolicyRule(_ context.Context, organizationID, ruleID string) (enterprise.PolicyRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rule, exists := m.enterprise.policyRules[organizationID][ruleID]
	if !exists {
		return enterprise.PolicyRule{}, ErrNotFound
	}
	return clonePolicyRule(rule), nil
}

func (m *Memory) ListPolicyRules(_ context.Context, organizationID string) ([]enterprise.PolicyRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]enterprise.PolicyRule, 0, len(m.enterprise.policyRules[organizationID]))
	for _, rule := range m.enterprise.policyRules[organizationID] {
		result = append(result, clonePolicyRule(rule))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (m *Memory) DeletePolicyRule(ctx context.Context, organizationID, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rules := m.enterprise.policyRules[organizationID]
	if _, exists := rules[ruleID]; !exists {
		return ErrNotFound
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organizationID)
	if err != nil {
		return err
	}
	delete(rules, ruleID)
	delete(m.enterprise.policyLocations, ruleID)
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) SavePluginRegistration(ctx context.Context, registration enterprise.PluginRegistration) error {
	registration = registration.Normalize()
	if err := registration.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[registration.OrganizationID]; !exists {
		return ErrNotFound
	}
	if location, exists := m.enterprise.pluginLocations[registration.ID]; exists && location != registration.OrganizationID {
		return ErrConflict
	}
	plugins := m.enterprise.plugins[registration.OrganizationID]
	if plugins == nil {
		plugins = map[string]enterprise.PluginRegistration{}
		m.enterprise.plugins[registration.OrganizationID] = plugins
	}
	keys := m.enterprise.pluginKeys[registration.OrganizationID]
	if keys == nil {
		keys = map[string]string{}
		m.enterprise.pluginKeys[registration.OrganizationID] = keys
	}
	if ownerID, exists := keys[registration.PluginKey]; exists && ownerID != registration.ID {
		return ErrConflict
	}
	previousKey := ""
	if current, exists := plugins[registration.ID]; exists {
		if current.Status == enterprise.PluginRevoked {
			return enterprise.ErrPluginRevoked
		}
		if !current.CreatedAt.Equal(registration.CreatedAt) || registration.UpdatedAt.Before(current.UpdatedAt) {
			return ErrConflict
		}
		previousKey = current.PluginKey
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, registration.OrganizationID)
	if err != nil {
		return err
	}
	plugins[registration.ID] = clonePluginRegistration(registration)
	m.enterprise.pluginLocations[registration.ID] = registration.OrganizationID
	if previousKey != "" && previousKey != registration.PluginKey {
		delete(keys, previousKey)
	}
	keys[registration.PluginKey] = registration.ID
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) GetPluginRegistration(_ context.Context, organizationID, registrationID string) (enterprise.PluginRegistration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	registration, exists := m.enterprise.plugins[organizationID][registrationID]
	if !exists {
		return enterprise.PluginRegistration{}, ErrNotFound
	}
	return clonePluginRegistration(registration), nil
}

func (m *Memory) ListPluginRegistrations(_ context.Context, organizationID string) ([]enterprise.PluginRegistration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]enterprise.PluginRegistration, 0, len(m.enterprise.plugins[organizationID]))
	for _, registration := range m.enterprise.plugins[organizationID] {
		result = append(result, clonePluginRegistration(registration))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].PluginKey == result[right].PluginKey {
			return result[left].ID < result[right].ID
		}
		return result[left].PluginKey < result[right].PluginKey
	})
	return result, nil
}

func (m *Memory) SetPluginRegistrationStatus(
	ctx context.Context,
	organizationID, registrationID string,
	status enterprise.PluginStatus,
	updatedAt time.Time,
) error {
	if !status.Valid() {
		return &enterprise.ValidationError{Field: "status", Message: "must be active, disabled, or revoked"}
	}
	if updatedAt.IsZero() {
		return &enterprise.ValidationError{Field: "updatedAt", Message: "is required"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	registration, exists := m.enterprise.plugins[organizationID][registrationID]
	if !exists {
		return ErrNotFound
	}
	if registration.Status == enterprise.PluginRevoked {
		return enterprise.ErrPluginRevoked
	}
	updatedAt = updatedAt.UTC()
	if updatedAt.Before(registration.UpdatedAt) {
		return ErrConflict
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organizationID)
	if err != nil {
		return err
	}
	registration.Status = status
	registration.UpdatedAt = updatedAt
	m.enterprise.plugins[organizationID][registrationID] = registration
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) AttachProjectToOrganization(ctx context.Context, organizationID, projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return ErrNotFound
	}
	if _, exists := m.projects[projectID]; !exists {
		return ErrNotFound
	}
	if current, attached := m.enterprise.projectOrganizations[projectID]; attached {
		if current == organizationID {
			sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organizationID)
			if err != nil {
				return err
			}
			m.commitContextAuditLocked(sealedAudit, hasAudit)
			return nil
		}
		return ErrConflict
	}
	sealedAudit, hasAudit, err := m.sealContextAuditLocked(ctx, organizationID)
	if err != nil {
		return err
	}
	m.enterprise.projectOrganizations[projectID] = organizationID
	m.commitContextAuditLocked(sealedAudit, hasAudit)
	return nil
}

func (m *Memory) ListOrganizationProjects(_ context.Context, organizationID string) ([]domain.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := []domain.Project{}
	for projectID, attachedOrganizationID := range m.enterprise.projectOrganizations {
		if attachedOrganizationID == organizationID {
			result = append(result, clone(m.projects[projectID]))
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result, nil
}

func (m *Memory) sealContextAuditLocked(
	ctx context.Context,
	organizationID string,
) (enterprise.AuditEvent, bool, error) {
	draft, exists := enterpriseAuditFromContext(ctx)
	if !exists {
		return enterprise.AuditEvent{}, false, nil
	}
	if draft.OrganizationID != organizationID {
		return enterprise.AuditEvent{}, false, &enterprise.ValidationError{
			Field: "organizationId", Message: "audit event does not match the mutation tenant",
		}
	}
	if _, duplicate := m.enterprise.auditLocations[draft.ID]; duplicate {
		return enterprise.AuditEvent{}, false, ErrConflict
	}
	events := m.enterprise.auditEvents[organizationID]
	checkpoint := enterprise.AuditCheckpoint{OrganizationID: organizationID, LastHash: enterprise.GenesisAuditHash}
	if len(events) > 0 {
		last := events[len(events)-1]
		checkpoint.LastSequence = last.Sequence
		checkpoint.LastHash = last.Hash
	}
	sealed, _, err := enterprise.SealAuditEvent(checkpoint, draft)
	if err != nil {
		return enterprise.AuditEvent{}, false, err
	}
	return sealed, true, nil
}

func (m *Memory) commitContextAuditLocked(event enterprise.AuditEvent, exists bool) {
	if !exists {
		return
	}
	m.enterprise.auditEvents[event.OrganizationID] = append(
		m.enterprise.auditEvents[event.OrganizationID],
		cloneAuditEvent(event),
	)
	m.enterprise.auditLocations[event.ID] = event.OrganizationID
}

func (m *Memory) AppendAuditEvent(_ context.Context, event enterprise.AuditEvent) (enterprise.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEnterpriseStateLocked()
	if _, exists := m.enterprise.organizations[event.OrganizationID]; !exists {
		return enterprise.AuditEvent{}, ErrNotFound
	}
	if _, exists := m.enterprise.auditLocations[event.ID]; exists {
		return enterprise.AuditEvent{}, ErrConflict
	}
	events := m.enterprise.auditEvents[event.OrganizationID]
	checkpoint := enterprise.AuditCheckpoint{
		OrganizationID: event.OrganizationID,
		LastHash:       enterprise.GenesisAuditHash,
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		checkpoint.LastSequence = last.Sequence
		checkpoint.LastHash = last.Hash
	}
	sealed, _, err := enterprise.SealAuditEvent(checkpoint, event)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	m.enterprise.auditEvents[event.OrganizationID] = append(events, cloneAuditEvent(sealed))
	m.enterprise.auditLocations[sealed.ID] = sealed.OrganizationID
	return cloneAuditEvent(sealed), nil
}

func (m *Memory) ListAuditEvents(
	_ context.Context,
	organizationID string,
	afterSequence uint64,
	limit int,
) ([]enterprise.AuditEvent, error) {
	if limit < 1 || limit > MaxAuditListLimit {
		return nil, ErrInvalidAuditLimit
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]enterprise.AuditEvent, 0, min(limit, len(m.enterprise.auditEvents[organizationID])))
	for _, event := range m.enterprise.auditEvents[organizationID] {
		if event.Sequence <= afterSequence {
			continue
		}
		result = append(result, cloneAuditEvent(event))
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (m *Memory) GetAuditCheckpoint(_ context.Context, organizationID string) (enterprise.AuditCheckpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.enterprise.organizations[organizationID]; !exists {
		return enterprise.AuditCheckpoint{}, ErrNotFound
	}
	checkpoint := enterprise.AuditCheckpoint{OrganizationID: organizationID, LastHash: enterprise.GenesisAuditHash}
	events := m.enterprise.auditEvents[organizationID]
	if len(events) > 0 {
		checkpoint.LastSequence = events[len(events)-1].Sequence
		checkpoint.LastHash = events[len(events)-1].Hash
	}
	return checkpoint, nil
}

func cloneMembership(membership enterprise.OrganizationMembership) enterprise.OrganizationMembership {
	if membership.JoinedAt != nil {
		joinedAt := *membership.JoinedAt
		membership.JoinedAt = &joinedAt
	}
	return membership
}

func cloneSSOConnection(connection enterprise.SSOConnection) enterprise.SSOConnection {
	connection.Domains = append([]string(nil), connection.Domains...)
	return connection
}

func clonePolicyRule(rule enterprise.PolicyRule) enterprise.PolicyRule {
	rule.Actions = append([]string(nil), rule.Actions...)
	rule.Resources = append([]string(nil), rule.Resources...)
	rule.Conditions.Roles = append([]enterprise.OrganizationRole(nil), rule.Conditions.Roles...)
	return rule
}

func clonePluginRegistration(registration enterprise.PluginRegistration) enterprise.PluginRegistration {
	registration.Capabilities = append([]string(nil), registration.Capabilities...)
	return registration
}

func cloneAuditEvent(event enterprise.AuditEvent) enterprise.AuditEvent {
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
		return event
	}
	raw, err := json.Marshal(event.Metadata)
	if err != nil {
		event.Metadata = shallowCloneMetadata(event.Metadata)
		return event
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var metadata map[string]any
	if err := decoder.Decode(&metadata); err != nil || metadata == nil {
		event.Metadata = shallowCloneMetadata(event.Metadata)
		return event
	}
	event.Metadata = metadata
	return event
}

func shallowCloneMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata))
	for key, value := range metadata {
		result[key] = value
	}
	return result
}

var _ EnterpriseRepository = (*Memory)(nil)
