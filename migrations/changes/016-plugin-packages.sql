CREATE TABLE plugin_packages (
    sequence bigserial PRIMARY KEY,
    plugin_id text NOT NULL,
    version text NOT NULL,
    digest text NOT NULL,
    descriptor text NOT NULL,
    descriptor_digest text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(plugin_id, version, digest, descriptor_digest)
);

CREATE UNIQUE INDEX plugin_packages_active_id_idx ON plugin_packages(plugin_id) WHERE active;
