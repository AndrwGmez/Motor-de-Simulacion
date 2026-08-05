package enterprise

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSafeGlobMatchUsesBoundedSegmentSemantics(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{pattern: "flows.read", value: "flows.read", want: true},
		{pattern: "flows.*", value: "flows.read", want: true},
		{pattern: "flows.*", value: "flows.read.detail", want: true},
		{pattern: "project:*", value: "project:123", want: true},
		{pattern: "project:*", value: "project:123/node:456", want: false},
		{pattern: "project:**", value: "project:123/node:456", want: true},
		{pattern: "*", value: "project:123", want: false},
		{pattern: "**", value: "project:123/node:456", want: true},
		{pattern: "node:*-prod", value: "node:orders-prod", want: true},
		{pattern: "node:*-prod", value: "node:orders-dev", want: false},
		{pattern: "node:*", value: "node:", want: true},
		{pattern: "node:*", value: "edge:node", want: false},
	}
	for _, test := range tests {
		t.Run(test.pattern+"_"+test.value, func(t *testing.T) {
			if got := safeGlobMatch(test.pattern, test.value); got != test.want {
				t.Fatalf("safeGlobMatch(%q, %q) = %v, want %v", test.pattern, test.value, got, test.want)
			}
		})
	}
}

func TestPolicyEngineDenyPrecedenceTenantIsolationAndRoles(t *testing.T) {
	rules := []PolicyRule{
		policyRule("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3", testOrganizationID, PolicyDeny,
			[]string{"flows.read"}, []string{"project:blocked"}, []OrganizationRole{OrganizationMember}),
		policyRule("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1", testOrganizationID, PolicyAllow,
			[]string{"flows.*"}, []string{"project:*"}, []OrganizationRole{OrganizationMember}),
		policyRule("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa4", testOrganizationID, PolicyDeny,
			[]string{"flows.read"}, []string{"project:*"}, []OrganizationRole{OrganizationMember}),
		policyRule("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2", testOtherTenantID, PolicyAllow,
			[]string{"**"}, []string{"**"}, nil),
	}
	rules[2].Disabled = true
	engine, err := NewPolicyEngine(rules)
	if err != nil {
		t.Fatal(err)
	}

	allowed, err := engine.Evaluate(policyRequest(testOrganizationID, OrganizationMember, "flows.update", "project:open"))
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed || allowed.Effect != PolicyAllow || allowed.Reason != DecisionExplicitAllow {
		t.Fatalf("allow decision = %#v", allowed)
	}
	if want := []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"}; !reflect.DeepEqual(allowed.MatchedRuleIDs, want) {
		t.Fatalf("allow matches = %#v, want %#v", allowed.MatchedRuleIDs, want)
	}

	denied, err := engine.Evaluate(policyRequest(testOrganizationID, OrganizationMember, "flows.read", "project:blocked"))
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.Effect != PolicyDeny || denied.Reason != DecisionExplicitDeny {
		t.Fatalf("deny decision = %#v", denied)
	}
	wantMatches := []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3",
	}
	if !reflect.DeepEqual(denied.MatchedRuleIDs, wantMatches) {
		t.Fatalf("deny matches = %#v, want %#v", denied.MatchedRuleIDs, wantMatches)
	}

	for _, request := range []PolicyRequest{
		policyRequest(testOrganizationID, OrganizationAuditor, "flows.read", "project:open"),
		policyRequest(testOrganizationID, OrganizationMember, "plugins.install", "project:open"),
	} {
		decision, evaluateErr := engine.Evaluate(request)
		if evaluateErr != nil {
			t.Fatal(evaluateErr)
		}
		if decision.Allowed || decision.Reason != DecisionNoMatch || len(decision.MatchedRuleIDs) != 0 {
			t.Fatalf("default-deny decision = %#v", decision)
		}
	}

	otherTenant, err := engine.Evaluate(policyRequest(testOtherTenantID, OrganizationAuditor, "anything.read", "any:resource/path"))
	if err != nil {
		t.Fatal(err)
	}
	if !otherTenant.Allowed || otherTenant.MatchedRuleIDs[0] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2" {
		t.Fatalf("other-tenant decision = %#v", otherTenant)
	}
}

