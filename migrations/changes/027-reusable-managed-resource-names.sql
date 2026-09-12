ALTER TABLE managed_resources
    ADD COLUMN deleted_at timestamptz;

UPDATE managed_resources resource
SET deleted_at = COALESCE(deletion.completed_at, now())
FROM operations deletion
WHERE resource.deletion_operation_id = deletion.id
  AND deletion.status = 'succeeded';

UPDATE managed_resources resource
SET deleted_at = COALESCE(deletion.completed_at, now())
FROM pipeline_runs deletion
WHERE resource.deletion_pipeline_run_id = deletion.id
  AND deletion.status = 'succeeded';

ALTER TABLE managed_resources
    DROP CONSTRAINT managed_resources_kind_provider_connection_id_name_key;

CREATE UNIQUE INDEX managed_resources_active_name_key
    ON managed_resources(kind, provider, connection_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX managed_resources_deleted_at_idx
    ON managed_resources(deleted_at)
    WHERE deleted_at IS NOT NULL;
