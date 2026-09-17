package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateCatalogStrategy(ctx context.Context, strategy domain.CatalogStrategy, operation domain.Operation) (domain.CatalogStrategy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	defer tx.Rollback(ctx)
	operation.Status = domain.OperationQueued
	if err := insertOperation(ctx, tx, &operation); err != nil {
		return domain.CatalogStrategy{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO strategy_catalog (id, workspace_id, harbor_resource_id, name, kind, source_image, default_configuration, operation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at
	`, strategy.ID, strategy.WorkspaceID, strategy.HarborResourceID, strategy.Name, strategy.Kind, strategy.SourceImage, strategy.DefaultConfiguration, operation.ID).Scan(&strategy.CreatedAt, &strategy.UpdatedAt)
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CatalogStrategy{}, err
	}
	strategy.OperationID = operation.ID
	strategy.Status = domain.OperationQueued
	return strategy, nil
}

func (s *Store) ListCatalogStrategies(ctx context.Context) ([]domain.CatalogStrategy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT strategy.id, strategy.workspace_id, strategy.harbor_resource_id, strategy.name, strategy.kind,
		       strategy.source_image, strategy.default_configuration, strategy.operation_id,
		       COALESCE(artifact.id, ''), operation.status, operation.error, strategy.created_at, strategy.updated_at
		FROM strategy_catalog strategy
		JOIN operations operation ON operation.id = strategy.operation_id
		LEFT JOIN LATERAL (
			SELECT id FROM artifacts
			WHERE operation_id = operation.id AND output_name = 'strategy-image' AND verified_at IS NOT NULL
			ORDER BY created_at DESC LIMIT 1
		) artifact ON true
		ORDER BY strategy.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CatalogStrategy{}
	for rows.Next() {
		strategy, err := scanCatalogStrategy(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, strategy)
	}
	return result, rows.Err()
}

func (s *Store) GetCatalogStrategy(ctx context.Context, strategyID string) (domain.CatalogStrategy, error) {
	strategy, err := scanCatalogStrategy(s.pool.QueryRow(ctx, `
		SELECT strategy.id, strategy.workspace_id, strategy.harbor_resource_id, strategy.name, strategy.kind,
		       strategy.source_image, strategy.default_configuration, strategy.operation_id,
		       COALESCE(artifact.id, ''), operation.status, operation.error, strategy.created_at, strategy.updated_at
		FROM strategy_catalog strategy
		JOIN operations operation ON operation.id = strategy.operation_id
		LEFT JOIN LATERAL (
			SELECT id FROM artifacts
			WHERE operation_id = operation.id AND output_name = 'strategy-image' AND verified_at IS NOT NULL
			ORDER BY created_at DESC LIMIT 1
		) artifact ON true
		WHERE strategy.id = $1
	`, strategyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CatalogStrategy{}, ErrNotFound
	}
	return strategy, err
}

func (s *Store) DeleteCatalogStrategy(ctx context.Context, strategyID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `
		SELECT operation.status FROM strategy_catalog strategy
		JOIN operations operation ON operation.id = strategy.operation_id
		WHERE strategy.id = $1 FOR UPDATE OF strategy
	`, strategyID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !deletableCatalogStrategyStatus(status) {
		return ErrConflict
	}
	var inUse bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM experiment_configurations
			WHERE definition @> jsonb_build_object('components', jsonb_build_array(jsonb_build_object('strategyId', $1::text)))
		)
	`, strategyID).Scan(&inUse)
	if err != nil {
		return err
	}
	if inUse {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM strategy_catalog WHERE id = $1`, strategyID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func deletableCatalogStrategyStatus(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

func scanCatalogStrategy(row pgx.Row) (domain.CatalogStrategy, error) {
	var strategy domain.CatalogStrategy
	err := row.Scan(&strategy.ID, &strategy.WorkspaceID, &strategy.HarborResourceID, &strategy.Name, &strategy.Kind,
		&strategy.SourceImage, &strategy.DefaultConfiguration, &strategy.OperationID, &strategy.ArtifactID,
		&strategy.Status, &strategy.Error, &strategy.CreatedAt, &strategy.UpdatedAt)
	return strategy, err
}
