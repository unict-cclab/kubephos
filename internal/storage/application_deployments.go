package storage

import "context"

func (s *Store) CheckApplicationNamespaceAvailable(ctx context.Context, clusterID, namespace string) error {
	var occupied bool
	err := s.pool.QueryRow(ctx, `SELECT application_namespace_reserved($1,$2,'') OR EXISTS(SELECT 1 FROM pipeline_runs WHERE cluster_resource_id=$1 AND application_namespace=$2 AND status='running')`, clusterID, namespace).Scan(&occupied)
	if err != nil {
		return err
	}
	if occupied {
		return ErrConflict
	}
	return nil
}
func (s *Store) ApplicationNamespaceReserved(ctx context.Context, clusterID, namespace string) (bool, error) {
	var reserved bool
	err := s.pool.QueryRow(ctx, `SELECT application_namespace_reserved($1,$2,'')`, clusterID, namespace).Scan(&reserved)
	return reserved, err
}

// Dismiss only a failed lifecycle with no mutable step eligible for cleanup.
func (s *Store) DismissFailedApplicationDeployment(ctx context.Context, resourceID string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM managed_resources resource USING pipeline_runs run
 WHERE resource.id=$1 AND resource.kind='application-deployment' AND resource.pipeline_run_id=run.id
 AND run.status IN ('failed','canceled') AND resource.deletion_pipeline_run_id IS NULL
 AND NOT EXISTS (
  SELECT 1 FROM pipeline_run_stages stage JOIN operations op ON op.id=stage.operation_id
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(op.plan->'steps','[]'::jsonb)) WITH ORDINALITY AS planned(step,position)
  JOIN operation_steps step ON step.operation_id=op.id AND step.position=planned.position
  WHERE stage.run_id=run.id AND COALESCE((planned.step->>'mutating')::boolean,false)
  AND step.status IN ('succeeded','failed','canceled')
 )`, resourceID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
