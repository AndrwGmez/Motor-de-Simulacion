CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    email text NOT NULL,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT users_email_lower_unique UNIQUE (email),
    CONSTRAINT users_email_lowercase CHECK (email = lower(email))
);

CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('access', 'refresh')),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    family_id uuid NOT NULL
);
CREATE INDEX IF NOT EXISTS auth_sessions_user_idx ON auth_sessions(user_id);
CREATE INDEX IF NOT EXISTS auth_sessions_family_idx ON auth_sessions(family_id);

CREATE TABLE IF NOT EXISTS projects (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    owner_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS project_members_user_idx ON project_members(user_id);

CREATE TABLE IF NOT EXISTS flows (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    draft jsonb NOT NULL,
    draft_etag text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS flows_project_idx ON flows(project_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS flow_versions (
    id uuid PRIMARY KEY,
    flow_id uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    version_number integer NOT NULL CHECK (version_number > 0),
    definition jsonb NOT NULL,
    checksum text NOT NULL,
    created_at timestamptz NOT NULL,
    published_by uuid NOT NULL REFERENCES users(id),
    UNIQUE (flow_id, version_number)
);
CREATE INDEX IF NOT EXISTS flow_versions_flow_idx ON flow_versions(flow_id, version_number DESC);

CREATE TABLE IF NOT EXISTS runs (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    flow_id uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    version_id uuid REFERENCES flow_versions(id),
    status text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS runs_flow_idx ON runs(flow_id, created_at DESC);

CREATE TABLE IF NOT EXISTS run_idempotency_keys (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_type text NOT NULL CHECK (target_type IN ('flow_version', 'flow_draft')),
    target_id uuid NOT NULL,
    target_revision text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 128),
    request_hash text NOT NULL CHECK (char_length(request_hash) = 64),
    run_id uuid NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, target_type, target_id, target_revision, idempotency_key),
    CHECK (expires_at >= created_at + interval '24 hours')
);
CREATE INDEX IF NOT EXISTS run_idempotency_expiry_idx ON run_idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS run_events (
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    event jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, sequence)
);

CREATE TABLE IF NOT EXISTS node_runs (
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    node_run jsonb NOT NULL,
    PRIMARY KEY (run_id, ordinal)
);

CREATE TABLE IF NOT EXISTS share_links (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    flow_id uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    version_id uuid NOT NULL REFERENCES flow_versions(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS share_links_flow_idx ON share_links(flow_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
