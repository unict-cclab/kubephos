package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// Retry only cleanup, never the workload or experiment. Recovering an old run
// is permitted only while its cluster has no other active run.
func (s *Store) RetryExperimentCleanup(ctx context.Context, runID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var cluster string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(cluster_resource_id,'') FROM pipeline_runs WHERE id=$1`, runID).Scan(&cluster); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if cluster != "" {
		if _, err := tx.Exec(ctx, `SELECT id FROM managed_resources WHERE id=$1 FOR UPDATE`, cluster); err != nil {
			return err
		}
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&status); err != nil {
		return err
	}
	if !deletableExperimentStatus(status) {
		return ErrConflict
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_runs WHERE cluster_resource_id=NULLIF($1,'') AND status IN ('queued','running')) OR EXISTS(SELECT 1 FROM operations o JOIN pipeline_run_stages s ON s.operation_id=o.id OR s.cleanup_operation_id=o.id WHERE s.run_id=$2 AND o.status NOT IN ('succeeded','failed','canceled'))`, cluster, runID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return ErrConflict
	}
	command, err := tx.Exec(ctx, `UPDATE pipeline_run_stages SET cleanup_status='pending',cleanup_operation_id=NULL,cleanup_error='',cleanup_started_at=NULL,cleanup_completed_at=NULL WHERE run_id=$1 AND cleanup_status IN ('failed','canceled')`, runID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	if status == "succeeded" {
		status = "failed"
	}
	if _, err := tx.Exec(ctx, `UPDATE pipeline_runs SET terminal_status=$2,status='running',lease_owner=NULL,lease_until=NULL WHERE id=$1`, runID, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
