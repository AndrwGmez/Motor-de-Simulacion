CREATE TABLE IF NOT EXISTS run_leases (
    run_id uuid PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    instance_id text NOT NULL,
    actor_id text NOT NULL DEFAULT '',
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    acquired_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    released_at timestamptz
);

CREATE INDEX IF NOT EXISTS run_leases_active_idx
    ON run_leases(project_id, expires_at DESC)
    WHERE released_at IS NULL;

CREATE INDEX IF NOT EXISTS run_leases_actor_idx
    ON run_leases(actor_id, expires_at DESC)
    WHERE released_at IS NULL AND actor_id <> '';
