package storage

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Runs imported into another experiment and externally referenced artifacts are
// shared data. Everything exclusively owned by the deleted experiment is removed.
func deleteExperimentRuns(ctx context.Context, tx pgx.Tx, requested []string) error {
	rows, err := tx.Query(ctx, `SELECT id, status FROM pipeline_runs WHERE id=ANY($1::text[]) AND NOT EXISTS (SELECT 1 FROM experiment_trials WHERE pipeline_run_id=pipeline_runs.id) FOR UPDATE`, requested)
	if err != nil {
		return err
	}
	runs := []string{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			rows.Close()
			return err
		}
		if !deletableExperimentStatus(status) {
			rows.Close()
			return ErrConflict
		}
		runs = append(runs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var pipelines, operations []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(DISTINCT pipeline_id),'{}') FROM pipeline_runs WHERE id=ANY($1::text[])`, runs).Scan(&pipelines); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `WITH RECURSIVE owned(id) AS (
 SELECT operation_id FROM pipeline_run_stages WHERE run_id=ANY($1::text[]) AND operation_id IS NOT NULL
 UNION SELECT o.id FROM owned parent JOIN operations source ON source.id=parent.id JOIN operations o ON o.spec->>'sourceOperationId'=parent.id OR (o.plugin_id=source.plugin_id AND o.spec=source.spec AND o.plan->'steps'->0->>'cleanup'='true')
 ) SELECT COALESCE(array_agg(DISTINCT id),'{}') FROM (
 SELECT id FROM owned UNION SELECT cleanup_operation_id FROM pipeline_run_stages WHERE run_id=ANY($1::text[]) AND cleanup_operation_id IS NOT NULL
 ) candidates`, runs).Scan(&operations); err != nil {
		return err
	}
	// Only terminal history can be removed; a still-running cleanup must finish.
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE id=ANY($1::text[]) AND status NOT IN ('succeeded','failed','canceled'))`, operations).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pipeline_runs WHERE id=ANY($1::text[])`, runs); err != nil {
		return err
	}
	// Remove generated snapshots only. User-created pipeline definitions remain.
	if _, err := tx.Exec(ctx, `DELETE FROM pipelines p WHERE id=ANY($1::text[]) AND (description LIKE 'Immutable execution pipeline for %' OR description LIKE 'Immutable suite pipeline for %') AND NOT EXISTS(SELECT 1 FROM pipeline_runs WHERE pipeline_id=p.id) AND NOT EXISTS(SELECT 1 FROM experiment_variants WHERE pipeline_id=p.id)`, pipelines); err != nil {
		return err
	}
	// Retain operations which supply data to other resources or experiments.
	for {
		previous := len(operations)
		if err := tx.QueryRow(ctx, `WITH operation_inputs AS MATERIALIZED (
 SELECT DISTINCT input #>> '{}' AS artifact_id FROM operations consumer
 CROSS JOIN LATERAL jsonb_path_query(consumer.plan,'$.steps[*].artifactInputs[*].artifactId') input
 WHERE consumer.id<>ALL($1::text[])
 ), pipeline_inputs AS MATERIALIZED (
 SELECT DISTINCT input #>> '{}' AS artifact_id FROM pipelines consumer
 CROSS JOIN LATERAL jsonb_path_query(consumer.definition,'$.** ? (@.type() == "string")') input
 ) SELECT COALESCE(array_agg(o.id),'{}') FROM operations o WHERE o.id=ANY($1::text[])
 AND NOT EXISTS(SELECT 1 FROM pipeline_run_stages WHERE operation_id=o.id OR cleanup_operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM experiment_trials t LEFT JOIN artifacts a ON a.id=t.result_artifact_id WHERE t.operation_id=o.id OR a.operation_id=o.id)
	AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN pipeline_runs r ON r.result_artifact_id=a.id WHERE a.operation_id=o.id)
	AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN experiment_figures f ON a.id=ANY(f.source_artifact_ids) WHERE a.operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM infrastructure_resources WHERE created_by_operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM managed_resources WHERE operation_id=o.id OR deletion_operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM strategy_catalog WHERE operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN plugin_runtime_profiles p ON p.endpoint_artifact_id=a.id OR p.credential_artifact_id=a.id WHERE a.operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN plugin_runtime_registries p ON p.endpoint_artifact_id=a.id OR p.credential_artifact_id=a.id WHERE a.operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN operation_inputs consumer ON consumer.artifact_id=a.id WHERE a.operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM artifacts a JOIN pipeline_inputs consumer ON consumer.artifact_id=a.id WHERE a.operation_id=o.id)`, operations).Scan(&operations); err != nil {
			return err
		}
		if len(operations) == previous {
			break
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO artifact_deletions(storage_key) SELECT '/kubephos/operations/'||unnest($1::text[])||'/' ON CONFLICT DO NOTHING`, operations); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM artifacts WHERE operation_id=ANY($1::text[])`, operations); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM operations WHERE id=ANY($1::text[])`, operations); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE target_id=ANY($1::text[]) OR target_id=ANY($2::text[]) OR target_id=ANY($3::text[])`, runs, operations, pipelines); err != nil {
		return err
	}
	return nil
}

// PurgeOrphanedExperimentHistory applies the same deletion to snapshots whose
// experiment was deleted by earlier versions, without touching infrastructure.
func (s *Store) PurgeOrphanedExperimentHistory(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var runs []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(r.id),'{}') FROM pipeline_runs r JOIN pipelines p ON p.id=r.pipeline_id WHERE r.status IN ('succeeded','failed','canceled') AND (p.description LIKE 'Immutable execution pipeline for %' OR p.description LIKE 'Immutable suite pipeline for %') AND NOT EXISTS(SELECT 1 FROM experiment_trials WHERE pipeline_run_id=r.id)`).Scan(&runs); err != nil {
		return err
	}
	if err := deleteExperimentRuns(ctx, tx, runs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
