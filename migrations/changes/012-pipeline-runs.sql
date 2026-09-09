CREATE TABLE pipeline_runs (
    id text PRIMARY KEY,
    pipeline_id text NOT NULL REFERENCES pipelines(id) ON DELETE RESTRICT,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    name text NOT NULL,
    status text NOT NULL,
    pipeline_hash text NOT NULL,
    result_type text NOT NULL,
    result_version text NOT NULL,
    result_artifact_id text REFERENCES artifacts(id) ON DELETE RESTRICT,
    cancel_requested boolean NOT NULL DEFAULT false,
    error text NOT NULL DEFAULT '',
    lease_owner text,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    queued_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    CHECK (length(pipeline_hash) = 64)
);

CREATE INDEX pipeline_runs_status_created_idx ON pipeline_runs(status, created_at);
CREATE INDEX pipeline_runs_workspace_created_idx ON pipeline_runs(workspace_id, created_at DESC);

CREATE TABLE pipeline_run_stages (
    id text PRIMARY KEY,
    run_id text NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    position integer NOT NULL,
    stage_id text NOT NULL,
    plugin_id text NOT NULL,
    title text NOT NULL,
    status text NOT NULL,
    operation_id text UNIQUE REFERENCES operations(id) ON DELETE RESTRICT,
    spec jsonb,
    error text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE(run_id, position),
    UNIQUE(run_id, stage_id),
    CHECK (status IN ('pending', 'queued', 'prechecking', 'running', 'verifying', 'succeeded', 'failed', 'canceled'))
);

CREATE INDEX pipeline_run_stages_run_idx ON pipeline_run_stages(run_id, position);
