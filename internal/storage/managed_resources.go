package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) SyncDiscoveredResources(ctx context.Context, resources []domain.InfrastructureResource) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, resource := range resources {
		var workspaceID any
		if resource.WorkspaceID != "" {
			workspaceID = resource.WorkspaceID
		}
		metadata := resource.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO infrastructure_resources (
				id, provider, external_id, workspace_id, kind, name, state,
				ownership, protection, metadata, last_seen_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'imported', 'read-only', $8, now())
			ON CONFLICT (provider, external_id) DO UPDATE
			SET kind = EXCLUDED.kind,
			    name = EXCLUDED.name,
			    state = EXCLUDED.state,
			    metadata = EXCLUDED.metadata,
			    last_seen_at = now(),
			    updated_at = now()
		`, resource.ID, resource.Provider, resource.ExternalID, workspaceID, resource.Kind, resource.Name, resource.State, metadata)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListInfrastructureResources(ctx context.Context) ([]domain.InfrastructureResource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider, external_id, COALESCE(workspace_id, ''), kind, name, state,
		       ownership, protection, metadata, last_seen_at, created_at, updated_at
		FROM infrastructure_resources
		ORDER BY ownership, kind, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.InfrastructureResource{}
	for rows.Next() {
		var resource domain.InfrastructureResource
		if err := rows.Scan(&resource.ID, &resource.Provider, &resource.ExternalID, &resource.WorkspaceID, &resource.Kind, &resource.Name, &resource.State, &resource.Ownership, &resource.Protection, &resource.Metadata, &resource.LastSeenAt, &resource.CreatedAt, &resource.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, resource)
	}
	return result, rows.Err()
}

func (s *Store) ReserveManagedResources(ctx context.Context, resources []domain.InfrastructureResource, operationID string) (map[string]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	reserved := make(map[string]string, len(resources))
	for _, resource := range resources {
		command, err := tx.Exec(ctx, `
		INSERT INTO infrastructure_resources (
			id, provider, external_id, workspace_id, kind, name, state,
			ownership, protection, metadata, created_by_operation_id, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'reserved', 'managed', 'managed', '{}', $7, now())
		ON CONFLICT (provider, external_id) DO UPDATE
		SET workspace_id = EXCLUDED.workspace_id,
		    kind = EXCLUDED.kind,
		    name = EXCLUDED.name,
		    state = 'reserved',
		    metadata = '{}',
		    created_by_operation_id = EXCLUDED.created_by_operation_id,
		    last_seen_at = now(),
		    updated_at = now()
		WHERE infrastructure_resources.ownership = 'managed'
		  AND infrastructure_resources.protection = 'managed'
		  AND infrastructure_resources.state IN ('deleted', 'cleaned')
	`, resource.ID, resource.Provider, resource.ExternalID, resource.WorkspaceID, resource.Kind, resource.Name, operationID)
		if err != nil {
			return nil, err
		}
		if command.RowsAffected() == 1 {
			reserved[resource.ExternalID] = resource.ID
			continue
		}
		var resourceID string
		var owner string
		err = tx.QueryRow(ctx, `
		SELECT id, COALESCE(created_by_operation_id, '')
		FROM infrastructure_resources
		WHERE provider = $1 AND external_id = $2
	`, resource.Provider, resource.ExternalID).Scan(&resourceID, &owner)
		if err != nil {
			return nil, err
		}
		if owner != operationID {
			return nil, fmt.Errorf("%w: resource %s already exists or is protected", ErrConflict, resource.ExternalID)
		}
		reserved[resource.ExternalID] = resourceID
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return reserved, nil
}

func (s *Store) ReleaseManagedResourceReservations(ctx context.Context, resources []domain.InfrastructureResource, operationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, resource := range resources {
		if _, err := tx.Exec(ctx, `
			UPDATE infrastructure_resources
			SET state = 'cleaned', updated_at = now(), last_seen_at = now()
			WHERE provider = $1 AND external_id = $2
			  AND created_by_operation_id = $3 AND state = 'reserved'
		`, resource.Provider, resource.ExternalID, operationID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) EnsureResourceAvailable(ctx context.Context, provider, externalID string) error {
	var ownership string
	var protection string
	var state string
	err := s.pool.QueryRow(ctx, `
		SELECT ownership, protection, state
		FROM infrastructure_resources
		WHERE provider = $1 AND external_id = $2
	`, provider, externalID).Scan(&ownership, &protection, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ownership == "managed" && protection == "managed" && (state == "deleted" || state == "cleaned") {
		return nil
	}
	return fmt.Errorf("%w: resource %s already exists or is protected", ErrConflict, externalID)
}

func (s *Store) SetOperationManagedResourcesState(ctx context.Context, operationID, state string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE infrastructure_resources
		SET state = $2, updated_at = now(), last_seen_at = now()
		WHERE created_by_operation_id = $1
		  AND ownership = 'managed'
		  AND ($2 <> 'failed' OR state NOT IN ('cleaned', 'deleted'))
	`, operationID, state)
	return err
}

