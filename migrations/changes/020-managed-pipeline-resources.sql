ALTER TABLE managed_resources
    ALTER COLUMN operation_id DROP NOT NULL,
    ADD COLUMN pipeline_id text REFERENCES pipelines(id) ON DELETE RESTRICT,
    ADD COLUMN pipeline_run_id text REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
    ADD CONSTRAINT managed_resources_creation_lifecycle CHECK (
        num_nonnulls(operation_id, pipeline_run_id) = 1
        AND (pipeline_run_id IS NULL OR pipeline_id IS NOT NULL)
    );

CREATE INDEX managed_resources_pipeline_run_idx ON managed_resources(pipeline_run_id) WHERE pipeline_run_id IS NOT NULL;
