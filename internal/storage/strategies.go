package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateCatalogStrategy(ctx context.Context, strategy domain.CatalogStrategy) (domain.CatalogStrategy, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO strategy_catalog (id, workspace_id, name, kind, source_image, default_configuration)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, strategy.ID, strategy.WorkspaceID, strategy.Name, strategy.Kind, strategy.SourceImage, strategy.DefaultConfiguration).Scan(&strategy.CreatedAt, &strategy.UpdatedAt)
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	strategy.Status = domain.OperationSucceeded
	return strategy, nil
}

func (s *Store) ListCatalogStrategies(ctx context.Context) ([]domain.CatalogStrategy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT strategy.id, strategy.workspace_id, COALESCE(strategy.harbor_resource_id, ''), strategy.name, strategy.kind,
		       strategy.source_image, strategy.default_configuration, COALESCE(strategy.operation_id, ''),
		       COALESCE(artifact.id, ''), COALESCE(operation.status, 'succeeded'), COALESCE(operation.error, ''), strategy.created_at, strategy.updated_at
		FROM strategy_catalog strategy
		LEFT JOIN operations operation ON operation.id = strategy.operation_id
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
		SELECT strategy.id, strategy.workspace_id, COALESCE(strategy.harbor_resource_id, ''), strategy.name, strategy.kind,
		       strategy.source_image, strategy.default_configuration, COALESCE(strategy.operation_id, ''),
		       COALESCE(artifact.id, ''), COALESCE(operation.status, 'succeeded'), COALESCE(operation.error, ''), strategy.created_at, strategy.updated_at
		FROM strategy_catalog strategy
		LEFT JOIN operations operation ON operation.id = strategy.operation_id
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
	if err != nil {
		return domain.CatalogStrategy{}, err
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
		SELECT COALESCE(operation.status, 'succeeded') FROM strategy_catalog strategy
		LEFT JOIN operations operation ON operation.id = strategy.operation_id
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
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	return strategy, err
}

// UpdateCatalogStrategy atomically updates settings and experiment configurations that reference it.
// Executed experiment snapshots are left unchanged.
func (s *Store) UpdateCatalogStrategy(ctx context.Context, strategy domain.CatalogStrategy) (domain.CatalogStrategy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	defer tx.Rollback(ctx)
	var previousImage string
	var previousConfiguration json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT source_image, default_configuration FROM strategy_catalog WHERE id = $1 FOR UPDATE`, strategy.ID).Scan(&previousImage, &previousConfiguration); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.CatalogStrategy{}, ErrNotFound
		}
		return domain.CatalogStrategy{}, err
	}
	err = tx.QueryRow(ctx, `
 UPDATE strategy_catalog strategy
 SET source_image = $2, name = $3, default_configuration = $4,
 operation_id = CASE WHEN source_image <> $2 THEN NULL ELSE operation_id END,
 harbor_resource_id = CASE WHEN source_image <> $2 THEN NULL ELSE harbor_resource_id END,
 updated_at = now()
 WHERE strategy.id = $1 AND NOT EXISTS (
  SELECT 1 FROM operations WHERE id = strategy.operation_id AND status NOT IN ('succeeded', 'failed', 'canceled')
 ) RETURNING created_at, updated_at
 `, strategy.ID, strategy.SourceImage, strategy.Name, strategy.DefaultConfiguration).Scan(&strategy.CreatedAt, &strategy.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CatalogStrategy{}, ErrConflict
	}
	strategy.Status = domain.OperationSucceeded
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	_, err = tx.Exec(ctx, synchronizeStrategyConfigurationsSQL, strategy.ID, strategy.DefaultConfiguration, strategy.SourceImage != previousImage, strategy.SourceImage, strategy.WorkspaceID, previousConfiguration)
	if err != nil {
		return domain.CatalogStrategy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CatalogStrategy{}, err
	}
	return s.GetCatalogStrategy(ctx, strategy.ID)
}

const synchronizeStrategyConfigurationsSQL = `
 UPDATE experiment_configurations experiment
 SET definition = jsonb_set(definition, '{components}', (
  SELECT jsonb_agg(
   CASE WHEN component->>'strategyId' = $1 THEN
    jsonb_set(component, '{configuration}',
     (COALESCE(component->'configuration', '{}'::jsonb) -
      ARRAY(SELECT key FROM jsonb_object_keys($6::jsonb) key WHERE NOT $2::jsonb ? key)) ||
     COALESCE((SELECT jsonb_object_agg(defaults.key, defaults.value)
      FROM jsonb_each($2::jsonb) defaults
      WHERE NOT COALESCE(component->'configuration', '{}'::jsonb) ? defaults.key), '{}'::jsonb) ||
     CASE WHEN $3 THEN jsonb_build_object('image', $4::text) ELSE '{}'::jsonb END)
   ELSE component END ORDER BY position)
  FROM jsonb_array_elements(definition->'components') WITH ORDINALITY AS items(component, position)
 )), updated_at = now()
 WHERE workspace_id = $5 AND definition @> jsonb_build_object(
  'components', jsonb_build_array(jsonb_build_object('strategyId', $1::text)))
`
