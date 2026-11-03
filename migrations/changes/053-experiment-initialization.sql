ALTER TABLE pipeline_runs
    ADD COLUMN application_namespace text NOT NULL DEFAULT '',
    ADD COLUMN pause_before_stage text NOT NULL DEFAULT '',
    ADD COLUMN execution_requested boolean NOT NULL DEFAULT false,
    ADD COLUMN initialized_at timestamptz;

CREATE UNIQUE INDEX pipeline_runs_application_namespace_active
    ON pipeline_runs(cluster_resource_id, application_namespace)
    WHERE status='running' AND cluster_resource_id IS NOT NULL AND application_namespace<>'';

ALTER TABLE experiments DROP CONSTRAINT experiments_status_check;
ALTER TABLE experiments ADD CONSTRAINT experiments_status_check
    CHECK (status IN ('queued','initializing','initialized','running','succeeded','failed','canceled'));
ALTER TABLE experiment_trials DROP CONSTRAINT experiment_trials_status_check;
ALTER TABLE experiment_trials ADD CONSTRAINT experiment_trials_status_check
    CHECK (status IN ('queued','initializing','initialized','running','succeeded','failed','canceled'));
