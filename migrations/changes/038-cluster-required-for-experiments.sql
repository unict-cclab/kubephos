WITH unique_ready_cluster AS (
    SELECT resource.workspace_id, min(resource.id) AS cluster_resource_id
    FROM managed_resources resource
    JOIN pipeline_runs run ON run.id = resource.pipeline_run_id
    WHERE resource.kind = 'kubernetes-cluster'
      AND resource.deleted_at IS NULL
      AND run.status = 'succeeded'
    GROUP BY resource.workspace_id
    HAVING count(*) = 1
)
UPDATE experiment_configurations configuration
SET cluster_resource_id = candidate.cluster_resource_id,
    updated_at = now()
FROM unique_ready_cluster candidate
WHERE configuration.cluster_resource_id IS NULL
  AND configuration.workspace_id = candidate.workspace_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM experiment_configurations
        WHERE cluster_resource_id IS NULL
    ) THEN
        RAISE EXCEPTION 'experiment configurations without a cluster must be assigned explicitly before migration 038';
    END IF;
END
$$;

ALTER TABLE experiment_configurations
    ALTER COLUMN cluster_resource_id SET NOT NULL;
