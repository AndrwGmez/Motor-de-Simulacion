package enterprise

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	testOrganizationID = "11111111-1111-4111-8111-111111111111"
	testOtherTenantID  = "22222222-2222-4222-8222-222222222222"
	testUserID         = "33333333-3333-4333-8333-333333333333"
	testOtherUserID    = "44444444-4444-4444-8444-444444444444"
)

var testTimestamp = time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)

func TestOrganizationValidation(t *testing.T) {
	valid := Organization{
		ID: testOrganizationID, Slug: "acme-platform", Name: "Acme Platform",
		Status: OrganizationActive, CreatedAt: testTimestamp, UpdatedAt: testTimestamp,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid organization: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Organization)
		field  string
	}{
		{name: "non UUID ID", mutate: func(value *Organization) { value.ID = "acme" }, field: "id"},
		{name: "uppercase slug", mutate: func(value *Organization) { value.Slug = "Acme" }, field: "slug"},
		{name: "leading hyphen", mutate: func(value *Organization) { value.Slug = "-acme" }, field: "slug"},
		{name: "oversized slug", mutate: func(value *Organization) { value.Slug = strings.Repeat("a", 64) }, field: "slug"},
		{name: "blank name", mutate: func(value *Organization) { value.Name = " " }, field: "name"},
		{name: "untrimmed name", mutate: func(value *Organization) { value.Name = " Acme" }, field: "name"},
		{name: "invalid status", mutate: func(value *Organization) { value.Status = "deleted" }, field: "status"},
		{name: "missing creation time", mutate: func(value *Organization) { value.CreatedAt = time.Time{} }, field: "createdAt"},
		{name: "backwards update", mutate: func(value *Organization) { value.UpdatedAt = value.CreatedAt.Add(-time.Second) }, field: "updatedAt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			assertValidationField(t, candidate.Validate(), test.field)
		})
	}
}

func TestOrganizationMembershipValidation(t *testing.T) {
	joined := testTimestamp.Add(time.Minute)
	valid := OrganizationMembership{
		OrganizationID: testOrganizationID, UserID: testUserID,
		Role: OrganizationMember, Status: MembershipActive,
		CreatedAt: testTimestamp, JoinedAt: &joined,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid membership: %v", err)
	}
	invited := valid
	invited.Role, invited.Status, invited.JoinedAt = OrganizationAuditor, MembershipInvited, nil
	if err := invited.Validate(); err != nil {
		t.Fatalf("valid invitation: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*OrganizationMembership)
		field  string
	}{
		{name: "invalid tenant", mutate: func(value *OrganizationMembership) { value.OrganizationID = "bad" }, field: "organizationId"},
		{name: "invalid user", mutate: func(value *OrganizationMembership) { value.UserID = "bad" }, field: "userId"},
		{name: "invalid role", mutate: func(value *OrganizationMembership) { value.Role = "superuser" }, field: "role"},
		{name: "invalid state", mutate: func(value *OrganizationMembership) { value.Status = "removed" }, field: "status"},
		{name: "active without joined time", mutate: func(value *OrganizationMembership) { value.JoinedAt = nil }, field: "joinedAt"},
		{name: "owner cannot be suspended", mutate: func(value *OrganizationMembership) { value.Role, value.Status = OrganizationOwner, MembershipSuspended }, field: "status"},
		{name: "invitation cannot be joined", mutate: func(value *OrganizationMembership) { value.Status = MembershipInvited }, field: "joinedAt"},
		{name: "joined before creation", mutate: func(value *OrganizationMembership) {
			early := value.CreatedAt.Add(-time.Second)
			value.JoinedAt = &early
		}, field: "joinedAt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			assertValidationField(t, candidate.Validate(), test.field)
		})
	}
}

