package storage

import (
	"context"

	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateCredential(ctx context.Context, credential domain.EncryptedCredential) (domain.Credential, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO credentials (id, name, kind, fingerprint, nonce, ciphertext)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, credential.ID, credential.Name, credential.Kind, credential.Fingerprint, credential.Nonce, credential.Ciphertext).Scan(&credential.CreatedAt, &credential.UpdatedAt)
	return credential.Credential, err
}

func (s *Store) ListCredentials(ctx context.Context) ([]domain.Credential, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, kind, fingerprint, created_at, updated_at
		FROM credentials
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Credential{}
	for rows.Next() {
		var credential domain.Credential
		if err := rows.Scan(&credential.ID, &credential.Name, &credential.Kind, &credential.Fingerprint, &credential.CreatedAt, &credential.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, credential)
	}
	return result, rows.Err()
}

func (s *Store) GetEncryptedCredential(ctx context.Context, credentialID string) (domain.EncryptedCredential, error) {
	var credential domain.EncryptedCredential
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, kind, fingerprint, nonce, ciphertext, created_at, updated_at
		FROM credentials
		WHERE id = $1
	`, credentialID).Scan(&credential.ID, &credential.Name, &credential.Kind, &credential.Fingerprint, &credential.Nonce, &credential.Ciphertext, &credential.CreatedAt, &credential.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EncryptedCredential{}, ErrNotFound
	}
	return credential, err
}

func (s *Store) CreateProviderConnection(ctx context.Context, connection domain.ProviderConnection) (domain.ProviderConnection, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO provider_connections (id, name, description, provider, plugin_id, configuration)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, connection.ID, connection.Name, connection.Description, connection.Provider, connection.PluginID, connection.Configuration).Scan(&connection.CreatedAt, &connection.UpdatedAt)
	return connection, err
}

func (s *Store) ListProviderConnections(ctx context.Context) ([]domain.ProviderConnection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, provider, plugin_id, created_at, updated_at
		FROM provider_connections
		ORDER BY provider, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ProviderConnection{}
	for rows.Next() {
		var connection domain.ProviderConnection
		if err := rows.Scan(&connection.ID, &connection.Name, &connection.Description, &connection.Provider, &connection.PluginID, &connection.CreatedAt, &connection.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, connection)
	}
	return result, rows.Err()
}

func (s *Store) GetProviderConnectionConfiguration(ctx context.Context, connectionID string) (domain.ProviderConnection, error) {
	var connection domain.ProviderConnection
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, provider, plugin_id, configuration, created_at, updated_at
		FROM provider_connections
		WHERE id = $1
	`, connectionID).Scan(&connection.ID, &connection.Name, &connection.Description, &connection.Provider, &connection.PluginID, &connection.Configuration, &connection.CreatedAt, &connection.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderConnection{}, ErrNotFound
	}
	return connection, err
}

func (s *Store) DeleteProviderConnection(ctx context.Context, connectionID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	err = tx.QueryRow(ctx, `SELECT id FROM provider_connections WHERE id = $1 FOR UPDATE`, connectionID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var exists bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM managed_resources resource
			LEFT JOIN operations deletion ON deletion.id = resource.deletion_operation_id
			LEFT JOIN pipeline_runs deletion_run ON deletion_run.id = resource.deletion_pipeline_run_id
			WHERE resource.connection_id = $1
			  AND COALESCE(deletion.status, deletion_run.status, '') <> $2
		)
	`, connectionID, domain.OperationSucceeded).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return ErrConflict
	}
	command, err := tx.Exec(ctx, `DELETE FROM provider_connections WHERE id = $1`, connectionID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