func TestPolicyEngineIsDeterministicAndDefensivelyCopiesRules(t *testing.T) {
	rules := []PolicyRule{
		policyRule("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2", testOrganizationID, PolicyAllow,
			[]string{"flows.read", "flows.*"}, []string{"project:*"}, nil),
		policyRule("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1", testOrganizationID, PolicyDeny,
			[]string{"flows.delete"}, []string{"project:*"}, []OrganizationRole{OrganizationMember}),
	}
	reversed := []PolicyRule{rules[1], rules[0]}
	first, err := NewPolicyEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPolicyEngine(reversed)
	if err != nil {
		t.Fatal(err)
	}
	request := policyRequest(testOrganizationID, OrganizationMember, "flows.delete", "project:one")
	firstDecision, _ := first.Evaluate(request)
	secondDecision, _ := second.Evaluate(request)
	if !reflect.DeepEqual(firstDecision, secondDecision) || !reflect.DeepEqual(first.Rules(), second.Rules()) {
		t.Fatalf("order changed result\nfirst:  %#v\nsecond: %#v", firstDecision, secondDecision)
	}

	rules[0].Actions[0] = "plugins.install"
	exposed := first.Rules()
	exposed[0].Actions[0] = "plugins.install"
	again, _ := first.Evaluate(request)
	if !reflect.DeepEqual(again, firstDecision) {
		t.Fatalf("external mutation changed immutable engine: %#v -> %#v", firstDecision, again)
	}
}

func TestPolicyEngineRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	valid := policyRule("cccccccc-cccc-4ccc-8ccc-ccccccccccc1", testOrganizationID, PolicyAllow,
		[]string{"flows.*"}, []string{"project:*"}, nil)
	tests := []struct {
		name   string
		mutate func(*PolicyRule)
		field  string
	}{
		{name: "regular expression syntax", mutate: func(rule *PolicyRule) { rule.Actions = []string{"flows.[a-z]+"} }, field: "actions[0]"},
		{name: "triple wildcard", mutate: func(rule *PolicyRule) { rule.Resources = []string{"project:***"} }, field: "resources[0]"},
		{name: "too many wildcards", mutate: func(rule *PolicyRule) { rule.Actions = []string{strings.Repeat("*a", maxWildcardTokens+1)} }, field: "actions[0]"},
		{name: "traversal", mutate: func(rule *PolicyRule) { rule.Resources = []string{"project:*/../secret"} }, field: "resources[0]"},
		{name: "too long", mutate: func(rule *PolicyRule) { rule.Actions = []string{strings.Repeat("a", maxPatternLength+1)} }, field: "actions[0]"},
		{name: "unknown effect", mutate: func(rule *PolicyRule) { rule.Effect = "audit" }, field: "effect"},
		{name: "unknown role", mutate: func(rule *PolicyRule) { rule.Conditions.Roles = []OrganizationRole{"root"} }, field: "conditions.roles[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			_, err := NewPolicyEngine([]PolicyRule{candidate})
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %T %v, want wrapped validation error", err, err)
			}
			if validation.Field != test.field {
				t.Fatalf("field = %q, want %q: %v", validation.Field, test.field, err)
			}
		})
	}

	_, err := NewPolicyEngine([]PolicyRule{valid, valid})
	assertValidationField(t, err, "id")

	engine, err := NewPolicyEngine([]PolicyRule{valid})
	if err != nil {
		t.Fatal(err)
	}
	badRequests := []struct {
		name    string
		request PolicyRequest
		field   string
	}{
		{name: "wildcard action", request: policyRequest(testOrganizationID, OrganizationMember, "flows.*", "project:one"), field: "action"},
		{name: "traversal resource", request: policyRequest(testOrganizationID, OrganizationMember, "flows.read", "project:one/../secret"), field: "resource"},
		{name: "wrong tenant format", request: policyRequest("tenant", OrganizationMember, "flows.read", "project:one"), field: "organizationId"},
		{name: "unknown role", request: policyRequest(testOrganizationID, "root", "flows.read", "project:one"), field: "role"},
	}
	for _, test := range badRequests {
		t.Run(test.name, func(t *testing.T) {
			_, evaluateErr := engine.Evaluate(test.request)
			assertValidationField(t, evaluateErr, test.field)
		})
	}
}

func policyRule(id, organizationID string, effect PolicyEffect, actions, resources []string, roles []OrganizationRole) PolicyRule {
	return PolicyRule{
		ID: id, OrganizationID: organizationID, Effect: effect,
		Actions: actions, Resources: resources, Conditions: PolicyConditions{Roles: roles},
		CreatedAt: testTimestamp, UpdatedAt: testTimestamp,
	}
}

func policyRequest(organizationID string, role OrganizationRole, action, resource string) PolicyRequest {
	return PolicyRequest{
		OrganizationID: organizationID, SubjectID: testUserID, Role: role,
		Action: action, Resource: resource,
	}
}
