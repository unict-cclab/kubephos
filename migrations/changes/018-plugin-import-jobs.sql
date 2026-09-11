CREATE TABLE plugin_import_jobs (
    id text PRIMARY KEY,
    descriptor text NOT NULL,
    descriptor_digest text NOT NULL,
    manifest jsonb,
    plugin_id text,
    version text,
    digest text,
    package_sequence bigint REFERENCES plugin_packages(sequence),
    status text NOT NULL CHECK (status IN ('queued', 'inspecting', 'validating', 'activating', 'succeeded', 'failed')),
    progress smallint NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    message text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    lease_owner text,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX plugin_import_jobs_queue_idx ON plugin_import_jobs(status, created_at);
