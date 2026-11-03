package storage

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func (s *Store) MarkPipelineInitialized(ctx context.Context, runID, owner string) error {
	result, err := s.pool.Exec(ctx, `UPDATE pipeline_runs SET initialized_at=COALESCE(initialized_at,now()) WHERE id=$1 AND lease_owner=$2 AND status='running' AND terminal_status IS NULL AND NOT cancel_requested AND NOT execution_requested AND pause_before_stage<>''`, runID, owner)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// The experiment row serializes double-clicks, cancellation and manual starts.
func (s *Store) StartExperimentTraffic(ctx context.Context, experimentID, runID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM experiments WHERE id=$1 FOR UPDATE`, experimentID).Scan(&status); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if status != "initialized" {
		return ErrConflict
	}
	result, err := tx.Exec(ctx, `UPDATE pipeline_runs r SET execution_requested=true WHERE r.id=$2 AND r.status='running' AND r.terminal_status IS NULL AND NOT r.cancel_requested AND r.initialized_at IS NOT NULL AND NOT r.execution_requested AND r.id IN (SELECT t.pipeline_run_id FROM experiment_trials t JOIN experiment_variants v ON v.id=t.variant_id WHERE v.experiment_id=$1)`, experimentID, runID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE experiment_trials t SET status='running',updated_at=now() FROM experiment_variants v,pipeline_runs r WHERE t.variant_id=v.id AND v.experiment_id=$1 AND r.id=t.pipeline_run_id AND r.status='running' AND r.execution_requested`, experimentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE experiments SET status='running',updated_at=now() WHERE id=$1`, experimentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
