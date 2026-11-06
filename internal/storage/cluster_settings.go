package storage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// Serialize metadata edits against deletion and topology maintenance. Experiments
// are intentionally not part of this guard: they may continue using the cluster.
// Keep the original provisioning lifecycle and artifacts intact.
func (s *Store) UpdateClusterSettings(ctx context.Context, id string, expected time.Time, spec json.RawMessage, apply func() error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked string
	err = tx.QueryRow(ctx, `SELECT r.id FROM managed_resources r JOIN pipeline_runs p ON p.id=r.pipeline_run_id
 WHERE r.id=$1 AND r.kind='kubernetes-cluster' AND r.updated_at=$2
 AND r.deleted_at IS NULL AND r.deletion_operation_id IS NULL AND r.deletion_pipeline_run_id IS NULL
 AND r.recreation_pipeline_run_id IS NULL AND p.status='succeeded' FOR UPDATE OF r`, id, expected).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = apply(); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE managed_resources SET spec=$2,updated_at=now() WHERE id=$1`, id, spec)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
