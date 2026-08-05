package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
)

const (
	memoryOrganizationA = "11111111-1111-4111-8111-111111111111"
	memoryOrganizationB = "22222222-2222-4222-8222-222222222222"
	memoryOwnerA        = "33333333-3333-4333-8333-333333333333"
	memoryOwnerB        = "44444444-4444-4444-8444-444444444444"
	memoryMember        = "55555555-5555-4555-8555-555555555555"
)

var memoryEnterpriseTime = time.Date(2026, time.August, 4, 15, 0, 0, 0, time.UTC)

func TestMemoryEnterpriseRepositoryIsTenantScopedAndDefensive(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	for _, userID := range []string{memoryOwnerA, memoryOwnerB, memoryMember} {
		if err := repository.CreateUser(ctx, domain.User{
			ID: userID, Email: userID + "@example.com", DisplayName: userID, PasswordHash: "test", CreatedAt: memoryEnterpriseTime,
		}); err != nil {
			t.Fatal(err)
		}
	}
	organizationA := memoryOrganization(memoryOrganizationA, "alpha-enterprise", "Alpha Enterprise")
	organizationB := memoryOrganization(memoryOrganizationB, "beta-enterprise", "Beta Enterprise")
	if err := repository.CreateOrganizationWithOwner(ctx, organizationA, memoryOwnerMembership(memoryOrganizationA, memoryOwnerA)); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateOrganizationWithOwner(ctx, organizationB, memoryOwnerMembership(memoryOrganizationB, memoryOwnerB)); err != nil {
		t.Fatal(err)
	}
	organizations, err := repository.ListOrganizations(ctx, memoryOwnerA)
	if err != nil || len(organizations) != 1 || organizations[0].ID != memoryOrganizationA {
		t.Fatalf("owner organizations=%+v err=%v", organizations, err)
	}
	joinedAt := memoryEnterpriseTime.Add(time.Hour)
	membership := enterprise.OrganizationMembership{
		OrganizationID: memoryOrganizationA, UserID: memoryMember,
		Role: enterprise.OrganizationAuditor, Status: enterprise.MembershipActive,
		CreatedAt: memoryEnterpriseTime, JoinedAt: &joinedAt,
	}
	if err := repository.SetMembership(ctx, membership); err != nil {
		t.Fatal(err)
	}
	storedMembership, err := repository.GetMembership(ctx, memoryOrganizationA, memoryMember)
	if err != nil || storedMembership.JoinedAt == nil {
		t.Fatalf("membership=%+v err=%v", storedMembership, err)
	}
	*storedMembership.JoinedAt = storedMembership.JoinedAt.Add(time.Hour)
	storedAgain, _ := repository.GetMembership(ctx, memoryOrganizationA, memoryMember)
	if !storedAgain.JoinedAt.Equal(joinedAt) {
		t.Fatal("membership returned an aliased JoinedAt")
	}
	if _, err := repository.GetMembership(ctx, memoryOrganizationB, memoryMember); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant membership err=%v", err)
	}

	connection := memorySSO(memoryOrganizationA, "66666666-6666-4666-8666-666666666666", "Corporate OIDC")
	if err := repository.SaveSSOConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	connectionB := connection
	connectionB.OrganizationID = memoryOrganizationB
	if err := repository.SaveSSOConnection(ctx, connectionB); !errors.Is(err, ErrConflict) {
		t.Fatalf("SSO ID moved tenants: %v", err)
	}
	if _, err := repository.GetSSOConnection(ctx, memoryOrganizationB, connection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant SSO err=%v", err)
	}
	returnedConnection, _ := repository.GetSSOConnection(ctx, memoryOrganizationA, connection.ID)
	returnedConnection.Domains[0] = "mutated.example.com"
	returnedConnection, _ = repository.GetSSOConnection(ctx, memoryOrganizationA, connection.ID)
	if returnedConnection.Domains[0] != "example.com" {
		t.Fatal("SSO domains were aliased")
	}

	rule := memoryPolicy(memoryOrganizationA, "77777777-7777-4777-8777-777777777777")
	if err := repository.SavePolicyRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	ruleB := rule
	ruleB.OrganizationID = memoryOrganizationB
	if err := repository.SavePolicyRule(ctx, ruleB); !errors.Is(err, ErrConflict) {
		t.Fatalf("policy ID moved tenants: %v", err)
	}
	returnedRule, _ := repository.GetPolicyRule(ctx, memoryOrganizationA, rule.ID)
	returnedRule.Actions[0] = "mutated"
	returnedRule, _ = repository.GetPolicyRule(ctx, memoryOrganizationA, rule.ID)
	if returnedRule.Actions[0] != "flows.read" {
		t.Fatal("policy actions were aliased")
	}
	if _, err := repository.GetPolicyRule(ctx, memoryOrganizationB, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant policy err=%v", err)
	}

	plugin := memoryPlugin(memoryOrganizationA, "88888888-8888-4888-8888-888888888888")
	if err := repository.SavePluginRegistration(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	pluginB := plugin
	pluginB.OrganizationID = memoryOrganizationB
	if err := repository.SavePluginRegistration(ctx, pluginB); !errors.Is(err, ErrConflict) {
		t.Fatalf("plugin ID moved tenants: %v", err)
	}
	returnedPlugin, _ := repository.GetPluginRegistration(ctx, memoryOrganizationA, plugin.ID)
	returnedPlugin.Capabilities[0] = "mutated"
	returnedPlugin, _ = repository.GetPluginRegistration(ctx, memoryOrganizationA, plugin.ID)
	if returnedPlugin.Capabilities[0] != "flows.read" {
		t.Fatal("plugin capabilities were aliased")
	}
	if err := repository.SetPluginRegistrationStatus(
		ctx, memoryOrganizationA, plugin.ID, enterprise.PluginRevoked, memoryEnterpriseTime.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	plugin.UpdatedAt = memoryEnterpriseTime.Add(2 * time.Minute)
	if err := repository.SavePluginRegistration(ctx, plugin); !errors.Is(err, enterprise.ErrPluginRevoked) {
		t.Fatalf("revoked plugin update err=%v", err)
	}
	if err := repository.SetPluginRegistrationStatus(
		ctx, memoryOrganizationA, plugin.ID, enterprise.PluginActive, memoryEnterpriseTime.Add(3*time.Minute),
	); !errors.Is(err, enterprise.ErrPluginRevoked) {
		t.Fatalf("revoked plugin status err=%v", err)
	}

	project := domain.Project{
		ID: "99999999-9999-4999-8999-999999999999", Name: "Enterprise project", OwnerID: memoryOwnerA,
		CreatedAt: memoryEnterpriseTime, UpdatedAt: memoryEnterpriseTime,
	}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := repository.AttachProjectToOrganization(ctx, memoryOrganizationA, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.AttachProjectToOrganization(ctx, memoryOrganizationB, project.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("project moved tenants: %v", err)
	}
	projectsA, _ := repository.ListOrganizationProjects(ctx, memoryOrganizationA)
	projectsB, _ := repository.ListOrganizationProjects(ctx, memoryOrganizationB)
	if len(projectsA) != 1 || len(projectsB) != 0 {
		t.Fatalf("tenant projects A=%+v B=%+v", projectsA, projectsB)
	}
}

func TestMemoryAuditAppendIsConcurrentContiguousAndCloned(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	organization := memoryOrganization(memoryOrganizationA, "audit-enterprise", "Audit Enterprise")
	if err := repository.CreateOrganization(ctx, organization); err != nil {
		t.Fatal(err)
	}

	const workers = 48
	start := make(chan struct{})
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := repository.AppendAuditEvent(ctx, enterprise.AuditEvent{
				ID: uuid.NewString(), OrganizationID: memoryOrganizationA,
				Action: "policy.updated", ResourceType: "policy", ResourceID: fmt.Sprintf("rule-%02d", index),
				Outcome: enterprise.AuditSucceeded, Metadata: map[string]any{"worker": index},
				OccurredAt: memoryEnterpriseTime.Add(time.Duration(index) * time.Microsecond),
			})
			errorsChannel <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	events, err := repository.ListAuditEvents(ctx, memoryOrganizationA, 0, workers)
	if err != nil || len(events) != workers {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("sequence[%d]=%d", index, event.Sequence)
		}
	}
	if err := enterprise.VerifyAuditChain(events); err != nil {
		t.Fatalf("invalid chain: %v", err)
	}
	checkpoint, err := repository.GetAuditCheckpoint(ctx, memoryOrganizationA)
	if err != nil || checkpoint.LastSequence != workers || checkpoint.LastHash != events[len(events)-1].Hash {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	if err := enterprise.VerifyAuditChainAgainst(events, checkpoint); err != nil {
		t.Fatalf("chain does not reach checkpoint: %v", err)
	}
	events[0].Metadata["worker"] = "mutated"
	storedAgain, _ := repository.ListAuditEvents(ctx, memoryOrganizationA, 0, 1)
	if storedAgain[0].Metadata["worker"] == "mutated" {
		t.Fatal("audit metadata was aliased")
	}
	page, err := repository.ListAuditEvents(ctx, memoryOrganizationA, 10, 5)
	if err != nil || len(page) != 5 || page[0].Sequence != 11 || page[4].Sequence != 15 {
		t.Fatalf("audit page=%+v err=%v", page, err)
	}
	if _, err := repository.ListAuditEvents(ctx, memoryOrganizationA, 0, 0); !errors.Is(err, ErrInvalidAuditLimit) {
		t.Fatalf("invalid limit err=%v", err)
	}
}

func TestMemoryEnterpriseMutationAndAuditAreAtomic(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	if err := repository.CreateUser(ctx, domain.User{
		ID: memoryOwnerA, Email: "atomic@example.com", DisplayName: "Atomic Owner", PasswordHash: "test", CreatedAt: memoryEnterpriseTime,
	}); err != nil {
		t.Fatal(err)
	}
	organization := memoryOrganization(memoryOrganizationA, "atomic-enterprise", "Atomic Enterprise")
	owner := memoryOwnerMembership(memoryOrganizationA, memoryOwnerA)
	createAudit := memoryAudit(memoryOrganizationA, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "organization.created", organization.ID)
	if err := repository.CreateOrganizationWithOwner(WithEnterpriseAudit(ctx, createAudit), organization, owner); err != nil {
		t.Fatal(err)
	}
	events, err := repository.ListAuditEvents(ctx, memoryOrganizationA, 0, 10)
	if err != nil || len(events) != 1 || events[0].Action != "organization.created" {
		t.Fatalf("create audit=%+v err=%v", events, err)
	}

	rule := memoryPolicy(memoryOrganizationA, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	policyAudit := memoryAudit(memoryOrganizationA, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "policy.saved", rule.ID)
	if err := repository.SavePolicyRule(WithEnterpriseAudit(ctx, policyAudit), rule); err != nil {
		t.Fatal(err)
	}

	connection := memorySSO(memoryOrganizationA, "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "Must roll back")
	invalidAudit := memoryAudit(memoryOrganizationA, "not-a-uuid", "sso.saved", connection.ID)
	if err := repository.SaveSSOConnection(WithEnterpriseAudit(ctx, invalidAudit), connection); err == nil {
		t.Fatal("invalid audit unexpectedly allowed the SSO mutation")
	}
	if _, err := repository.GetSSOConnection(ctx, memoryOrganizationA, connection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SSO survived failed audit: %v", err)
	}

	duplicateAudit := memoryAudit(memoryOrganizationA, policyAudit.ID, "sso.saved", connection.ID)
	if err := repository.SaveSSOConnection(WithEnterpriseAudit(ctx, duplicateAudit), connection); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate audit error=%v", err)
	}
	if _, err := repository.GetSSOConnection(ctx, memoryOrganizationA, connection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SSO survived duplicate audit: %v", err)
	}

	events, _ = repository.ListAuditEvents(ctx, memoryOrganizationA, 0, 10)
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("atomic audit chain=%+v", events)
	}
	if err := enterprise.VerifyAuditChain(events); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryMembershipConcurrentDemotionPreservesOwner(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	for _, userID := range []string{memoryOwnerA, memoryOwnerB} {
		if err := repository.CreateUser(ctx, domain.User{
			ID: userID, Email: userID + "@owners.test", DisplayName: userID, PasswordHash: "test", CreatedAt: memoryEnterpriseTime,
		}); err != nil {
			t.Fatal(err)
		}
	}
	organization := memoryOrganization(memoryOrganizationA, "owner-lock", "Owner Lock")
	if err := repository.CreateOrganizationWithOwner(ctx, organization, memoryOwnerMembership(memoryOrganizationA, memoryOwnerA)); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetMembership(ctx, memoryOwnerMembership(memoryOrganizationA, memoryOwnerB)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for index, userID := range []string{memoryOwnerA, memoryOwnerB} {
		go func(index int, userID string) {
			<-start
			membership := memoryOwnerMembership(memoryOrganizationA, userID)
			membership.Role = enterprise.OrganizationMember
			audit := memoryAudit(memoryOrganizationA, uuid.NewString(), "membership.demoted", userID)
			results <- repository.SetMembership(WithEnterpriseAudit(ctx, audit), membership)
		}(index, userID)
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
	memberships, err := repository.ListMemberships(ctx, memoryOrganizationA)
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
		t.Fatalf("active owners=%d memberships=%+v", owners, memberships)
	}
	events, _ := repository.ListAuditEvents(ctx, memoryOrganizationA, 0, 10)
	if len(events) != 1 {
		t.Fatalf("successful demotions audited=%d, want 1", len(events))
	}
}

func memoryOrganization(id, slug, name string) enterprise.Organization {
	return enterprise.Organization{
		ID: id, Slug: slug, Name: name, Status: enterprise.OrganizationActive,
		CreatedAt: memoryEnterpriseTime, UpdatedAt: memoryEnterpriseTime,
	}
}

func memoryOwnerMembership(organizationID, userID string) enterprise.OrganizationMembership {
	joinedAt := memoryEnterpriseTime
	return enterprise.OrganizationMembership{
		OrganizationID: organizationID, UserID: userID,
		Role: enterprise.OrganizationOwner, Status: enterprise.MembershipActive,
		CreatedAt: memoryEnterpriseTime, JoinedAt: &joinedAt,
	}
}

func memorySSO(organizationID, id, name string) enterprise.SSOConnection {
	return enterprise.SSOConnection{
		ID: id, OrganizationID: organizationID, Name: name, Protocol: enterprise.SSOProtocolOIDC,
		IssuerURL: "https://identity.example.com", Domains: []string{"example.com"}, Enabled: true,
		CreatedAt: memoryEnterpriseTime, UpdatedAt: memoryEnterpriseTime,
	}
}

func memoryPolicy(organizationID, id string) enterprise.PolicyRule {
	return enterprise.PolicyRule{
		ID: id, OrganizationID: organizationID, Description: "Read flows", Effect: enterprise.PolicyAllow,
		Actions: []string{"flows.read"}, Resources: []string{"flows/*"},
		Conditions: enterprise.PolicyConditions{Roles: []enterprise.OrganizationRole{enterprise.OrganizationAuditor}},
		CreatedAt:  memoryEnterpriseTime, UpdatedAt: memoryEnterpriseTime,
	}.Normalize()
}

func memoryPlugin(organizationID, id string) enterprise.PluginRegistration {
	return enterprise.PluginRegistration{
		ID: id, OrganizationID: organizationID, PluginKey: "flowverse.audit-export", Version: "1.0.0",
		Status: enterprise.PluginDisabled, SourceURL: "https://plugins.example.com/audit-export.tgz",
		Checksum: "sha256:" + strings.Repeat("a", 64), Capabilities: []string{"flows.read"},
		CreatedAt: memoryEnterpriseTime, UpdatedAt: memoryEnterpriseTime,
	}.Normalize()
}

func memoryAudit(organizationID, id, action, resourceID string) enterprise.AuditEvent {
	return enterprise.AuditEvent{
		ID: id, OrganizationID: organizationID, Action: action,
		ResourceType: "enterprise", ResourceID: resourceID,
		Outcome: enterprise.AuditSucceeded, Metadata: map[string]any{}, OccurredAt: memoryEnterpriseTime,
	}
}
