-- The plugin-local identity (qemu/VMID) is unique within a Proxmox cluster,
-- not across the independent connections managed by one KubePhos instance.
-- Preserve resource row IDs, provenance, plans and immutable machine artifacts.
WITH source AS (
    SELECT r.id, COALESCE(NULLIF(o.spec->>'connectionRef',''),
                          NULLIF(r.metadata->>'connectionRef','')) AS connection_ref
    FROM infrastructure_resources r
    LEFT JOIN operations o ON o.id=r.created_by_operation_id
    WHERE r.provider='proxmox'
)
UPDATE infrastructure_resources r
SET external_id=s.connection_ref || '/' || r.external_id
FROM source s
WHERE r.id=s.id AND s.connection_ref ~ '^[A-Za-z0-9_-]+$'
  AND left(r.external_id,length(s.connection_ref)+1) <> s.connection_ref || '/';
-- Legacy discovery rows without known connection provenance remain read-only
-- and unscoped. They cannot authorize changes to a scoped managed resource.
