package store

import (
	"context"
	"errors"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
)

const MaxAuditListLimit = 1000

var (
	ErrInvalidAuditLimit     = errors.New("audit limit must be between 1 and 1000")
	ErrLastOrganizationOwner = errors.New("organization must retain at least one active owner")
)

type enterpriseAuditContextKey struct{}

// WithEnterpriseAudit attaches an unsealed audit draft to an enterprise
// mutation. Memory and PostgreSQL repositories seal and append it in the same
// critical section/transaction as the requested state change. If sealing or
// insertion fails, the mutation fails without becoming visible.
func WithEnterpriseAudit(ctx context.Context, event enterprise.AuditEvent) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, enterpriseAuditContextKey{}, cloneAuditEvent(event))
}

func enterpriseAuditFromContext(ctx context.Context) (enterprise.AuditEvent, bool) {
	if ctx == nil {
		return enterprise.AuditEvent{}, false
	}
	event, ok := ctx.Value(enterpriseAuditContextKey{}).(enterprise.AuditEvent)
	return cloneAuditEvent(event), ok
}

// EnterpriseRepository is deliberately separate from Repository so the
// simulation data plane can be used without enabling the enterprise control
// plane. Every resource lookup and mutation after organization discovery is
// scoped by organizationID.
type EnterpriseRepository interface {
	CreateOrganization(context.Context, enterprise.Organization) error
	CreateOrganizationWithOwner(context.Context, enterprise.Organization, enterprise.OrganizationMembership) error
	ListOrganizations(context.Context, string) ([]enterprise.Organization, error)
	GetOrganization(context.Context, string) (enterprise.Organization, error)

	GetMembership(context.Context, string, string) (enterprise.OrganizationMembership, error)
	ListMemberships(context.Context, string) ([]enterprise.OrganizationMembership, error)
	SetMembership(context.Context, enterprise.OrganizationMembership) error

	SaveSSOConnection(context.Context, enterprise.SSOConnection) error
	GetSSOConnection(context.Context, string, string) (enterprise.SSOConnection, error)
	ListSSOConnections(context.Context, string) ([]enterprise.SSOConnection, error)

	SavePolicyRule(context.Context, enterprise.PolicyRule) error
	GetPolicyRule(context.Context, string, string) (enterprise.PolicyRule, error)
	ListPolicyRules(context.Context, string) ([]enterprise.PolicyRule, error)
	DeletePolicyRule(context.Context, string, string) error

	SavePluginRegistration(context.Context, enterprise.PluginRegistration) error
	GetPluginRegistration(context.Context, string, string) (enterprise.PluginRegistration, error)
	ListPluginRegistrations(context.Context, string) ([]enterprise.PluginRegistration, error)
	SetPluginRegistrationStatus(context.Context, string, string, enterprise.PluginStatus, time.Time) error

	AttachProjectToOrganization(context.Context, string, string) error
	ListOrganizationProjects(context.Context, string) ([]domain.Project, error)

	AppendAuditEvent(context.Context, enterprise.AuditEvent) (enterprise.AuditEvent, error)
	ListAuditEvents(context.Context, string, uint64, int) ([]enterprise.AuditEvent, error)
	GetAuditCheckpoint(context.Context, string) (enterprise.AuditCheckpoint, error)
}
