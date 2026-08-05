ALTER TABLE runs ADD COLUMN IF NOT EXISTS started_at timestamptz;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS completed_at timestamptz;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS output jsonb;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS error text NOT NULL DEFAULT '';

COMMENT ON COLUMN runs.payload IS
    'Immutable run input and flow snapshot. Events, node visits and mutable execution state are stored separately.';
