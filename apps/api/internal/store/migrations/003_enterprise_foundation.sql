CREATE TABLE IF NOT EXISTS organizations (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_slug_canonical CHECK (
        slug = lower(slug)
        AND slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
    ),
    CONSTRAINT organizations_name_nonempty CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT organizations_timestamps_ordered CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS organization_members (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'auditor')),
    status text NOT NULL DEFAULT 'invited' CHECK (status IN ('invited', 'active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    joined_at timestamptz,
    PRIMARY KEY (organization_id, user_id),
    CONSTRAINT organization_members_owner_active CHECK (role <> 'owner' OR status = 'active'),
    CONSTRAINT organization_members_join_state CHECK (
        (status = 'active' AND joined_at IS NOT NULL)
        OR status = 'suspended'
        OR (status = 'invited' AND joined_at IS NULL)
    ),
    CONSTRAINT organization_members_join_order CHECK (joined_at IS NULL OR joined_at >= created_at)
);
CREATE INDEX IF NOT EXISTS organization_members_user_idx
    ON organization_members(user_id, organization_id);
CREATE INDEX IF NOT EXISTS organization_members_tenant_role_idx
    ON organization_members(organization_id, role) WHERE status = 'active';

-- Existing projects deliberately remain valid and tenant-less. They can be
-- attached to an organization explicitly during a later migration workflow.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS organization_id uuid;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'projects_organization_fk'
          AND conrelid = 'projects'::regclass
    ) THEN
        ALTER TABLE projects
            ADD CONSTRAINT projects_organization_fk
            FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;
    END IF;
END
$$;
CREATE INDEX IF NOT EXISTS projects_organization_idx
    ON projects(organization_id, created_at) WHERE organization_id IS NOT NULL;

-- Metadata-only SSO configuration. No client secrets, private keys, access
-- tokens, or full certificates are persisted in this table.
CREATE TABLE IF NOT EXISTS sso_connections (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    protocol text NOT NULL CHECK (protocol IN ('oidc', 'saml')),
    issuer_url text NOT NULL DEFAULT '',
    metadata_url text NOT NULL DEFAULT '',
    entity_id text NOT NULL DEFAULT '',
    sign_in_url text NOT NULL DEFAULT '',
    certificate_fingerprint text NOT NULL DEFAULT '',
    domains text[] NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    CONSTRAINT sso_connections_name_nonempty CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT sso_connections_domains_present CHECK (
        cardinality(domains) BETWEEN 1 AND 50
        AND array_position(domains, NULL) IS NULL
    ),
    CONSTRAINT sso_connections_fingerprint_format CHECK (
        certificate_fingerprint = ''
        OR certificate_fingerprint ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT sso_connections_protocol_metadata CHECK (
        (
            protocol = 'oidc'
            AND issuer_url <> ''
            AND entity_id = ''
            AND sign_in_url = ''
            AND certificate_fingerprint = ''
        )
        OR (
            protocol = 'saml'
            AND issuer_url = ''
            AND entity_id <> ''
            AND sign_in_url <> ''
            AND certificate_fingerprint ~ '^sha256:[0-9a-f]{64}$'
        )
    ),
    CONSTRAINT sso_connections_timestamps_ordered CHECK (updated_at >= created_at)
);
CREATE INDEX IF NOT EXISTS sso_connections_tenant_enabled_idx
    ON sso_connections(organization_id, enabled, protocol);
CREATE INDEX IF NOT EXISTS sso_connections_domains_idx
    ON sso_connections USING gin(domains);

CREATE TABLE IF NOT EXISTS policy_rules (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    description text NOT NULL DEFAULT '',
    effect text NOT NULL CHECK (effect IN ('allow', 'deny')),
    actions text[] NOT NULL,
    resources text[] NOT NULL,
    roles text[] NOT NULL DEFAULT '{}'::text[],
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT policy_rules_description_length CHECK (char_length(description) <= 500),
    CONSTRAINT policy_rules_actions_present CHECK (
        cardinality(actions) BETWEEN 1 AND 32
        AND array_position(actions, NULL) IS NULL
    ),
    CONSTRAINT policy_rules_resources_present CHECK (
        cardinality(resources) BETWEEN 1 AND 32
        AND array_position(resources, NULL) IS NULL
    ),
    CONSTRAINT policy_rules_roles_valid CHECK (
        cardinality(roles) <= 4
        AND array_position(roles, NULL) IS NULL
        AND roles <@ ARRAY['owner', 'admin', 'member', 'auditor']::text[]
    ),
    CONSTRAINT policy_rules_timestamps_ordered CHECK (updated_at >= created_at)
);
CREATE INDEX IF NOT EXISTS policy_rules_tenant_enabled_idx
    ON policy_rules(organization_id, effect, id) WHERE disabled = false;

CREATE TABLE IF NOT EXISTS plugin_registrations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    plugin_key text NOT NULL,
    version text NOT NULL,
    status text NOT NULL DEFAULT 'disabled' CHECK (status IN ('active', 'disabled', 'revoked')),
    source_url text NOT NULL,
    checksum text NOT NULL,
    capabilities text[] NOT NULL DEFAULT '{}'::text[],
    installed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, plugin_key),
    CONSTRAINT plugin_registrations_key_canonical CHECK (
        plugin_key = lower(plugin_key)
        AND plugin_key ~ '^[a-z0-9]([a-z0-9._-]{0,126}[a-z0-9])?$'
    ),
    CONSTRAINT plugin_registrations_version_nonempty CHECK (version = btrim(version) AND char_length(version) BETWEEN 1 AND 128),
    CONSTRAINT plugin_registrations_checksum_format CHECK (checksum ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT plugin_registrations_capabilities_valid CHECK (
        cardinality(capabilities) <= 32
        AND array_position(capabilities, NULL) IS NULL
    ),
    CONSTRAINT plugin_registrations_timestamps_ordered CHECK (updated_at >= created_at)
);
CREATE INDEX IF NOT EXISTS plugin_registrations_tenant_status_idx
    ON plugin_registrations(organization_id, status, plugin_key);

