CREATE TABLE managed_resources (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    name text NOT NULL,
    kind text NOT NULL,
    provider text NOT NULL,
    connection_id text REFERENCES provider_connections(id) ON DELETE SET NULL,
    plugin_id text NOT NULL,
    plugin_version text NOT NULL,
    plugin_digest text NOT NULL DEFAULT '',
    spec jsonb NOT NULL,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    deletion_operation_id text REFERENCES operations(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(kind, provider, connection_id, name)
);

CREATE INDEX managed_resources_kind_created_idx ON managed_resources(kind, created_at DESC);
CREATE INDEX managed_resources_connection_idx ON managed_resources(connection_id) WHERE connection_id IS NOT NULL;

CREATE TABLE managed_resource_dependencies (
    resource_id text NOT NULL REFERENCES managed_resources(id) ON DELETE CASCADE,
    depends_on_id text NOT NULL REFERENCES managed_resources(id) ON DELETE RESTRICT,
    relation text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(resource_id, depends_on_id, relation),
    CHECK(resource_id <> depends_on_id)
);

CREATE INDEX managed_resource_dependencies_target_idx ON managed_resource_dependencies(depends_on_id);
