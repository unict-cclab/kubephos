CREATE TABLE pipelines (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    definition jsonb NOT NULL,
    resolution jsonb NOT NULL,
    validation jsonb NOT NULL,
    pipeline_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(pipeline_hash) = 64),
    UNIQUE(workspace_id, name)
);

CREATE INDEX pipelines_workspace_created_idx ON pipelines(workspace_id, created_at DESC);
CREATE INDEX pipelines_hash_idx ON pipelines(pipeline_hash);