func (s *Store) AuthorizeManagedResource(ctx context.Context, provider, externalID, workspaceID string) error {
	return s.authorizeManagedResource(ctx, provider, externalID, workspaceID, false)
}

// Cleanup may revisit an already deleted resource. The provider must still
// verify its ownership marker before touching any VM that is present.
func (s *Store) AuthorizeManagedCleanup(ctx context.Context, provider, externalID, workspaceID string) error {
	return s.authorizeManagedResource(ctx, provider, externalID, workspaceID, true)
}
func (s *Store) authorizeManagedResource(ctx context.Context, provider, externalID, workspaceID string, allowDeleted bool) error {
	var ownership string
	var protection string
	var storedWorkspace string
	err := s.pool.QueryRow(ctx, `
		SELECT ownership, protection, COALESCE(workspace_id, '')
		FROM infrastructure_resources
		WHERE provider = $1 AND external_id = $2 AND (state <> 'deleted' OR $3)
	`, provider, externalID, allowDeleted).Scan(&ownership, &protection, &storedWorkspace)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: managed resource %s was not found", ErrConflict, externalID)
	}
	if err != nil {
		return err
	}
	if ownership != "managed" || protection != "managed" || storedWorkspace != workspaceID {
		return fmt.Errorf("%w: resource %s is imported, protected or belongs to another workspace", ErrConflict, externalID)
	}
	return nil
}

func (s *Store) CompleteManagedResource(ctx context.Context, operationID, provider string, resource domain.DiscoveredResource) error {
	metadata := resource.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE infrastructure_resources
		SET name = $4, kind = $5, state = $6, metadata = $7, updated_at = now(), last_seen_at = now()
		WHERE created_by_operation_id = $1
		  AND provider = $2
		  AND external_id = $3
		  AND ownership = 'managed'
		  AND protection = 'managed'
	`, operationID, provider, resource.ExternalID, resource.Name, resource.Kind, resource.State, metadata)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: managed resource %s is not reserved by this operation", ErrConflict, resource.ExternalID)
	}
	return nil
}

func (s *Store) SetManagedResourceState(ctx context.Context, provider, externalID, workspaceID, state string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE infrastructure_resources
		SET state = $4, updated_at = now(), last_seen_at = now()
		WHERE provider = $1
		  AND external_id = $2
		  AND workspace_id = $3
		  AND ownership = 'managed'
		  AND protection = 'managed'
	`, provider, externalID, workspaceID, state)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: managed resource %s cannot change state", ErrConflict, externalID)
	}
	return nil
}

func (s *Store) UpdateManagedResource(ctx context.Context, provider, workspaceID string, resource domain.DiscoveredResource) error {
	result, err := s.pool.Exec(ctx, `UPDATE infrastructure_resources SET name=$4,kind=$5,state=$6,metadata=$7,updated_at=now(),last_seen_at=now() WHERE provider=$1 AND external_id=$2 AND workspace_id=$3 AND ownership='managed' AND protection='managed' AND state<>'deleted'`, provider, resource.ExternalID, workspaceID, resource.Name, resource.Kind, resource.State, resource.Metadata)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
