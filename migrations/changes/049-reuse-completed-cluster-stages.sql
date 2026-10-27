-- A managed-cluster retry may reference immutable, successful operations from
-- an earlier attempt. Keep those operations and their artifacts in the history.
ALTER TABLE pipeline_run_stages DROP CONSTRAINT pipeline_run_stages_operation_id_key;
CREATE UNIQUE INDEX pipeline_run_stages_run_operation_id_key ON pipeline_run_stages(run_id,operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX pipeline_run_stages_operation_id_idx ON pipeline_run_stages(operation_id) WHERE operation_id IS NOT NULL;
