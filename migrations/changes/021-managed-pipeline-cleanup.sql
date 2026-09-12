ALTER TABLE managed_resources
    ADD COLUMN deletion_pipeline_id text REFERENCES pipelines(id) ON DELETE RESTRICT,
    ADD COLUMN deletion_pipeline_run_id text REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
    ADD CONSTRAINT managed_resources_deletion_lifecycle CHECK (
        num_nonnulls(deletion_operation_id, deletion_pipeline_run_id) <= 1
        AND (deletion_pipeline_run_id IS NULL OR deletion_pipeline_id IS NOT NULL)
    );

CREATE INDEX managed_resources_deletion_pipeline_run_idx ON managed_resources(deletion_pipeline_run_id) WHERE deletion_pipeline_run_id IS NOT NULL;
