CREATE TABLE experiment_configurations (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    cluster_resource_id text NOT NULL REFERENCES managed_resources(id) ON DELETE RESTRICT,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    application_ref text NOT NULL,
    application_digest text NOT NULL,
    definition jsonb NOT NULL,
    validation jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX experiment_configurations_workspace_name_idx ON experiment_configurations(workspace_id, lower(name));
CREATE INDEX experiment_configurations_cluster_idx ON experiment_configurations(cluster_resource_id);
