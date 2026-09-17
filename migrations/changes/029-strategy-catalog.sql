CREATE TABLE strategy_catalog (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    harbor_resource_id text NOT NULL REFERENCES managed_resources(id) ON DELETE RESTRICT,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('scheduler', 'descheduler', 'autoscaler')),
    source_image text NOT NULL,
    default_configuration jsonb NOT NULL DEFAULT '{}'::jsonb,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, kind, name)
);

CREATE INDEX strategy_catalog_workspace_created_idx ON strategy_catalog(workspace_id, created_at DESC);
