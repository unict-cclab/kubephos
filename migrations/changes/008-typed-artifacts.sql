ALTER TABLE artifacts
    ADD COLUMN output_name text,
    ADD COLUMN artifact_type text NOT NULL DEFAULT 'RunResult',
    ADD COLUMN artifact_version text NOT NULL DEFAULT 'v1alpha1',
    ADD COLUMN verified_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX artifacts_step_output_idx
    ON artifacts(operation_id, step_id, output_name)
    WHERE step_id IS NOT NULL AND output_name IS NOT NULL;

CREATE INDEX artifacts_type_version_idx
    ON artifacts(artifact_type, artifact_version);
