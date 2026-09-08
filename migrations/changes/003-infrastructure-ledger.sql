CREATE TABLE infrastructure_resources (
    id text PRIMARY KEY,
    provider_plugin_id text NOT NULL,
    external_id text NOT NULL,
    workspace_id text REFERENCES workspaces(id) ON DELETE RESTRICT,
    kind text NOT NULL,
    name text NOT NULL,
    state text NOT NULL,
    ownership text NOT NULL,
    protection text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(provider_plugin_id, external_id),
    CHECK (ownership IN ('imported', 'managed')),
    CHECK (protection IN ('read-only', 'managed')),
    CHECK ((ownership = 'imported' AND protection = 'read-only') OR ownership = 'managed')
);

CREATE INDEX infrastructure_resources_ownership_idx ON infrastructure_resources(ownership, kind, name);

CREATE TABLE audit_events (
    sequence bigserial PRIMARY KEY,
    actor text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL DEFAULT '',
    outcome text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_created_idx ON audit_events(created_at DESC);
