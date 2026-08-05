package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/enterprise"
)

func (p *Postgres) CreateOrganization(ctx context.Context, organization enterprise.Organization) error {
	organization.CreatedAt = storedEnterpriseTime(organization.CreatedAt)
	organization.UpdatedAt = storedEnterpriseTime(organization.UpdatedAt)
	if err := organization.Validate(); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6)`, organization.ID, organization.Slug, organization.Name,
		organization.Status, organization.CreatedAt, organization.UpdatedAt); err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, organization.ID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) CreateOrganizationWithOwner(
	ctx context.Context,
	organization enterprise.Organization,
	owner enterprise.OrganizationMembership,
) error {
	organization.CreatedAt = storedEnterpriseTime(organization.CreatedAt)
	organization.UpdatedAt = storedEnterpriseTime(organization.UpdatedAt)
	owner.CreatedAt = storedEnterpriseTime(owner.CreatedAt)
	owner.JoinedAt = storedEnterpriseTimePointer(owner.JoinedAt)
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
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6)`, organization.ID, organization.Slug, organization.Name,
		organization.Status, organization.CreatedAt, organization.UpdatedAt); err != nil {
		return translateEnterprise(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_members(
		organization_id,user_id,role,status,created_at,joined_at) VALUES($1,$2,$3,$4,$5,$6)`,
		owner.OrganizationID, owner.UserID, owner.Role, owner.Status, owner.CreatedAt, owner.JoinedAt); err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, organization.ID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) ListOrganizations(ctx context.Context, userID string) ([]enterprise.Organization, error) {
	rows, err := p.pool.Query(ctx, `SELECT o.id::text,o.slug,o.name,o.status,o.created_at,o.updated_at
		FROM organizations o JOIN organization_members m ON m.organization_id=o.id
		WHERE m.user_id=$1 ORDER BY o.created_at,o.id`, userID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.Organization{}
	for rows.Next() {
		organization, scanErr := scanOrganization(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, organization)
	}
	return result, translateEnterprise(rows.Err())
}

func (p *Postgres) GetOrganization(ctx context.Context, organizationID string) (enterprise.Organization, error) {
	organization, err := scanOrganization(p.pool.QueryRow(ctx, `SELECT id::text,slug,name,status,created_at,updated_at
		FROM organizations WHERE id=$1`, organizationID))
	return organization, translateEnterprise(err)
}

func scanOrganization(row rowScanner) (enterprise.Organization, error) {
	var organization enterprise.Organization
	var status string
	err := row.Scan(&organization.ID, &organization.Slug, &organization.Name, &status, &organization.CreatedAt, &organization.UpdatedAt)
	organization.Status = enterprise.OrganizationStatus(status)
	return organization, err
}

func (p *Postgres) GetMembership(
	ctx context.Context,
	organizationID, userID string,
) (enterprise.OrganizationMembership, error) {
	membership, err := scanMembership(p.pool.QueryRow(ctx, `SELECT organization_id::text,user_id::text,role,status,created_at,joined_at
		FROM organization_members WHERE organization_id=$1 AND user_id=$2`, organizationID, userID))
	return membership, translateEnterprise(err)
}

func (p *Postgres) ListMemberships(ctx context.Context, organizationID string) ([]enterprise.OrganizationMembership, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT organization_id::text,user_id::text,role,status,created_at,joined_at
		FROM organization_members WHERE organization_id=$1 ORDER BY created_at,user_id`, organizationID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.OrganizationMembership{}
	for rows.Next() {
		membership, scanErr := scanMembership(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, membership)
	}
	return result, translateEnterprise(rows.Err())
}

func scanMembership(row rowScanner) (enterprise.OrganizationMembership, error) {
	var membership enterprise.OrganizationMembership
	var role, status string
	err := row.Scan(&membership.OrganizationID, &membership.UserID, &role, &status, &membership.CreatedAt, &membership.JoinedAt)
	membership.Role = enterprise.OrganizationRole(role)
	membership.Status = enterprise.MembershipStatus(status)
	return membership, err
}

func (p *Postgres) SetMembership(ctx context.Context, membership enterprise.OrganizationMembership) error {
	membership.CreatedAt = storedEnterpriseTime(membership.CreatedAt)
	membership.JoinedAt = storedEnterpriseTimePointer(membership.JoinedAt)
	if err := membership.Validate(); err != nil {
		return err
	}
	tx, err := p.beginLockedOrganizationTx(ctx, membership.OrganizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentCreatedAt time.Time
	currentErr := tx.QueryRow(ctx, `SELECT created_at FROM organization_members
		WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, membership.OrganizationID, membership.UserID).Scan(&currentCreatedAt)
	switch {
	case currentErr == nil && !currentCreatedAt.Equal(membership.CreatedAt):
		return ErrConflict
	case currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows):
		return translateEnterprise(currentErr)
	}
	if membership.Role != enterprise.OrganizationOwner || membership.Status != enterprise.MembershipActive {
		var otherOwners int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_members
			WHERE organization_id=$1 AND user_id<>$2 AND role='owner' AND status='active'`,
			membership.OrganizationID, membership.UserID).Scan(&otherOwners); err != nil {
			return translateEnterprise(err)
		}
		if otherOwners == 0 {
			return ErrLastOrganizationOwner
		}
	}
	if errors.Is(currentErr, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO organization_members(
			organization_id,user_id,role,status,created_at,joined_at) VALUES($1,$2,$3,$4,$5,$6)`,
			membership.OrganizationID, membership.UserID, membership.Role, membership.Status,
			membership.CreatedAt, membership.JoinedAt)
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_members SET role=$3,status=$4,joined_at=$5
			WHERE organization_id=$1 AND user_id=$2`, membership.OrganizationID, membership.UserID,
			membership.Role, membership.Status, membership.JoinedAt)
	}
	if err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, membership.OrganizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) SaveSSOConnection(ctx context.Context, connection enterprise.SSOConnection) error {
	connection = connection.Normalize()
	connection.CreatedAt = storedEnterpriseTime(connection.CreatedAt)
	connection.UpdatedAt = storedEnterpriseTime(connection.UpdatedAt)
	if err := connection.Validate(); err != nil {
		return err
	}
	tx, err := p.beginLockedOrganizationTx(ctx, connection.OrganizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentOrganizationID string
	var currentCreatedAt, currentUpdatedAt time.Time
	currentErr := tx.QueryRow(ctx, `SELECT organization_id::text,created_at,updated_at
		FROM sso_connections WHERE id=$1 FOR UPDATE`, connection.ID).
		Scan(&currentOrganizationID, &currentCreatedAt, &currentUpdatedAt)
	switch {
	case currentErr == nil && currentOrganizationID != connection.OrganizationID:
		return ErrConflict
	case currentErr == nil && (!currentCreatedAt.Equal(connection.CreatedAt) || connection.UpdatedAt.Before(currentUpdatedAt)):
		return ErrConflict
	case currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows):
		return translateEnterprise(currentErr)
	}
	if errors.Is(currentErr, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO sso_connections(
			id,organization_id,name,protocol,issuer_url,metadata_url,entity_id,sign_in_url,
			certificate_fingerprint,domains,enabled,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, connection.ID,
			connection.OrganizationID, connection.Name, connection.Protocol, connection.IssuerURL,
			connection.MetadataURL, connection.EntityID, connection.SignInURL,
			connection.CertificateFingerprint, connection.Domains, connection.Enabled,
			connection.CreatedAt, connection.UpdatedAt)
	} else {
		_, err = tx.Exec(ctx, `UPDATE sso_connections SET name=$3,protocol=$4,issuer_url=$5,
			metadata_url=$6,entity_id=$7,sign_in_url=$8,certificate_fingerprint=$9,domains=$10,
			enabled=$11,updated_at=$12 WHERE id=$1 AND organization_id=$2`, connection.ID,
			connection.OrganizationID, connection.Name, connection.Protocol, connection.IssuerURL,
			connection.MetadataURL, connection.EntityID, connection.SignInURL,
			connection.CertificateFingerprint, connection.Domains, connection.Enabled, connection.UpdatedAt)
	}
	if err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, connection.OrganizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) GetSSOConnection(
	ctx context.Context,
	organizationID, connectionID string,
) (enterprise.SSOConnection, error) {
	connection, err := scanSSOConnection(p.pool.QueryRow(ctx, ssoSelect+` WHERE organization_id=$1 AND id=$2`, organizationID, connectionID))
	return connection, translateEnterprise(err)
}

