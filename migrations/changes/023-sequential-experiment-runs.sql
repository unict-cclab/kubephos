ALTER TABLE experiments
    ADD COLUMN configuration_id text REFERENCES experiment_configurations(id) ON DELETE RESTRICT;

ALTER TABLE pipeline_runs
    ADD COLUMN cluster_resource_id text REFERENCES managed_resources(id) ON DELETE RESTRICT,
    ADD COLUMN terminal_status text,
    ADD CONSTRAINT pipeline_runs_terminal_status_check CHECK (terminal_status IS NULL OR terminal_status IN ('failed', 'canceled'));

CREATE INDEX pipeline_runs_cluster_active_idx ON pipeline_runs(cluster_resource_id, status) WHERE cluster_resource_id IS NOT NULL;

ALTER TABLE pipeline_run_stages
    ADD COLUMN cleanup_operation_id text UNIQUE REFERENCES operations(id) ON DELETE RESTRICT,
    ADD COLUMN cleanup_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN cleanup_error text NOT NULL DEFAULT '',
    ADD COLUMN cleanup_started_at timestamptz,
    ADD COLUMN cleanup_completed_at timestamptz,
    ADD CONSTRAINT pipeline_run_stages_cleanup_status_check CHECK (cleanup_status IN ('pending', 'queued', 'prechecking', 'running', 'verifying', 'succeeded', 'failed', 'canceled', 'skipped'));
