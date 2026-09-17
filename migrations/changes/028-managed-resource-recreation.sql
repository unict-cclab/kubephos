ALTER TABLE managed_resources
    ADD COLUMN recreation_pipeline_id text REFERENCES pipelines(id) ON DELETE RESTRICT,
    ADD COLUMN recreation_pipeline_run_id text REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
    ADD CONSTRAINT managed_resources_recreation_lifecycle CHECK (
        (recreation_pipeline_id IS NULL) = (recreation_pipeline_run_id IS NULL)
    );

CREATE INDEX managed_resources_recreation_pipeline_run_idx
    ON managed_resources(recreation_pipeline_run_id)
    WHERE recreation_pipeline_run_id IS NOT NULL;