func TestSSOConnectionMetadataOnlyValidation(t *testing.T) {
	validOIDC := SSOConnection{
		ID: "55555555-5555-4555-8555-555555555555", OrganizationID: testOrganizationID,
		Name: "Corporate identity", Protocol: SSOProtocolOIDC,
		IssuerURL: "https://identity.example.com", MetadataURL: "https://identity.example.com/.well-known/openid-configuration",
		Domains: []string{"SUB.EXAMPLE.COM", " example.com ", "example.com"}, Enabled: true,
		CreatedAt: testTimestamp, UpdatedAt: testTimestamp,
	}.Normalize()
	if err := validOIDC.Validate(); err != nil {
		t.Fatalf("valid OIDC metadata: %v", err)
	}
	if got, want := validOIDC.Domains, []string{"example.com", "sub.example.com"}; !equalStrings(got, want) {
		t.Fatalf("normalized domains = %#v, want %#v", got, want)
	}

	validSAML := SSOConnection{
		ID: "66666666-6666-4666-8666-666666666666", OrganizationID: testOrganizationID,
		Name: "SAML identity", Protocol: SSOProtocolSAML,
		MetadataURL: "https://idp.example.com/metadata", EntityID: "urn:example:idp",
		SignInURL: "https://idp.example.com/sso", CertificateFingerprint: "sha256:" + strings.Repeat("a", 64),
		Domains: []string{"example.com"}, CreatedAt: testTimestamp, UpdatedAt: testTimestamp,
	}.Normalize()
	if err := validSAML.Validate(); err != nil {
		t.Fatalf("valid SAML metadata: %v", err)
	}

	tests := []struct {
		name      string
		candidate SSOConnection
		field     string
	}{
		{name: "OIDC requires issuer", candidate: func() SSOConnection { value := validOIDC; value.IssuerURL = ""; return value }(), field: "issuerUrl"},
		{name: "OIDC rejects SAML fields", candidate: func() SSOConnection { value := validOIDC; value.EntityID = "urn:secret"; return value }(), field: "protocol"},
		{name: "issuer requires HTTPS", candidate: func() SSOConnection {
			value := validOIDC
			value.IssuerURL = "http://identity.example.com"
			return value
		}(), field: "issuerUrl"},
		{name: "issuer rejects credentials", candidate: func() SSOConnection {
			value := validOIDC
			value.IssuerURL = "https://user:password@identity.example.com"
			return value
		}(), field: "issuerUrl"},
		{name: "metadata rejects token query", candidate: func() SSOConnection { value := validOIDC; value.MetadataURL += "?token=secret"; return value }(), field: "metadataUrl"},
		{name: "domains must be canonical", candidate: func() SSOConnection { value := validOIDC; value.Domains = []string{"SUB.example.com"}; return value }(), field: "domains"},
		{name: "domains must be DNS names", candidate: func() SSOConnection { value := validOIDC; value.Domains = []string{"localhost"}; return value }(), field: "domains[0]"},
		{name: "SAML requires fingerprint", candidate: func() SSOConnection { value := validSAML; value.CertificateFingerprint = ""; return value }(), field: "certificateFingerprint"},
		{name: "SAML entity ID rejects query data", candidate: func() SSOConnection {
			value := validSAML
			value.EntityID = "https://idp.example.com/entity?token=secret"
			return value
		}(), field: "entityId"},
		{name: "SAML rejects OIDC issuer", candidate: func() SSOConnection { value := validSAML; value.IssuerURL = "https://issuer.example.com"; return value }(), field: "issuerUrl"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertValidationField(t, test.candidate.Validate(), test.field)
		})
	}

	raw, err := json.Marshal(validSAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"clientSecret", "privateKey", "accessToken", "certificatePem"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("metadata-only SSO model leaked %q: %s", forbidden, raw)
		}
	}
}

func assertValidationField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error for %s", field)
	}
	validation, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if validation.Field != field {
		t.Fatalf("validation field = %q, want %q: %v", validation.Field, field, err)
	}
}
