ALTER TABLE experiments DROP CONSTRAINT experiments_status_check;
ALTER TABLE experiments ADD CONSTRAINT experiments_status_check CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled'));

ALTER TABLE experiment_variants ADD COLUMN pipeline_id text REFERENCES pipelines(id) ON DELETE RESTRICT;
ALTER TABLE experiment_variants ADD COLUMN pipeline_hash text;

ALTER TABLE experiment_trials ALTER COLUMN operation_id DROP NOT NULL;
ALTER TABLE experiment_trials ALTER COLUMN result_artifact_id DROP NOT NULL;
ALTER TABLE experiment_trials ALTER COLUMN completed_at DROP NOT NULL;
ALTER TABLE experiment_trials DROP CONSTRAINT experiment_trials_status_check;
ALTER TABLE experiment_trials ADD CONSTRAINT experiment_trials_status_check CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled'));
ALTER TABLE experiment_trials ADD COLUMN pipeline_run_id text UNIQUE REFERENCES pipeline_runs(id) ON DELETE RESTRICT;
ALTER TABLE experiment_trials ADD COLUMN error text NOT NULL DEFAULT '';
ALTER TABLE experiment_trials ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX experiment_trials_pipeline_run_idx ON experiment_trials(pipeline_run_id);
