CREATE TABLE workspaces (
    id text PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE operations (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    plugin_id text NOT NULL,
    title text NOT NULL,
    status text NOT NULL,
    spec jsonb NOT NULL,
    plan jsonb NOT NULL,
    validation jsonb NOT NULL,
    plan_hash text NOT NULL,
    cancel_requested boolean NOT NULL DEFAULT false,
    error text NOT NULL DEFAULT '',
    lease_owner text,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    queued_at timestamptz,
    started_at timestamptz,
    completed_at timestamptz
);

CREATE INDEX operations_status_created_idx ON operations(status, created_at);
CREATE INDEX operations_workspace_created_idx ON operations(workspace_id, created_at DESC);

CREATE TABLE operation_steps (
    id text PRIMARY KEY,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    position integer NOT NULL,
    name text NOT NULL,
    status text NOT NULL,
    input jsonb NOT NULL,
    result jsonb,
    health jsonb,
    error text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE(operation_id, position)
);

CREATE TABLE operation_logs (
    sequence bigserial PRIMARY KEY,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    step_id text,
    level text NOT NULL,
    source text NOT NULL,
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX operation_logs_operation_sequence_idx ON operation_logs(operation_id, sequence);

CREATE TABLE artifacts (
    id text PRIMARY KEY,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    step_id text,
    name text NOT NULL,
    media_type text NOT NULL,
    storage_key text NOT NULL UNIQUE,
    digest text NOT NULL,
    size_bytes bigint NOT NULL,
    sensitive boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
