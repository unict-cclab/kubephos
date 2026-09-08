CREATE TABLE provider_connections (
    id text PRIMARY KEY,
    name text NOT NULL,
    provider text NOT NULL,
    plugin_id text NOT NULL,
    configuration jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(provider, name)
);

CREATE INDEX provider_connections_provider_idx ON provider_connections(provider, name);
