ALTER TABLE infrastructure_resources
    RENAME COLUMN provider_plugin_id TO provider;

UPDATE infrastructure_resources
SET provider = 'proxmox'
WHERE provider = 'io.kubephos.infrastructure.proxmox.discovery';

ALTER TABLE infrastructure_resources
    ADD COLUMN created_by_operation_id text REFERENCES operations(id) ON DELETE RESTRICT;

CREATE INDEX infrastructure_resources_operation_idx ON infrastructure_resources(created_by_operation_id);
