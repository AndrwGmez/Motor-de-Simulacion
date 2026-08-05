package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnterpriseFoundationMigrationIsIdempotentAndTenantScoped(t *testing.T) {
	raw, err := migrationFiles.ReadFile("migrations/003_enterprise_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, table := range []string{
		"organizations",
		"organization_members",
		"sso_connections",
		"policy_rules",
		"plugin_registrations",
		"audit_log",
	} {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("migration is missing idempotent %s table creation", table)
		}
	}
	for _, tenantTable := range []string{
		"organization_members",
		"sso_connections",
		"policy_rules",
		"plugin_registrations",
		"audit_log",
	} {
		start := strings.Index(sql, "CREATE TABLE IF NOT EXISTS "+tenantTable+" (")
		if start < 0 {
			continue
		}
		end := strings.Index(sql[start:], "\n);")
		if end < 0 || !strings.Contains(sql[start:start+end], "organization_id uuid NOT NULL") {
			t.Errorf("%s does not have a required tenant ID", tenantTable)
		}
	}
	for _, index := range []string{
		"organization_members_user_idx",
		"projects_organization_idx",
		"sso_connections_tenant_enabled_idx",
		"policy_rules_tenant_enabled_idx",
		"plugin_registrations_tenant_status_idx",
		"audit_log_tenant_time_idx",
		"audit_log_resource_idx",
	} {
		if !strings.Contains(sql, "CREATE INDEX IF NOT EXISTS "+index) {
			t.Errorf("migration is missing idempotent index %s", index)
		}
	}
	if !strings.Contains(sql, "ALTER TABLE projects ADD COLUMN IF NOT EXISTS organization_id uuid;") {
		t.Error("projects are not opt-in tenant aware")
	}
	if strings.Contains(sql, "ALTER TABLE projects ADD COLUMN IF NOT EXISTS organization_id uuid NOT NULL") {
		t.Error("existing projects would be broken by a NOT NULL tenant column")
	}
	if !strings.Contains(sql, "CREATE OR REPLACE FUNCTION reject_audit_log_mutation()") ||
		!strings.Contains(sql, "BEFORE UPDATE OR DELETE ON audit_log") {
		t.Error("audit_log is not protected as append-only")
	}
	for _, forbidden := range []string{
		"client_secret text",
		"private_key text",
		"access_token text",
		"DROP TABLE",
		"TRUNCATE TABLE",
		"DELETE FROM projects",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("migration contains forbidden destructive or secret-bearing SQL %q", forbidden)
		}
	}
}

func TestEnterpriseFoundationPostgresIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repository, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}

	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID, organizationID, projectID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) VALUES($1,$2,$3,$4,$5)`,
		userID, uuid.NewString()+"@example.com", "Enterprise Test", "test-only", now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`,
		organizationID, "test-"+strings.ToLower(strings.ReplaceAll(uuid.NewString(), "-", ""))[:12], "Enterprise Test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_members(organization_id,user_id,role,status,created_at,joined_at)
		VALUES($1,$2,'owner','active',$3,$3)`, organizationID, userID, now); err != nil {
		t.Fatal(err)
	}
	// Legacy project creation remains valid before an explicit tenant attach.
	if _, err := tx.Exec(ctx, `INSERT INTO projects(id,name,description,owner_id,created_at,updated_at)
		VALUES($1,'Legacy','',$2,$3,$3)`, projectID, userID, now); err != nil {
		t.Fatal(err)
	}
	var tenantMissing bool
	if err := tx.QueryRow(ctx, `SELECT organization_id IS NULL FROM projects WHERE id=$1`, projectID).Scan(&tenantMissing); err != nil || !tenantMissing {
		t.Fatalf("legacy project tenant state = %v, err=%v", tenantMissing, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET organization_id=$2 WHERE id=$1`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO sso_connections(
		id,organization_id,name,protocol,issuer_url,domains,enabled,created_at,updated_at)
		VALUES($1,$2,'Corporate OIDC','oidc','https://identity.example.com',$3,true,$4,$4)`,
		uuid.NewString(), organizationID, []string{"example.com"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO policy_rules(
		id,organization_id,description,effect,actions,resources,roles,created_at,updated_at)
		VALUES($1,$2,'Members can read','allow',$3,$4,$5,$6,$6)`,
		uuid.NewString(), organizationID, []string{"flows.read"}, []string{"project:*"}, []string{"member"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO plugin_registrations(
		id,organization_id,plugin_key,version,status,source_url,checksum,capabilities,installed_by,created_at,updated_at)
		VALUES($1,$2,'com.acme.simulator','1.0.0','active','oci://registry.example.com/acme/simulator',$3,$4,$5,$6,$6)`,
		uuid.NewString(), organizationID, "sha256:"+strings.Repeat("a", 64), []string{"flows.read"}, userID, now); err != nil {
		t.Fatal(err)
	}
	auditID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(
		id,organization_id,sequence,actor_id,action,resource_type,resource_id,outcome,metadata,occurred_at,previous_hash,event_hash)
		VALUES($1,$2,1,$3,'flows.create','flow','flow:orders','succeeded','{}'::jsonb,$4,$5,$6)`,
		auditID, organizationID, userID, now, strings.Repeat("0", 64), strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}

	var organizations, members, connections, policies, plugins, auditEvents int
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM organizations WHERE id=$1),
		(SELECT count(*) FROM organization_members WHERE organization_id=$1),
		(SELECT count(*) FROM sso_connections WHERE organization_id=$1),
		(SELECT count(*) FROM policy_rules WHERE organization_id=$1),
		(SELECT count(*) FROM plugin_registrations WHERE organization_id=$1),
		(SELECT count(*) FROM audit_log WHERE organization_id=$1)`, organizationID).
		Scan(&organizations, &members, &connections, &policies, &plugins, &auditEvents); err != nil {
		t.Fatal(err)
	}
	if organizations != 1 || members != 1 || connections != 1 || policies != 1 || plugins != 1 || auditEvents != 1 {
		t.Fatalf("enterprise rows = org:%d members:%d sso:%d policies:%d plugins:%d audit:%d",
			organizations, members, connections, policies, plugins, auditEvents)
	}

	if _, err := tx.Exec(ctx, `SAVEPOINT invalid_enterprise_policy`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO policy_rules(id,organization_id,effect,actions,resources,roles)
		VALUES($1,$2,'allow',$3,$4,$5)`, uuid.NewString(), organizationID,
		[]string{"flows.read"}, []string{"project:*"}, []string{"root"}); err == nil {
		t.Fatal("invalid policy role passed the database constraint")
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_enterprise_policy`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_log SET action='tampered' WHERE id=$1`, auditID); err == nil || !strings.Contains(err.Error(), "audit_log is append-only") {
		t.Fatalf("audit mutation error = %v", err)
	}
}