func (p *Postgres) ListSSOConnections(ctx context.Context, organizationID string) ([]enterprise.SSOConnection, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, ssoSelect+` WHERE organization_id=$1 ORDER BY name,id`, organizationID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.SSOConnection{}
	for rows.Next() {
		connection, scanErr := scanSSOConnection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, connection)
	}
	return result, translateEnterprise(rows.Err())
}

const ssoSelect = `SELECT id::text,organization_id::text,name,protocol,issuer_url,metadata_url,
	entity_id,sign_in_url,certificate_fingerprint,domains,enabled,created_at,updated_at FROM sso_connections`

func scanSSOConnection(row rowScanner) (enterprise.SSOConnection, error) {
	var connection enterprise.SSOConnection
	var protocol string
	err := row.Scan(&connection.ID, &connection.OrganizationID, &connection.Name, &protocol,
		&connection.IssuerURL, &connection.MetadataURL, &connection.EntityID, &connection.SignInURL,
		&connection.CertificateFingerprint, &connection.Domains, &connection.Enabled,
		&connection.CreatedAt, &connection.UpdatedAt)
	connection.Protocol = enterprise.SSOProtocol(protocol)
	return connection, err
}

func (p *Postgres) SavePolicyRule(ctx context.Context, rule enterprise.PolicyRule) error {
	rule = rule.Normalize()
	rule.CreatedAt = storedEnterpriseTime(rule.CreatedAt)
	rule.UpdatedAt = storedEnterpriseTime(rule.UpdatedAt)
	if err := rule.Validate(); err != nil {
		return err
	}
	tx, err := p.beginLockedOrganizationTx(ctx, rule.OrganizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentOrganizationID string
	var currentCreatedAt, currentUpdatedAt time.Time
	currentErr := tx.QueryRow(ctx, `SELECT organization_id::text,created_at,updated_at
		FROM policy_rules WHERE id=$1 FOR UPDATE`, rule.ID).
		Scan(&currentOrganizationID, &currentCreatedAt, &currentUpdatedAt)
	switch {
	case currentErr == nil && currentOrganizationID != rule.OrganizationID:
		return ErrConflict
	case currentErr == nil && (!currentCreatedAt.Equal(rule.CreatedAt) || rule.UpdatedAt.Before(currentUpdatedAt)):
		return ErrConflict
	case currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows):
		return translateEnterprise(currentErr)
	}
	roles := organizationRolesToStrings(rule.Conditions.Roles)
	if errors.Is(currentErr, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO policy_rules(
			id,organization_id,description,effect,actions,resources,roles,disabled,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, rule.ID, rule.OrganizationID,
			rule.Description, rule.Effect, rule.Actions, rule.Resources, roles, rule.Disabled,
			rule.CreatedAt, rule.UpdatedAt)
	} else {
		_, err = tx.Exec(ctx, `UPDATE policy_rules SET description=$3,effect=$4,actions=$5,
			resources=$6,roles=$7,disabled=$8,updated_at=$9 WHERE id=$1 AND organization_id=$2`,
			rule.ID, rule.OrganizationID, rule.Description, rule.Effect, rule.Actions,
			rule.Resources, roles, rule.Disabled, rule.UpdatedAt)
	}
	if err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, rule.OrganizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) GetPolicyRule(ctx context.Context, organizationID, ruleID string) (enterprise.PolicyRule, error) {
	rule, err := scanPolicyRule(p.pool.QueryRow(ctx, policySelect+` WHERE organization_id=$1 AND id=$2`, organizationID, ruleID))
	return rule, translateEnterprise(err)
}

