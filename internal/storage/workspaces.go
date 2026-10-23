package storage

import (
	"context"

	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateWorkspace(ctx context.Context, workspace domain.Workspace) (domain.Workspace, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workspaces (id, name, description, status)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at
	`, workspace.ID, workspace.Name, workspace.Description, workspace.Status).Scan(&workspace.CreatedAt, &workspace.UpdatedAt)
	return workspace, err
}

func (s *Store) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, status, created_at, updated_at
		FROM workspaces
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Workspace{}
	for rows.Next() {
		var workspace domain.Workspace
		if err := rows.Scan(&workspace.ID, &workspace.Name, &workspace.Description, &workspace.Status, &workspace.CreatedAt, &workspace.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, workspace)
	}
	return result, rows.Err()
}

func (s *Store) GetWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	var workspace domain.Workspace
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, status, created_at, updated_at
		FROM workspaces
		WHERE id = $1
	`, workspaceID).Scan(&workspace.ID, &workspace.Name, &workspace.Description, &workspace.Status, &workspace.CreatedAt, &workspace.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Workspace{}, ErrNotFound
	}
	return workspace, err
}
