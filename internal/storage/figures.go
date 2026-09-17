package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

const figureColumns = `id, experiment_id, metric, format, source_artifact_ids, storage_key, digest, size_bytes, created_at`

func scanExperimentFigure(row pgx.Row) (domain.ExperimentFigure, error) {
	var figure domain.ExperimentFigure
	err := row.Scan(&figure.ID, &figure.ExperimentID, &figure.Metric, &figure.Format, &figure.SourceArtifactIDs, &figure.StorageKey, &figure.Digest, &figure.SizeBytes, &figure.CreatedAt)
	return figure, err
}

func (s *Store) EligibleExperimentFigureSources(ctx context.Context, experimentID string) (map[string]bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experiments WHERE id = $1)`, experimentID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
    SELECT DISTINCT artifact.id
    FROM experiment_trials trial
    JOIN experiment_variants variant ON variant.id = trial.variant_id
    JOIN artifacts artifact ON artifact.id = trial.result_artifact_id
    WHERE variant.experiment_id = $1 AND trial.status = 'succeeded'
      AND artifact.verified_at IS NOT NULL AND artifact.sensitive = false
  `, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (s *Store) CreateExperimentFigure(ctx context.Context, figure domain.ExperimentFigure) (domain.ExperimentFigure, error) {
	return scanExperimentFigure(s.pool.QueryRow(ctx, `
    INSERT INTO experiment_figures (id, experiment_id, metric, format, source_artifact_ids, storage_key, digest, size_bytes)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    RETURNING `+figureColumns,
		figure.ID, figure.ExperimentID, figure.Metric, figure.Format, figure.SourceArtifactIDs, figure.StorageKey, figure.Digest, figure.SizeBytes,
	))
}

func (s *Store) ListExperimentFigures(ctx context.Context, experimentID string) ([]domain.ExperimentFigure, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+figureColumns+` FROM experiment_figures WHERE experiment_id = $1 ORDER BY created_at DESC`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ExperimentFigure{}
	for rows.Next() {
		figure, err := scanExperimentFigure(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, figure)
	}
	return result, rows.Err()
}

func (s *Store) GetExperimentFigure(ctx context.Context, figureID string) (domain.ExperimentFigure, error) {
	figure, err := scanExperimentFigure(s.pool.QueryRow(ctx, `SELECT `+figureColumns+` FROM experiment_figures WHERE id = $1`, figureID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExperimentFigure{}, ErrNotFound
	}
	return figure, err
}