func (p *Postgres) ListPolicyRules(ctx context.Context, organizationID string) ([]enterprise.PolicyRule, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, policySelect+` WHERE organization_id=$1 ORDER BY id`, organizationID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.PolicyRule{}
	for rows.Next() {
		rule, scanErr := scanPolicyRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, rule)
	}
	return result, translateEnterprise(rows.Err())
}

func (p *Postgres) DeletePolicyRule(ctx context.Context, organizationID, ruleID string) error {
	tx, err := p.beginLockedOrganizationTx(ctx, organizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM policy_rules WHERE organization_id=$1 AND id=$2`, organizationID, ruleID)
	if err != nil {
		return translateEnterprise(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = p.appendContextAuditTx(ctx, tx, organizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

const policySelect = `SELECT id::text,organization_id::text,description,effect,actions,resources,
	roles,disabled,created_at,updated_at FROM policy_rules`

func scanPolicyRule(row rowScanner) (enterprise.PolicyRule, error) {
	var rule enterprise.PolicyRule
	var effect string
	var roles []string
	err := row.Scan(&rule.ID, &rule.OrganizationID, &rule.Description, &effect, &rule.Actions,
		&rule.Resources, &roles, &rule.Disabled, &rule.CreatedAt, &rule.UpdatedAt)
	rule.Effect = enterprise.PolicyEffect(effect)
	rule.Conditions.Roles = stringsToOrganizationRoles(roles)
	return rule, err
}

func (p *Postgres) SavePluginRegistration(ctx context.Context, registration enterprise.PluginRegistration) error {
	registration = registration.Normalize()
	registration.CreatedAt = storedEnterpriseTime(registration.CreatedAt)
	registration.UpdatedAt = storedEnterpriseTime(registration.UpdatedAt)
	if err := registration.Validate(); err != nil {
		return err
	}
	tx, err := p.beginLockedOrganizationTx(ctx, registration.OrganizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentOrganizationID, currentStatus string
	var currentCreatedAt, currentUpdatedAt time.Time
	currentErr := tx.QueryRow(ctx, `SELECT organization_id::text,status,created_at,updated_at
		FROM plugin_registrations WHERE id=$1 FOR UPDATE`, registration.ID).
		Scan(&currentOrganizationID, &currentStatus, &currentCreatedAt, &currentUpdatedAt)
	switch {
	case currentErr == nil && currentOrganizationID != registration.OrganizationID:
		return ErrConflict
	case currentErr == nil && enterprise.PluginStatus(currentStatus) == enterprise.PluginRevoked:
		return enterprise.ErrPluginRevoked
	case currentErr == nil && (!currentCreatedAt.Equal(registration.CreatedAt) || registration.UpdatedAt.Before(currentUpdatedAt)):
		return ErrConflict
	case currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows):
		return translateEnterprise(currentErr)
	}
	if errors.Is(currentErr, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO plugin_registrations(
			id,organization_id,plugin_key,version,status,source_url,checksum,capabilities,
			installed_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			registration.ID, registration.OrganizationID, registration.PluginKey, registration.Version,
			registration.Status, registration.SourceURL, registration.Checksum, registration.Capabilities,
			nullableUUID(registration.InstalledBy), registration.CreatedAt, registration.UpdatedAt)
	} else {
		_, err = tx.Exec(ctx, `UPDATE plugin_registrations SET plugin_key=$3,version=$4,status=$5,
			source_url=$6,checksum=$7,capabilities=$8,installed_by=$9,updated_at=$10
			WHERE id=$1 AND organization_id=$2`, registration.ID, registration.OrganizationID,
			registration.PluginKey, registration.Version, registration.Status, registration.SourceURL,
			registration.Checksum, registration.Capabilities, nullableUUID(registration.InstalledBy), registration.UpdatedAt)
	}
	if err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, registration.OrganizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) GetPluginRegistration(
	ctx context.Context,
	organizationID, registrationID string,
) (enterprise.PluginRegistration, error) {
	registration, err := scanPluginRegistration(p.pool.QueryRow(ctx,
		pluginSelect+` WHERE organization_id=$1 AND id=$2`, organizationID, registrationID))
	return registration, translateEnterprise(err)
}

func (p *Postgres) ListPluginRegistrations(ctx context.Context, organizationID string) ([]enterprise.PluginRegistration, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, pluginSelect+` WHERE organization_id=$1 ORDER BY plugin_key,id`, organizationID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.PluginRegistration{}
	for rows.Next() {
		registration, scanErr := scanPluginRegistration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, registration)
	}
	return result, translateEnterprise(rows.Err())
}

func (p *Postgres) SetPluginRegistrationStatus(
	ctx context.Context,
	organizationID, registrationID string,
	status enterprise.PluginStatus,
	updatedAt time.Time,
) error {
	if !status.Valid() {
		return &enterprise.ValidationError{Field: "status", Message: "must be active, disabled, or revoked"}
	}
	updatedAt = storedEnterpriseTime(updatedAt)
	if updatedAt.IsZero() {
		return &enterprise.ValidationError{Field: "updatedAt", Message: "is required"}
	}
	tx, err := p.beginLockedOrganizationTx(ctx, organizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	var currentUpdatedAt time.Time
	err = tx.QueryRow(ctx, `SELECT status,updated_at FROM plugin_registrations
		WHERE organization_id=$1 AND id=$2 FOR UPDATE`, organizationID, registrationID).
		Scan(&currentStatus, &currentUpdatedAt)
	if err != nil {
		return translateEnterprise(err)
	}
	if enterprise.PluginStatus(currentStatus) == enterprise.PluginRevoked {
		return enterprise.ErrPluginRevoked
	}
	if updatedAt.Before(currentUpdatedAt) {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE plugin_registrations SET status=$3,updated_at=$4
		WHERE organization_id=$1 AND id=$2`, organizationID, registrationID, status, updatedAt); err != nil {
		return translateEnterprise(err)
	}
	if _, err = p.appendContextAuditTx(ctx, tx, organizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

const pluginSelect = `SELECT id::text,organization_id::text,plugin_key,version,status,source_url,
	checksum,capabilities,COALESCE(installed_by::text,''),created_at,updated_at FROM plugin_registrations`

func scanPluginRegistration(row rowScanner) (enterprise.PluginRegistration, error) {
	var registration enterprise.PluginRegistration
	var status string
	err := row.Scan(&registration.ID, &registration.OrganizationID, &registration.PluginKey,
		&registration.Version, &status, &registration.SourceURL, &registration.Checksum,
		&registration.Capabilities, &registration.InstalledBy, &registration.CreatedAt, &registration.UpdatedAt)
	registration.Status = enterprise.PluginStatus(status)
	return registration, err
}

func (p *Postgres) AttachProjectToOrganization(ctx context.Context, organizationID, projectID string) error {
	tx, err := p.beginLockedOrganizationTx(ctx, organizationID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentOrganizationID string
	err = tx.QueryRow(ctx, `SELECT COALESCE(organization_id::text,'') FROM projects WHERE id=$1 FOR UPDATE`, projectID).
		Scan(&currentOrganizationID)
	if err != nil {
		return translateEnterprise(err)
	}
	if currentOrganizationID != "" && currentOrganizationID != organizationID {
		return ErrConflict
	}
	if currentOrganizationID == "" {
		if _, err = tx.Exec(ctx, `UPDATE projects SET organization_id=$2 WHERE id=$1 AND organization_id IS NULL`,
			projectID, organizationID); err != nil {
			return translateEnterprise(err)
		}
	}
	if _, err = p.appendContextAuditTx(ctx, tx, organizationID); err != nil {
		return err
	}
	return translateEnterprise(tx.Commit(ctx))
}

func (p *Postgres) ListOrganizationProjects(ctx context.Context, organizationID string) ([]domain.Project, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT id::text,name,description,owner_id::text,created_at,updated_at
		FROM projects WHERE organization_id=$1 ORDER BY created_at,id`, organizationID)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []domain.Project{}
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Description, &project.OwnerID,
			&project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, project)
	}
	return result, translateEnterprise(rows.Err())
}

func (p *Postgres) AppendAuditEvent(ctx context.Context, event enterprise.AuditEvent) (enterprise.AuditEvent, error) {
	tx, err := p.beginLockedOrganizationTx(ctx, event.OrganizationID)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	defer tx.Rollback(ctx)
	sealed, err := p.appendAuditDraftTx(ctx, tx, event.OrganizationID, event)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return enterprise.AuditEvent{}, translateEnterprise(err)
	}
	return cloneAuditEvent(sealed), nil
}

func (p *Postgres) ListAuditEvents(
	ctx context.Context,
	organizationID string,
	afterSequence uint64,
	limit int,
) ([]enterprise.AuditEvent, error) {
	if limit < 1 || limit > MaxAuditListLimit || afterSequence > math.MaxInt64 {
		return nil, ErrInvalidAuditLimit
	}
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, auditSelect+`
		WHERE organization_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, organizationID, int64(afterSequence), limit)
	if err != nil {
		return nil, translateEnterprise(err)
	}
	defer rows.Close()
	result := []enterprise.AuditEvent{}
	for rows.Next() {
		event, scanErr := scanAuditEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, event)
	}
	return result, translateEnterprise(rows.Err())
}

func (p *Postgres) GetAuditCheckpoint(ctx context.Context, organizationID string) (enterprise.AuditCheckpoint, error) {
	if err := p.ensureOrganization(ctx, organizationID); err != nil {
		return enterprise.AuditCheckpoint{}, err
	}
	checkpoint, err := auditCheckpointFromRow(organizationID, p.pool.QueryRow(ctx,
		`SELECT sequence,event_hash FROM audit_log WHERE organization_id=$1 ORDER BY sequence DESC LIMIT 1`, organizationID))
	return checkpoint, err
}

const auditSelect = `SELECT id::text,organization_id::text,sequence,COALESCE(actor_id::text,''),
	action,resource_type,resource_id,outcome,request_id,COALESCE(source_ip::text,''),metadata,
	occurred_at,previous_hash,event_hash FROM audit_log`

func scanAuditEvent(row rowScanner) (enterprise.AuditEvent, error) {
	var event enterprise.AuditEvent
	var sequence int64
	var outcome string
	var metadata []byte
	err := row.Scan(&event.ID, &event.OrganizationID, &sequence, &event.ActorID, &event.Action,
		&event.ResourceType, &event.ResourceID, &outcome, &event.RequestID, &event.SourceIP,
		&metadata, &event.OccurredAt, &event.PreviousHash, &event.Hash)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	if sequence < 1 {
		return enterprise.AuditEvent{}, ErrConflict
	}
	event.Sequence = uint64(sequence)
	event.Outcome = enterprise.AuditOutcome(outcome)
	event.OccurredAt = event.OccurredAt.UTC()
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	decoder.UseNumber()
	if err := decoder.Decode(&event.Metadata); err != nil {
		return enterprise.AuditEvent{}, err
	}
	if event.Metadata == nil {
		return enterprise.AuditEvent{}, ErrConflict
	}
	return event, nil
}

func (p *Postgres) appendContextAuditTx(
	ctx context.Context,
	tx pgx.Tx,
	organizationID string,
) (enterprise.AuditEvent, error) {
	draft, exists := enterpriseAuditFromContext(ctx)
	if !exists {
		return enterprise.AuditEvent{}, nil
	}
	if draft.OrganizationID != organizationID {
		return enterprise.AuditEvent{}, &enterprise.ValidationError{
			Field: "organizationId", Message: "audit event does not match the mutation tenant",
		}
	}
	return p.appendAuditDraftTx(ctx, tx, organizationID, draft)
}

func (p *Postgres) appendAuditDraftTx(
	ctx context.Context,
	tx pgx.Tx,
	organizationID string,
	draft enterprise.AuditEvent,
) (enterprise.AuditEvent, error) {
	checkpoint, err := auditCheckpointFromRow(organizationID, tx.QueryRow(ctx,
		`SELECT sequence,event_hash FROM audit_log WHERE organization_id=$1 ORDER BY sequence DESC LIMIT 1`, organizationID))
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	sealed, _, err := enterprise.SealAuditEvent(checkpoint, draft)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	if sealed.Sequence > math.MaxInt64 {
		return enterprise.AuditEvent{}, ErrConflict
	}
	metadata, err := json.Marshal(sealed.Metadata)
	if err != nil {
		return enterprise.AuditEvent{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(
		id,organization_id,sequence,actor_id,action,resource_type,resource_id,outcome,
		request_id,source_ip,metadata,occurred_at,previous_hash,event_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, sealed.ID,
		sealed.OrganizationID, int64(sealed.Sequence), nullableUUID(sealed.ActorID), sealed.Action,
		sealed.ResourceType, sealed.ResourceID, sealed.Outcome, sealed.RequestID,
		nullableString(sealed.SourceIP), metadata, sealed.OccurredAt, sealed.PreviousHash, sealed.Hash)
	if err != nil {
		return enterprise.AuditEvent{}, translateEnterprise(err)
	}
	return sealed, nil
}

func auditCheckpointFromRow(organizationID string, row pgx.Row) (enterprise.AuditCheckpoint, error) {
	checkpoint := enterprise.AuditCheckpoint{OrganizationID: organizationID, LastHash: enterprise.GenesisAuditHash}
	var sequence int64
	err := row.Scan(&sequence, &checkpoint.LastHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, nil
	}
	if err != nil {
		return enterprise.AuditCheckpoint{}, translateEnterprise(err)
	}
	if sequence < 1 {
		return enterprise.AuditCheckpoint{}, ErrConflict
	}
	checkpoint.LastSequence = uint64(sequence)
	return checkpoint, nil
}

func (p *Postgres) beginLockedOrganizationTx(ctx context.Context, organizationID string) (pgx.Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&lockedID); err != nil {
		tx.Rollback(ctx)
		return nil, translateEnterprise(err)
	}
	return tx, nil
}

func (p *Postgres) ensureOrganization(ctx context.Context, organizationID string) error {
	var id string
	return translateEnterprise(p.pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id=$1`, organizationID).Scan(&id))
}

func organizationRolesToStrings(roles []enterprise.OrganizationRole) []string {
	result := make([]string, len(roles))
	for index, role := range roles {
		result[index] = string(role)
	}
	return result
}

func stringsToOrganizationRoles(roles []string) []enterprise.OrganizationRole {
	result := make([]enterprise.OrganizationRole, len(roles))
	for index, role := range roles {
		result[index] = enterprise.OrganizationRole(role)
	}
	return result
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func storedEnterpriseTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func storedEnterpriseTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := storedEnterpriseTime(*value)
	return &normalized
}

func translateEnterprise(err error) error {
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23503" {
		return ErrNotFound
	}
	return translate(err)
}

var _ EnterpriseRepository = (*Postgres)(nil)
