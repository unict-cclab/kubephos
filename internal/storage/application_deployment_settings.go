package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

func lockApplicationDeployment(ctx context.Context, tx pgx.Tx, id, cluster string, expected time.Time) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('cluster:'||$1,0))`, cluster); err != nil {
		return err
	}
	var found string
	err := tx.QueryRow(ctx, `SELECT r.id FROM managed_resources r WHERE r.id=$1 AND r.kind='application-deployment'
 AND r.spec->>'clusterResourceId'=$2 AND r.updated_at=$3 AND r.deleted_at IS NULL
 AND r.deletion_operation_id IS NULL AND r.deletion_pipeline_run_id IS NULL
 AND r.recreation_pipeline_run_id IS NULL FOR UPDATE`, id, cluster, expected).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Persist the desired configuration before contacting Kubernetes. A failed or
// interrupted connection must not leave the control object ahead of the database.
// A session lock serializes successive placement edits across the network call.
func (s *Store) UpdateApplicationDeploymentSettings(ctx context.Context, id, cluster string, expected time.Time, spec json.RawMessage, apply func() error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := "application-placement:" + id
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
			_ = conn.Conn().Close(unlockCtx)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockApplicationDeployment(ctx, tx, id, cluster, expected); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE managed_resources SET spec=$2,updated_at=now() WHERE id=$1`, id, spec); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return apply()
}

func (s *Store) RetryApplicationDeployment(ctx context.Context, id, cluster string, expected time.Time, spec json.RawMessage, pipeline domain.Pipeline, run domain.PipelineRun) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockApplicationDeployment(ctx, tx, id, cluster, expected); err != nil {
		return err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT p.status FROM managed_resources r JOIN pipeline_runs p ON p.id=r.pipeline_run_id WHERE r.id=$1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "failed" && status != "canceled" {
		return ErrConflict
	}
	// The existing reservation stays attached to this resource. Update its saved
	// settings before switching run IDs so the namespace guard recognizes itself.
	if _, err = tx.Exec(ctx, `UPDATE managed_resources SET spec=$2 WHERE id=$1`, id, spec); err != nil {
		return err
	}
	if err = insertPipelineLifecycle(ctx, tx, &pipeline, &run); err != nil {
		return err
	}
	var version, digest string
	if stages := pipeline.Resolution.Stages; len(stages) > 0 {
		version, digest = stages[len(stages)-1].PluginVersion, stages[len(stages)-1].PluginDigest
	}
	if _, err = tx.Exec(ctx, `UPDATE managed_resources SET pipeline_id=$2,pipeline_run_id=$3,
 plugin_version=CASE WHEN $4<>'' THEN $4 ELSE plugin_version END,
 plugin_digest=CASE WHEN $4<>'' THEN $5 ELSE plugin_digest END,updated_at=now() WHERE id=$1`, id, pipeline.ID, run.ID, version, digest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
