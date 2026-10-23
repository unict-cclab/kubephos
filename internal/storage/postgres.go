package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kubephos.dev/kubephos/internal/domain"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ready(ctx context.Context) error {
	var present bool
	err := s.pool.QueryRow(ctx, `SELECT to_regclass('public.operations') IS NOT NULL`).Scan(&present)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("database schema is not installed")
	}
	return nil
}

func (s *Store) Stats(ctx context.Context) (domain.DashboardStats, error) {
	var stats domain.DashboardStats
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM workspaces),
			(SELECT count(*) FROM operations WHERE status IN ('queued', 'prechecking', 'running', 'verifying')),
			(SELECT count(*) FROM operations WHERE status = 'ready'),
			(SELECT count(*) FROM operations WHERE status = 'failed')
	`).Scan(&stats.Workspaces, &stats.ActiveOperations, &stats.ReadyOperations, &stats.FailedOperations)
	return stats, err
}

func scanOperation(row scanner) (domain.Operation, error) {
	var operation domain.Operation
	var planRaw []byte
	var validationRaw []byte
	err := row.Scan(
		&operation.ID,
		&operation.WorkspaceID,
		&operation.PluginID,
		&operation.PluginVersion,
		&operation.PluginDigest,
		&operation.Title,
		&operation.Status,
		&operation.Spec,
		&planRaw,
		&validationRaw,
		&operation.PlanHash,
		&operation.CancelRequested,
		&operation.Error,
		&operation.CreatedAt,
		&operation.QueuedAt,
		&operation.StartedAt,
		&operation.CompletedAt,
	)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := json.Unmarshal(planRaw, &operation.Plan); err != nil {
		return domain.Operation{}, err
	}
	if err := json.Unmarshal(validationRaw, &operation.Validation); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func duplicate(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func foreignKeyViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23503"
}

func WaitForDatabase(ctx context.Context, databaseURL string, timeout time.Duration) (*Store, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		store, err := Open(ctx, databaseURL)
		if err == nil {
			return store, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("database unavailable: %w", lastErr)
}

// Snapshots can contain complete Locust scripts and multiple configuration
// definitions. Fetch them on demand rather than duplicating every history poll.
func (s *Store) GetExperimentConfigurationSnapshot(ctx context.Context, experimentID string) (json.RawMessage, error) {
	var snapshot json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT configuration_snapshot FROM experiments WHERE id=$1`, experimentID).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return snapshot, err
}
