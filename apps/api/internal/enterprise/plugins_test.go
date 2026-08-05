package enterprise

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPluginRegistrationValidationAndNormalization(t *testing.T) {
	valid := validPluginRegistration("77777777-7777-4777-8777-777777777777", testOrganizationID, "com.acme.simulator")
	valid.PluginKey = " COM.Acme.Simulator "
	valid.Capabilities = []string{"FLOWS.READ", " runs.create ", "flows.read"}
	valid = valid.Normalize()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid plugin: %v", err)
	}
	if valid.PluginKey != "com.acme.simulator" || !reflect.DeepEqual(valid.Capabilities, []string{"flows.read", "runs.create"}) {
		t.Fatalf("normalization failed: %#v", valid)
	}

	tests := []struct {
		name   string
		mutate func(*PluginRegistration)
		field  string
	}{
		{name: "invalid tenant", mutate: func(value *PluginRegistration) { value.OrganizationID = "bad" }, field: "organizationId"},
		{name: "invalid installer", mutate: func(value *PluginRegistration) { value.InstalledBy = "bad" }, field: "installedBy"},
		{name: "invalid key", mutate: func(value *PluginRegistration) { value.PluginKey = "Acme Plugin" }, field: "pluginKey"},
		{name: "leading zero semver", mutate: func(value *PluginRegistration) { value.Version = "01.2.3" }, field: "version"},
		{name: "leading zero prerelease", mutate: func(value *PluginRegistration) { value.Version = "1.2.3-01" }, field: "version"},
		{name: "missing patch semver", mutate: func(value *PluginRegistration) { value.Version = "1.2" }, field: "version"},
		{name: "invalid state", mutate: func(value *PluginRegistration) { value.Status = "pending" }, field: "status"},
		{name: "insecure artifact URL", mutate: func(value *PluginRegistration) { value.SourceURL = "http://plugins.example.com/plugin.tgz" }, field: "sourceUrl"},
		{name: "artifact URL credentials", mutate: func(value *PluginRegistration) { value.SourceURL = "https://token@plugins.example.com/plugin.tgz" }, field: "sourceUrl"},
		{name: "artifact URL query", mutate: func(value *PluginRegistration) { value.SourceURL += "?signature=secret" }, field: "sourceUrl"},
		{name: "artifact URL without path", mutate: func(value *PluginRegistration) { value.SourceURL = "oci://registry.example.com" }, field: "sourceUrl"},
		{name: "bad digest", mutate: func(value *PluginRegistration) { value.Checksum = "sha256:1234" }, field: "checksum"},
		{name: "wildcard capability", mutate: func(value *PluginRegistration) { value.Capabilities = []string{"flows.*"} }, field: "capabilities[0]"},
		{name: "backwards timestamp", mutate: func(value *PluginRegistration) { value.UpdatedAt = value.CreatedAt.Add(-1) }, field: "updatedAt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			assertValidationField(t, candidate.Validate(), test.field)
		})
	}
}

func TestPluginRegistryIsTenantScopedOrderedAndDefensive(t *testing.T) {
	registry := NewPluginRegistry()
	registrations := []PluginRegistration{
		validPluginRegistration("88888888-8888-4888-8888-888888888883", testOrganizationID, "zeta.plugin"),
		validPluginRegistration("88888888-8888-4888-8888-888888888881", testOrganizationID, "alpha.plugin"),
		validPluginRegistration("88888888-8888-4888-8888-888888888882", testOtherTenantID, "alpha.plugin"),
	}
	for _, registration := range registrations {
		if err := registry.Register(registration); err != nil {
			t.Fatal(err)
		}
	}

	got := registry.List(testOrganizationID)
	if len(got) != 2 || got[0].PluginKey != "alpha.plugin" || got[1].PluginKey != "zeta.plugin" {
		t.Fatalf("tenant list is not deterministic: %#v", got)
	}
	if other := registry.List(testOtherTenantID); len(other) != 1 || other[0].ID != registrations[2].ID {
		t.Fatalf("other tenant list = %#v", other)
	}
	if _, exists := registry.Get(testOrganizationID, "missing.plugin"); exists {
		t.Fatal("missing plugin was returned")
	}
	stored, exists := registry.Get(testOrganizationID, " ALPHA.PLUGIN ")
	if !exists || stored.ID != registrations[1].ID {
		t.Fatalf("stored plugin = %#v, exists=%v", stored, exists)
	}

	registrations[1].Capabilities[0] = "tampered"
	stored.Capabilities[0] = "tampered"
	again, _ := registry.Get(testOrganizationID, "alpha.plugin")
	if again.Capabilities[0] != "flows.read" {
		t.Fatalf("registry storage was mutated externally: %#v", again)
	}
}

func TestPluginRegistryEnforcesIdentityAndTerminalRevocation(t *testing.T) {
	var registry PluginRegistry
	registration := validPluginRegistration("99999999-9999-4999-8999-999999999999", testOrganizationID, "acme.plugin")
	if err := registry.Register(registration); err != nil {
		t.Fatalf("zero-value registry failed: %v", err)
	}
	if err := registry.Register(registration); !errors.Is(err, ErrPluginAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}
	duplicateID := validPluginRegistration(registration.ID, testOtherTenantID, "other.plugin")
	if err := registry.Register(duplicateID); !errors.Is(err, ErrPluginAlreadyRegistered) {
		t.Fatalf("cross-tenant duplicate ID error = %v", err)
	}
	duplicateKey := validPluginRegistration("aaaaaaaa-9999-4999-8999-999999999999", testOrganizationID, registration.PluginKey)
	if err := registry.Register(duplicateKey); !errors.Is(err, ErrPluginAlreadyRegistered) {
		t.Fatalf("duplicate key error = %v", err)
	}

	disabledAt := registration.UpdatedAt.Add(1)
	if err := registry.SetStatus(testOrganizationID, registration.PluginKey, PluginDisabled, disabledAt); err != nil {
		t.Fatal(err)
	}
	revokedAt := disabledAt.Add(1)
	if err := registry.SetStatus(testOrganizationID, registration.PluginKey, PluginRevoked, revokedAt); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetStatus(testOrganizationID, registration.PluginKey, PluginActive, revokedAt.Add(1)); !errors.Is(err, ErrPluginRevoked) {
		t.Fatalf("revoked transition error = %v", err)
	}
	if err := registry.SetStatus(testOrganizationID, "missing.plugin", PluginActive, revokedAt); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("missing transition error = %v", err)
	}
	stored, _ := registry.Get(testOrganizationID, registration.PluginKey)
	if stored.Status != PluginRevoked || !stored.UpdatedAt.Equal(revokedAt.UTC()) {
		t.Fatalf("status was not persisted: %#v", stored)
	}
}

func validPluginRegistration(id, organizationID, key string) PluginRegistration {
	return PluginRegistration{
		ID: id, OrganizationID: organizationID, PluginKey: key,
		Version: "1.2.3-beta.1+build.7", Status: PluginActive,
		SourceURL:    "oci://registry.example.com/flowverse/" + strings.ReplaceAll(key, ".", "-"),
		Checksum:     "sha256:" + strings.Repeat("b", 64),
		Capabilities: []string{"flows.read"}, InstalledBy: testUserID,
		CreatedAt: testTimestamp, UpdatedAt: testTimestamp,
	}.Normalize()
}
