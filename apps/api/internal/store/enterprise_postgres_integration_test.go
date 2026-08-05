package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
)

func TestPostgresEnterpriseRepositoryIntegration(t *testing.T) {
	repository, ctx := openEnterprisePostgres(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerA := createEnterpriseUser(t, ctx, repository, now)
	ownerB := createEnterpriseUser(t, ctx, repository, now)
	member := createEnterpriseUser(t, ctx, repository, now)
	organizationA := postgresOrganization(now, "alpha")
	organizationB := postgresOrganization(now, "beta")
	createAudit := postgresAudit(organizationA.ID, "organization.created", organizationA.ID, now)
	if err := repository.CreateOrganizationWithOwner(
		WithEnterpriseAudit(ctx, createAudit), organizationA, postgresOwner(organizationA.ID, ownerA.ID, now),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateOrganizationWithOwner(
		ctx, organizationB, postgresOwner(organizationB.ID, ownerB.ID, now),
	); err != nil {
		t.Fatal(err)
	}
	organizations, err := repository.ListOrganizations(ctx, ownerA.ID)
	if err != nil || len(organizations) != 1 || organizations[0].ID != organizationA.ID {
		t.Fatalf("organizations=%+v err=%v", organizations, err)
	}
	if storedOrganization, err := repository.GetOrganization(ctx, organizationA.ID); err != nil || storedOrganization.Slug != organizationA.Slug {
		t.Fatalf("organization=%+v err=%v", storedOrganization, err)
	}

	joinedAt := now.Add(time.Minute)
	membership := enterprise.OrganizationMembership{
		OrganizationID: organizationA.ID, UserID: member.ID,
		Role: enterprise.OrganizationAuditor, Status: enterprise.MembershipActive,
		CreatedAt: now, JoinedAt: &joinedAt,
	}
	if err := repository.SetMembership(ctx, membership); err != nil {
		t.Fatal(err)
	}
	if memberships, err := repository.ListMemberships(ctx, organizationA.ID); err != nil || len(memberships) != 2 {
		t.Fatalf("memberships=%+v err=%v", memberships, err)
	}
	if _, err := repository.GetMembership(ctx, organizationB.ID, member.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant membership err=%v", err)
	}

	connection := postgresSSO(organizationA.ID, now, "Corporate OIDC")
	ssoAudit := postgresAudit(organizationA.ID, "sso.saved", connection.ID, now.Add(time.Microsecond))
	if err := repository.SaveSSOConnection(WithEnterpriseAudit(ctx, ssoAudit), connection); err != nil {
		t.Fatal(err)
	}
	crossTenantConnection := connection
	crossTenantConnection.OrganizationID = organizationB.ID
	if err := repository.SaveSSOConnection(ctx, crossTenantConnection); !errors.Is(err, ErrConflict) {
		t.Fatalf("SSO moved tenants: %v", err)
	}
	if _, err := repository.GetSSOConnection(ctx, organizationB.ID, connection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant SSO err=%v", err)
	}
	if connections, err := repository.ListSSOConnections(ctx, organizationA.ID); err != nil || len(connections) != 1 || connections[0].ID != connection.ID {
		t.Fatalf("SSO list=%+v err=%v", connections, err)
	}

	rule := postgresPolicy(organizationA.ID, now)
	policyAudit := postgresAudit(organizationA.ID, "policy.saved", rule.ID, now.Add(2*time.Microsecond))
	if err := repository.SavePolicyRule(WithEnterpriseAudit(ctx, policyAudit), rule); err != nil {
		t.Fatal(err)
	}
	crossTenantRule := rule
	crossTenantRule.OrganizationID = organizationB.ID
	if err := repository.SavePolicyRule(ctx, crossTenantRule); !errors.Is(err, ErrConflict) {
		t.Fatalf("policy moved tenants: %v", err)
	}
	if err := repository.DeletePolicyRule(ctx, organizationB.ID, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant policy delete err=%v", err)
	}
	if rules, err := repository.ListPolicyRules(ctx, organizationA.ID); err != nil || len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("policy list=%+v err=%v", rules, err)
	}
	deletePolicyAudit := postgresAudit(organizationA.ID, "policy.deleted", rule.ID, now.Add(3*time.Microsecond))
	if err := repository.DeletePolicyRule(WithEnterpriseAudit(ctx, deletePolicyAudit), organizationA.ID, rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetPolicyRule(ctx, organizationA.ID, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted policy err=%v", err)
	}

	plugin := postgresPlugin(organizationA.ID, now)
	if err := repository.SavePluginRegistration(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	if plugins, err := repository.ListPluginRegistrations(ctx, organizationA.ID); err != nil || len(plugins) != 1 || plugins[0].ID != plugin.ID {
		t.Fatalf("plugin list=%+v err=%v", plugins, err)
	}
	if _, err := repository.GetPluginRegistration(ctx, organizationB.ID, plugin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant plugin err=%v", err)
	}
	if err := repository.SetPluginRegistrationStatus(
		ctx, organizationA.ID, plugin.ID, enterprise.PluginRevoked, now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	plugin.UpdatedAt = now.Add(2 * time.Minute)
	if err := repository.SavePluginRegistration(ctx, plugin); !errors.Is(err, enterprise.ErrPluginRevoked) {
		t.Fatalf("revoked plugin save err=%v", err)
	}
	if err := repository.SetPluginRegistrationStatus(
		ctx, organizationA.ID, plugin.ID, enterprise.PluginActive, now.Add(3*time.Minute),
	); !errors.Is(err, enterprise.ErrPluginRevoked) {
		t.Fatalf("revoked plugin status err=%v", err)
	}

	project := domain.Project{
		ID: uuid.NewString(), Name: "Tenant project", OwnerID: ownerA.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	attachAudit := postgresAudit(organizationA.ID, "project.attached", project.ID, now.Add(4*time.Microsecond))
	if err := repository.AttachProjectToOrganization(WithEnterpriseAudit(ctx, attachAudit), organizationA.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.AttachProjectToOrganization(ctx, organizationB.ID, project.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("project moved tenants: %v", err)
	}
	projectsA, err := repository.ListOrganizationProjects(ctx, organizationA.ID)
	if err != nil || len(projectsA) != 1 || projectsA[0].ID != project.ID {
		t.Fatalf("organization projects=%+v err=%v", projectsA, err)
	}
	projectsB, err := repository.ListOrganizationProjects(ctx, organizationB.ID)
	if err != nil || len(projectsB) != 0 {
		t.Fatalf("other organization projects=%+v err=%v", projectsB, err)
	}

	rolledBackConnection := postgresSSO(organizationA.ID, now, "Rolled back OIDC")
	invalidAudit := postgresAudit(organizationA.ID, "sso.saved", rolledBackConnection.ID, now.Add(5*time.Microsecond))
	invalidAudit.ID = "invalid"
	if err := repository.SaveSSOConnection(WithEnterpriseAudit(ctx, invalidAudit), rolledBackConnection); err == nil {
		t.Fatal("invalid audit allowed SSO mutation")
	}
	if _, err := repository.GetSSOConnection(ctx, organizationA.ID, rolledBackConnection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SSO survived invalid audit err=%v", err)
	}
	duplicateAudit := postgresAudit(organizationA.ID, "sso.saved", rolledBackConnection.ID, now.Add(6*time.Microsecond))
	duplicateAudit.ID = policyAudit.ID
	if err := repository.SaveSSOConnection(WithEnterpriseAudit(ctx, duplicateAudit), rolledBackConnection); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate audit err=%v", err)
	}
	if _, err := repository.GetSSOConnection(ctx, organizationA.ID, rolledBackConnection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SSO survived duplicate audit err=%v", err)
	}

	events, err := repository.ListAuditEvents(ctx, organizationA.ID, 0, 20)
	if err != nil || len(events) != 5 {
		t.Fatalf("audit events=%+v err=%v", events, err)
	}
	if err := enterprise.VerifyAuditChain(events); err != nil {
		t.Fatalf("stored audit chain invalid: %v", err)
	}
	checkpoint, err := repository.GetAuditCheckpoint(ctx, organizationA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := enterprise.VerifyAuditChainAgainst(events, checkpoint); err != nil {
		t.Fatalf("audit checkpoint mismatch: %v", err)
	}
}

func TestPostgresAuditAppendIsContiguousUnderConcurrency(t *testing.T) {
	repository, ctx := openEnterprisePostgres(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	organization := postgresOrganization(now, "concurrent-audit")
	if err := repository.CreateOrganization(ctx, organization); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			event := postgresAudit(organization.ID, "policy.evaluated", fmt.Sprintf("rule-%02d", index), now.Add(time.Duration(index)*time.Microsecond))
			_, err := repository.AppendAuditEvent(ctx, event)
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent audit append: %v", err)
		}
	}
	events, err := repository.ListAuditEvents(ctx, organization.ID, 0, workers)
	if err != nil || len(events) != workers {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("sequence[%d]=%d", index, event.Sequence)
		}
	}
	if err := enterprise.VerifyAuditChain(events); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := repository.GetAuditCheckpoint(ctx, organization.ID)
	if err != nil || checkpoint.LastSequence != workers {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
}

func TestPostgresConcurrentMembershipDemotionPreservesOwner(t *testing.T) {
	repository, ctx := openEnterprisePostgres(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ownerA := createEnterpriseUser(t, ctx, repository, now)
	ownerB := createEnterpriseUser(t, ctx, repository, now)
	organization := postgresOrganization(now, "owner-lock")
	if err := repository.CreateOrganizationWithOwner(
		ctx, organization, postgresOwner(organization.ID, ownerA.ID, now),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetMembership(ctx, postgresOwner(organization.ID, ownerB.ID, now)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, ownerID := range []string{ownerA.ID, ownerB.ID} {
		go func(ownerID string) {
			<-start
			membership := postgresOwner(organization.ID, ownerID, now)
			membership.Role = enterprise.OrganizationMember
			audit := postgresAudit(organization.ID, "membership.demoted", ownerID, now.Add(time.Microsecond))
			results <- repository.SetMembership(WithEnterpriseAudit(ctx, audit), membership)
		}(ownerID)
	}
	close(start)
	successes, protected := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrLastOrganizationOwner):
			protected++
		default:
			t.Fatalf("unexpected demotion error: %v", err)
		}
	}
	if successes != 1 || protected != 1 {
		t.Fatalf("demotions successes=%d protected=%d", successes, protected)
	}
	memberships, err := repository.ListMemberships(ctx, organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, membership := range memberships {
		if membership.Role == enterprise.OrganizationOwner && membership.Status == enterprise.MembershipActive {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners=%d memberships=%+v", owners, memberships)
	}
	events, err := repository.ListAuditEvents(ctx, organization.ID, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("successful mutation audits=%+v err=%v", events, err)
	}
}

func openEnterprisePostgres(t *testing.T) (*Postgres, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	repository, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.Close)
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return repository, ctx
}

func createEnterpriseUser(t *testing.T, ctx context.Context, repository *Postgres, now time.Time) domain.User {
	t.Helper()
	user := domain.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@enterprise.test",
		DisplayName: "Enterprise Test", PasswordHash: "test", CreatedAt: now,
	}
	if err := repository.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	return user
}

func postgresOrganization(now time.Time, prefix string) enterprise.Organization {
	id := uuid.NewString()
	return enterprise.Organization{
		ID: id, Slug: prefix + "-" + strings.ReplaceAll(id[:12], "-", ""), Name: prefix,
		Status: enterprise.OrganizationActive, CreatedAt: now, UpdatedAt: now,
	}
}

func postgresOwner(organizationID, userID string, now time.Time) enterprise.OrganizationMembership {
	joinedAt := now
	return enterprise.OrganizationMembership{
		OrganizationID: organizationID, UserID: userID, Role: enterprise.OrganizationOwner,
		Status: enterprise.MembershipActive, CreatedAt: now, JoinedAt: &joinedAt,
	}
}

func postgresSSO(organizationID string, now time.Time, name string) enterprise.SSOConnection {
	return enterprise.SSOConnection{
		ID: uuid.NewString(), OrganizationID: organizationID, Name: name,
		Protocol: enterprise.SSOProtocolOIDC, IssuerURL: "https://identity.example.com",
		Domains: []string{"example.com"}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
}

func postgresPolicy(organizationID string, now time.Time) enterprise.PolicyRule {
	return enterprise.PolicyRule{
		ID: uuid.NewString(), OrganizationID: organizationID, Description: "Read flows",
		Effect: enterprise.PolicyAllow, Actions: []string{"flows.read"}, Resources: []string{"flows/*"},
		Conditions: enterprise.PolicyConditions{Roles: []enterprise.OrganizationRole{enterprise.OrganizationAuditor}},
		CreatedAt:  now, UpdatedAt: now,
	}.Normalize()
}

func postgresPlugin(organizationID string, now time.Time) enterprise.PluginRegistration {
	return enterprise.PluginRegistration{
		ID: uuid.NewString(), OrganizationID: organizationID, PluginKey: "flowverse.audit-export",
		Version: "1.0.0", Status: enterprise.PluginDisabled,
		SourceURL: "https://plugins.example.com/audit-export.tgz",
		Checksum:  "sha256:" + strings.Repeat("a", 64), Capabilities: []string{"flows.read"},
		CreatedAt: now, UpdatedAt: now,
	}.Normalize()
}

func postgresAudit(organizationID, action, resourceID string, occurredAt time.Time) enterprise.AuditEvent {
	return enterprise.AuditEvent{
		ID: uuid.NewString(), OrganizationID: organizationID, Action: action,
		ResourceType: "enterprise", ResourceID: resourceID, Outcome: enterprise.AuditSucceeded,
		Metadata: map[string]any{}, OccurredAt: occurredAt,
	}
}
