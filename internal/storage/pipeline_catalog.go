package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreatePipeline(ctx context.Context, pipeline domain.Pipeline) (domain.Pipeline, error) {
	definition, err := json.Marshal(pipeline.Definition)
	if err != nil {
		return domain.Pipeline{}, err
	}
	resolution, err := json.Marshal(pipeline.Resolution)
	if err != nil {
		return domain.Pipeline{}, err
	}
	validation, err := json.Marshal(pipeline.Validation)
	if err != nil {
		return domain.Pipeline{}, err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO pipelines (id, workspace_id, name, description, definition, resolution, validation, pipeline_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at
	`, pipeline.ID, pipeline.WorkspaceID, pipeline.Name, pipeline.Description, definition, resolution, validation, pipeline.Hash).Scan(&pipeline.CreatedAt, &pipeline.UpdatedAt)
	if duplicate(err) {
		return domain.Pipeline{}, ErrConflict
	}
	return pipeline, err
}

func (s *Store) ListPipelines(ctx context.Context, limit int) ([]domain.Pipeline, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, name, description, definition, resolution, validation, pipeline_hash, created_at, updated_at
		FROM pipelines
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Pipeline{}
	for rows.Next() {
		pipeline, err := scanPipeline(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, pipeline)
	}
	return result, rows.Err()
}

func (s *Store) GetPipeline(ctx context.Context, pipelineID string) (domain.Pipeline, error) {
	pipeline, err := scanPipeline(s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, definition, resolution, validation, pipeline_hash, created_at, updated_at
		FROM pipelines
		WHERE id = $1
	`, pipelineID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Pipeline{}, ErrNotFound
	}
	return pipeline, err
}

func scanPipeline(row scanner) (domain.Pipeline, error) {
	var pipeline domain.Pipeline
	var definition, resolution, validation []byte
	err := row.Scan(&pipeline.ID, &pipeline.WorkspaceID, &pipeline.Name, &pipeline.Description, &definition, &resolution, &validation, &pipeline.Hash, &pipeline.CreatedAt, &pipeline.UpdatedAt)
	if err != nil {
		return domain.Pipeline{}, err
	}
	if err := json.Unmarshal(definition, &pipeline.Definition); err != nil {
		return domain.Pipeline{}, err
	}
	if err := json.Unmarshal(resolution, &pipeline.Resolution); err != nil {
		return domain.Pipeline{}, err
	}
	if err := json.Unmarshal(validation, &pipeline.Validation); err != nil {
		return domain.Pipeline{}, err
	}
	return pipeline, nil
}