CREATE TABLE IF NOT EXISTS audit_log (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    sequence bigint NOT NULL CHECK (sequence > 0),
    actor_id uuid,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'denied', 'failed')),
    request_id text NOT NULL DEFAULT '',
    source_ip inet,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    previous_hash character(64) NOT NULL,
    event_hash character(64) NOT NULL,
    UNIQUE (organization_id, sequence),
    UNIQUE (organization_id, event_hash),
    CONSTRAINT audit_log_action_nonempty CHECK (action <> ''),
    CONSTRAINT audit_log_resource_type_nonempty CHECK (resource_type <> ''),
    CONSTRAINT audit_log_resource_id_nonempty CHECK (resource_id <> ''),
    CONSTRAINT audit_log_request_id_length CHECK (char_length(request_id) <= 100),
    CONSTRAINT audit_log_previous_hash_format CHECK (previous_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT audit_log_event_hash_format CHECK (event_hash ~ '^[0-9a-f]{64}$')
);
CREATE INDEX IF NOT EXISTS audit_log_tenant_time_idx
    ON audit_log(organization_id, occurred_at DESC, sequence DESC);
CREATE INDEX IF NOT EXISTS audit_log_actor_idx
    ON audit_log(organization_id, actor_id, occurred_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS audit_log_resource_idx
    ON audit_log(organization_id, resource_type, resource_id, occurred_at DESC);

CREATE OR REPLACE FUNCTION reject_audit_log_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_trigger
        WHERE tgname = 'audit_log_immutable'
          AND tgrelid = 'audit_log'::regclass
    ) THEN
        CREATE TRIGGER audit_log_immutable
            BEFORE UPDATE OR DELETE ON audit_log
            FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation();
    END IF;
END
$$;
