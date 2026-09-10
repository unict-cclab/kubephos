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

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateInitialUser(ctx context.Context, user domain.User) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(457213684)`); err != nil {
		return domain.User{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return domain.User{}, err
	}
	if exists {
		return domain.User{}, fmt.Errorf("%w: initial administrator already exists", ErrConflict)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (id, username, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at
	`, user.ID, user.Username, user.PasswordHash, user.Role).Scan(&user.CreatedAt, &user.UpdatedAt); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	var user domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, created_at, updated_at
		FROM users
		WHERE username = $1
	`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_sessions (token_hash, user_id, csrf_token, expires_at)
		VALUES ($1, $2, $3, $4)
	`, session.TokenHash, session.User.ID, session.CSRFToken, session.ExpiresAt)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (domain.Session, error) {
	var session domain.Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.token_hash, s.csrf_token, s.expires_at,
		       u.id, u.username, u.role, u.created_at, u.updated_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()
	`, tokenHash).Scan(&session.TokenHash, &session.CSRFToken, &session.ExpiresAt, &session.User.ID, &session.User.Username, &session.User.Role, &session.User.CreatedAt, &session.User.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err == nil {
		_, _ = s.pool.Exec(ctx, `UPDATE auth_sessions SET last_seen_at = now() WHERE token_hash = $1 AND last_seen_at < now() - interval '5 minutes'`, tokenHash)
	}
	return session, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at <= now()`)
	return err
}

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

func (s *Store) CreateCompletedExperiment(ctx context.Context, experiment domain.Experiment) (domain.Experiment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Experiment{}, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `
		INSERT INTO experiments (id, workspace_id, name, description, status, result_type, result_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
	`, experiment.ID, experiment.WorkspaceID, experiment.Name, experiment.Description, experiment.Status, experiment.ResultType, experiment.ResultVersion).Scan(&experiment.CreatedAt, &experiment.UpdatedAt); err != nil {
		return domain.Experiment{}, err
	}
	for variantIndex := range experiment.Variants {
		variant := &experiment.Variants[variantIndex]
		if len(variant.Configuration) == 0 {
			variant.Configuration = json.RawMessage(`{}`)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO experiment_variants (id, experiment_id, position, name, configuration)
			VALUES ($1, $2, $3, $4, $5)
		`, variant.ID, experiment.ID, variant.Position, variant.Name, variant.Configuration); err != nil {
			return domain.Experiment{}, err
		}
		for trialIndex := range variant.Trials {
			trial := &variant.Trials[trialIndex]
			var workspaceID, operationID, operationStatus, artifactType, artifactVersion string
			var sensitive bool
			var completedAt *time.Time
			err := tx.QueryRow(ctx, `
				SELECT o.workspace_id, o.id, o.status, o.completed_at,
				       a.artifact_type, a.artifact_version, a.sensitive
				FROM artifacts a
				JOIN operations o ON o.id = a.operation_id
				WHERE a.id = $1
				FOR SHARE OF a, o
			`, trial.ResultArtifactID).Scan(&workspaceID, &operationID, &operationStatus, &completedAt, &artifactType, &artifactVersion, &sensitive)
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.Experiment{}, fmt.Errorf("%w: result artifact %s", ErrNotFound, trial.ResultArtifactID)
			}
			if err != nil {
				return domain.Experiment{}, err
			}
			if workspaceID != experiment.WorkspaceID || operationStatus != domain.OperationSucceeded || completedAt == nil || artifactType != experiment.ResultType || artifactVersion != experiment.ResultVersion || sensitive {
				return domain.Experiment{}, fmt.Errorf("%w: result artifact %s is not an eligible completed dataset", ErrConflict, trial.ResultArtifactID)
			}
			trial.OperationID = operationID
			completed := completedAt.UTC()
			trial.CompletedAt = &completed
			if err := tx.QueryRow(ctx, `
				INSERT INTO experiment_trials (id, variant_id, position, status, operation_id, result_artifact_id, completed_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING created_at
			`, trial.ID, variant.ID, trial.Position, trial.Status, trial.OperationID, trial.ResultArtifactID, trial.CompletedAt).Scan(&trial.CreatedAt); err != nil {
				return domain.Experiment{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Experiment{}, err
	}
	return experiment, nil
}

func (s *Store) ListExperiments(ctx context.Context, limit int) ([]domain.Experiment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, name, description, status, result_type, result_version, scheduled_for, created_at, updated_at
		FROM experiments
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	result := []domain.Experiment{}
	for rows.Next() {
		var experiment domain.Experiment
		if err := rows.Scan(&experiment.ID, &experiment.WorkspaceID, &experiment.Name, &experiment.Description, &experiment.Status, &experiment.ResultType, &experiment.ResultVersion, &experiment.ScheduledFor, &experiment.CreatedAt, &experiment.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, experiment)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range result {
		variants, err := s.listExperimentVariants(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Variants = variants
	}
	return result, nil
}

func (s *Store) listExperimentVariants(ctx context.Context, experimentID string) ([]domain.ExperimentVariant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, experiment_id, position, name, COALESCE(pipeline_id, ''), COALESCE(pipeline_hash, ''), configuration
		FROM experiment_variants
		WHERE experiment_id = $1
		ORDER BY position
	`, experimentID)
	if err != nil {
		return nil, err
	}
	variants := []domain.ExperimentVariant{}
	for rows.Next() {
		var variant domain.ExperimentVariant
		if err := rows.Scan(&variant.ID, &variant.ExperimentID, &variant.Position, &variant.Name, &variant.PipelineID, &variant.PipelineHash, &variant.Configuration); err != nil {
			rows.Close()
			return nil, err
		}
		variants = append(variants, variant)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range variants {
		trials, err := s.listExperimentTrials(ctx, variants[index].ID)
		if err != nil {
			return nil, err
		}
		variants[index].Trials = trials
	}
	return variants, nil
}

func (s *Store) listExperimentTrials(ctx context.Context, variantID string) ([]domain.ExperimentTrial, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, variant_id, position, status, COALESCE(operation_id, ''), COALESCE(pipeline_run_id, ''), COALESCE(result_artifact_id, ''), error, created_at, completed_at
		FROM experiment_trials
		WHERE variant_id = $1
		ORDER BY position
	`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trials := []domain.ExperimentTrial{}
	for rows.Next() {
		var trial domain.ExperimentTrial
		if err := rows.Scan(&trial.ID, &trial.VariantID, &trial.Position, &trial.Status, &trial.OperationID, &trial.PipelineRunID, &trial.ResultArtifactID, &trial.Error, &trial.CreatedAt, &trial.CompletedAt); err != nil {
			return nil, err
		}
		trials = append(trials, trial)
	}
	return trials, rows.Err()
}

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
		INSERT INTO provider_connections (id, name, provider, plugin_id, configuration)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at
	`, connection.ID, connection.Name, connection.Provider, connection.PluginID, connection.Configuration).Scan(&connection.CreatedAt, &connection.UpdatedAt)
	return connection, err
}

func (s *Store) ListProviderConnections(ctx context.Context) ([]domain.ProviderConnection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, provider, plugin_id, created_at, updated_at
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
		if err := rows.Scan(&connection.ID, &connection.Name, &connection.Provider, &connection.PluginID, &connection.CreatedAt, &connection.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, connection)
	}
	return result, rows.Err()
}

func (s *Store) GetProviderConnectionConfiguration(ctx context.Context, connectionID string) (domain.ProviderConnection, error) {
	var connection domain.ProviderConnection
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, provider, plugin_id, configuration, created_at, updated_at
		FROM provider_connections
		WHERE id = $1
	`, connectionID).Scan(&connection.ID, &connection.Name, &connection.Provider, &connection.PluginID, &connection.Configuration, &connection.CreatedAt, &connection.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderConnection{}, ErrNotFound
	}
	return connection, err
}

func (s *Store) SyncCatalogApplications(ctx context.Context, applications []domain.CatalogApplication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, application := range applications {
		command, err := tx.Exec(ctx, `
			INSERT INTO catalog_applications (id, version, name, description, origin, descriptor, digest, enabled)
			VALUES ($1, $2, $3, $4, 'built-in', $5, $6, true)
			ON CONFLICT (id, version) DO NOTHING
		`, application.ID, application.Version, application.Name, application.Description, application.Descriptor, application.Digest)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 1 {
			continue
		}
		var digest string
		if err := tx.QueryRow(ctx, `SELECT digest FROM catalog_applications WHERE id = $1 AND version = $2`, application.ID, application.Version).Scan(&digest); err != nil {
			return err
		}
		if digest != application.Digest {
			return fmt.Errorf("%w: application %s version %s already has a different digest", ErrConflict, application.ID, application.Version)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateCatalogApplication(ctx context.Context, application domain.CatalogApplication) (domain.CatalogApplication, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO catalog_applications (id, version, name, description, origin, descriptor, digest, enabled)
		VALUES ($1, $2, $3, $4, 'imported', $5, $6, true)
		RETURNING created_at, updated_at
	`, application.ID, application.Version, application.Name, application.Description, application.Descriptor, application.Digest).Scan(&application.CreatedAt, &application.UpdatedAt)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return domain.CatalogApplication{}, ErrConflict
		}
	}
	return application, err
}

func (s *Store) GetCatalogApplicationDescriptor(ctx context.Context, applicationID, version string) (json.RawMessage, error) {
	var descriptor json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT descriptor
		FROM catalog_applications
		WHERE id = $1 AND version = $2 AND enabled = true
	`, applicationID, version).Scan(&descriptor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return descriptor, err
}

func (s *Store) ListCatalogApplications(ctx context.Context) ([]domain.CatalogApplication, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, version, description, origin, descriptor, digest, enabled, created_at, updated_at
		FROM catalog_applications
		WHERE enabled = true
		ORDER BY name, version DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CatalogApplication{}
	for rows.Next() {
		var application domain.CatalogApplication
		if err := rows.Scan(&application.ID, &application.Name, &application.Version, &application.Description, &application.Origin, &application.Descriptor, &application.Digest, &application.Enabled, &application.CreatedAt, &application.UpdatedAt); err != nil {
			return nil, err
		}
		application.Reference = "app:" + application.ID + "@" + application.Version
		result = append(result, application)
	}
	return result, rows.Err()
}

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

func (s *Store) ReserveManagedResource(ctx context.Context, resource domain.InfrastructureResource, operationID string) error {
	_, err := s.ReserveManagedResources(ctx, []domain.InfrastructureResource{resource}, operationID)
	return err
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
	var ownership string
	var protection string
	var storedWorkspace string
	err := s.pool.QueryRow(ctx, `
		SELECT ownership, protection, COALESCE(workspace_id, '')
		FROM infrastructure_resources
		WHERE provider = $1 AND external_id = $2 AND state <> 'deleted'
	`, provider, externalID).Scan(&ownership, &protection, &storedWorkspace)
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

func (s *Store) AppendAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	details := event.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor, action, target_type, target_id, outcome, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.Actor, event.Action, event.TargetType, event.TargetID, event.Outcome, details)
	return err
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, actor, action, target_type, target_id, outcome, details, created_at
		FROM audit_events
		ORDER BY sequence DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AuditEvent{}
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.Sequence, &event.Actor, &event.Action, &event.TargetType, &event.TargetID, &event.Outcome, &event.Details, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) CreateOperation(ctx context.Context, operation domain.Operation) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback(ctx)
	plan, err := json.Marshal(operation.Plan)
	if err != nil {
		return domain.Operation{}, err
	}
	validation, err := json.Marshal(operation.Validation)
	if err != nil {
		return domain.Operation{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO operations (id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at
	`, operation.ID, operation.WorkspaceID, operation.PluginID, operation.PluginVersion, operation.PluginDigest, operation.Title, operation.Status, operation.Spec, plan, validation, operation.PlanHash).Scan(&operation.CreatedAt)
	if err != nil {
		return domain.Operation{}, err
	}
	for position, planned := range operation.Plan.Steps {
		step := domain.OperationStep{
			ID:          operation.ID + "_" + planned.ID,
			OperationID: operation.ID,
			Position:    position + 1,
			Name:        planned.Name,
			Status:      domain.StepPending,
			Input:       planned.Input,
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO operation_steps (id, operation_id, position, name, status, input)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, step.ID, step.OperationID, step.Position, step.Name, step.Status, step.Input)
		if err != nil {
			return domain.Operation{}, err
		}
		operation.Steps = append(operation.Steps, step)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, level, source, message)
		VALUES ($1, 'info', 'validation', $2)
	`, operation.ID, fmt.Sprintf("Validation passed. Plan %s is ready for confirmation.", operation.PlanHash[:12]))
	if err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

func (s *Store) QueueOperation(ctx context.Context, operationID, planHash string, acceptWarnings bool) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback(ctx)
	var validationRaw []byte
	var storedHash string
	err = tx.QueryRow(ctx, `
		SELECT validation, plan_hash
		FROM operations
		WHERE id = $1 AND status = $2
		FOR UPDATE
	`, operationID, domain.OperationReady).Scan(&validationRaw, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, ErrConflict
	}
	if err != nil {
		return domain.Operation{}, err
	}
	if storedHash != planHash {
		return domain.Operation{}, fmt.Errorf("%w: validated plan has changed", ErrConflict)
	}
	var validation domain.ValidationReport
	if err := json.Unmarshal(validationRaw, &validation); err != nil {
		return domain.Operation{}, err
	}
	if !validation.Valid {
		return domain.Operation{}, fmt.Errorf("%w: validation is not valid", ErrConflict)
	}
	if !acceptWarnings {
		for _, issue := range validation.Issues {
			if issue.Level == "warning" {
				return domain.Operation{}, fmt.Errorf("%w: warnings require confirmation", ErrConflict)
			}
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = $2, queued_at = now()
		WHERE id = $1 AND status = $3
	`, operationID, domain.OperationQueued, domain.OperationReady)
	if err != nil {
		return domain.Operation{}, err
	}
	if command.RowsAffected() != 1 {
		return domain.Operation{}, ErrConflict
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, level, source, message)
		VALUES ($1, 'info', 'queue', 'Validated plan confirmed and queued.')
	`, operationID)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, err
	}
	return s.GetOperation(ctx, operationID)
}

func (s *Store) ListOperations(ctx context.Context, limit int) ([]domain.Operation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash,
		       cancel_requested, error, created_at, queued_at, started_at, completed_at
		FROM operations
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Operation{}
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, operation)
	}
	return result, rows.Err()
}

func (s *Store) GetOperation(ctx context.Context, operationID string) (domain.Operation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash,
		       cancel_requested, error, created_at, queued_at, started_at, completed_at
		FROM operations
		WHERE id = $1
	`, operationID)
	operation, err := scanOperation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, ErrNotFound
	}
	if err != nil {
		return domain.Operation{}, err
	}
	operation.Steps, err = s.ListSteps(ctx, operationID)
	if err != nil {
		return domain.Operation{}, err
	}
	operation.Artifacts, err = s.ListArtifacts(ctx, operationID)
	return operation, err
}

func (s *Store) ListSteps(ctx context.Context, operationID string) ([]domain.OperationStep, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, operation_id, position, name, status, input, result, health, error, started_at, completed_at
		FROM operation_steps
		WHERE operation_id = $1
		ORDER BY position
	`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.OperationStep{}
	for rows.Next() {
		var step domain.OperationStep
		if err := rows.Scan(&step.ID, &step.OperationID, &step.Position, &step.Name, &step.Status, &step.Input, &step.Result, &step.Health, &step.Error, &step.StartedAt, &step.CompletedAt); err != nil {
			return nil, err
		}
		result = append(result, step)
	}
	return result, rows.Err()
}

func (s *Store) ClaimOperation(ctx context.Context, owner string) (domain.Operation, bool, error) {
	row := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM operations
			WHERE status = $1 AND cancel_requested = false
			ORDER BY queued_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE operations AS operation
		SET status = $2, lease_owner = $3, lease_until = now() + interval '30 seconds', started_at = COALESCE(started_at, now())
		FROM candidate
		WHERE operation.id = candidate.id
		RETURNING operation.id, operation.workspace_id, operation.plugin_id, operation.plugin_version, operation.plugin_digest, operation.title, operation.status,
		          operation.spec, operation.plan, operation.validation, operation.plan_hash, operation.cancel_requested,
		          operation.error, operation.created_at, operation.queued_at, operation.started_at, operation.completed_at
	`, domain.OperationQueued, domain.OperationPrechecking, owner)
	operation, err := scanOperation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, err
	}
	operation.Steps, err = s.ListSteps(ctx, operation.ID)
	return operation, true, err
}

func (s *Store) SetOperationStatus(ctx context.Context, operationID, status, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE operations SET status = $2, lease_until = now() + interval '30 seconds' WHERE id = $1`, operationID, status)
	if err != nil {
		return err
	}
	if message != "" {
		return s.AppendLog(ctx, operationID, "", "info", "engine", message)
	}
	return nil
}

func (s *Store) SetStepState(ctx context.Context, operationID, stepID, status string, result, health json.RawMessage, failure string) error {
	completed := status == domain.StepSucceeded || status == domain.StepFailed || status == domain.StepCanceled
	_, err := s.pool.Exec(ctx, `
		UPDATE operation_steps
		SET status = $3,
		    result = COALESCE($4, result),
		    health = COALESCE($5, health),
		    error = $6,
		    started_at = CASE WHEN $3 IN ('prechecking', 'running') THEN COALESCE(started_at, now()) ELSE started_at END,
		    completed_at = CASE WHEN $7 THEN now() ELSE completed_at END
		WHERE operation_id = $1 AND id = $2
	`, operationID, stepID, status, nullableJSON(result), nullableJSON(health), failure, completed)
	return err
}

func (s *Store) CompleteOperation(ctx context.Context, operationID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE operations
		SET status = $2, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1
	`, operationID, domain.OperationSucceeded)
	if err != nil {
		return err
	}
	return s.AppendLog(ctx, operationID, "", "info", "engine", "Operation completed after all health gates passed.")
}

func (s *Store) FailOperation(ctx context.Context, operationID, status, failure string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE operations
		SET status = $2, error = $3, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1
	`, operationID, status, failure)
	if err != nil {
		return err
	}
	level := "error"
	if status == domain.OperationCanceled {
		level = "warning"
	}
	return s.AppendLog(ctx, operationID, "", level, "engine", failure)
}

func (s *Store) RequestCancel(ctx context.Context, operationID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE operations
		SET cancel_requested = true,
		    status = CASE WHEN status IN ('ready', 'queued') THEN $2 ELSE status END,
		    completed_at = CASE WHEN status IN ('ready', 'queued') THEN now() ELSE completed_at END
		WHERE id = $1 AND status NOT IN ('succeeded', 'failed', 'canceled')
	`, operationID, domain.OperationCanceled)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	return s.AppendLog(ctx, operationID, "", "warning", "user", "Cancellation requested.")
}

func (s *Store) OperationCancelRequested(ctx context.Context, operationID string) (bool, error) {
	var requested bool
	err := s.pool.QueryRow(ctx, `SELECT cancel_requested FROM operations WHERE id = $1`, operationID).Scan(&requested)
	return requested, err
}

func (s *Store) RenewLease(ctx context.Context, operationID, owner string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE operations
		SET lease_until = now() + interval '30 seconds'
		WHERE id = $1 AND lease_owner = $2 AND status IN ('prechecking', 'running', 'verifying')
	`, operationID, owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailExpiredOperations(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		WITH expired AS (
			UPDATE operations
			SET status = 'failed',
			    error = 'Worker lease expired. Review logs and state before retrying.',
			    completed_at = now(),
			    lease_owner = NULL,
			    lease_until = NULL
			WHERE status IN ('prechecking', 'running', 'verifying') AND lease_until < now()
			RETURNING id
		), interrupted_steps AS (
			UPDATE operation_steps
			SET status = 'failed',
			    completed_at = now(),
			    error = 'Worker lease expired before the step completed.'
			WHERE operation_id IN (SELECT id FROM expired)
			  AND status IN ('prechecking', 'running', 'verifying')
			RETURNING operation_id
		)
		INSERT INTO operation_logs (operation_id, level, source, message)
		SELECT id, 'error', 'worker', 'Worker lease expired. Automatic retry was blocked because the step outcome is unknown.'
		FROM expired
	`)
	return err
}

func (s *Store) AppendLog(ctx context.Context, operationID, stepID, level, source, message string) error {
	var value any
	if stepID != "" {
		value = stepID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, step_id, level, source, message)
		VALUES ($1, $2, $3, $4, $5)
	`, operationID, value, level, source, message)
	return err
}

func (s *Store) ListLogs(ctx context.Context, operationID string, after int64, limit int) ([]domain.LogEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, operation_id, COALESCE(step_id, ''), level, source, message, created_at
		FROM operation_logs
		WHERE operation_id = $1 AND sequence > $2
		ORDER BY sequence
		LIMIT $3
	`, operationID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.LogEntry{}
	for rows.Next() {
		var entry domain.LogEntry
		if err := rows.Scan(&entry.Sequence, &entry.OperationID, &entry.StepID, &entry.Level, &entry.Source, &entry.Message, &entry.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
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

func (s *Store) CreateArtifact(ctx context.Context, artifact domain.Artifact) (domain.Artifact, error) {
	if artifact.StorageDigest == "" {
		artifact.StorageDigest = artifact.Digest
	}
	if artifact.StoredSizeBytes == 0 && artifact.SizeBytes > 0 {
		artifact.StoredSizeBytes = artifact.SizeBytes
	}
	var stepID any
	if artifact.StepID != "" {
		stepID = artifact.StepID
	}
	var outputName any
	if artifact.OutputName != "" {
		outputName = artifact.OutputName
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO artifacts (id, operation_id, step_id, output_name, name, artifact_type, artifact_version, media_type, storage_key, digest, size_bytes, storage_digest, stored_size_bytes, encryption_nonce, sensitive)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, verified_at
	`, artifact.ID, artifact.OperationID, stepID, outputName, artifact.Name, artifact.Type, artifact.Version, artifact.MediaType, artifact.StorageKey, artifact.Digest, artifact.SizeBytes, artifact.StorageDigest, artifact.StoredSizeBytes, artifact.EncryptionNonce, artifact.Sensitive).Scan(&artifact.CreatedAt, &artifact.VerifiedAt)
	return artifact, err
}

func (s *Store) ListArtifacts(ctx context.Context, operationID string) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, operation_id, COALESCE(step_id, ''), COALESCE(output_name, ''), name, artifact_type, artifact_version, media_type, storage_key, digest, size_bytes, storage_digest, stored_size_bytes, encryption_nonce, sensitive, verified_at, created_at
		FROM artifacts
		WHERE operation_id = $1
		ORDER BY created_at
	`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Artifact{}
	for rows.Next() {
		var artifact domain.Artifact
		if err := rows.Scan(&artifact.ID, &artifact.OperationID, &artifact.StepID, &artifact.OutputName, &artifact.Name, &artifact.Type, &artifact.Version, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.StorageDigest, &artifact.StoredSizeBytes, &artifact.EncryptionNonce, &artifact.Sensitive, &artifact.VerifiedAt, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (s *Store) ListAvailableArtifacts(ctx context.Context, limit int) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.operation_id, o.workspace_id, COALESCE(a.step_id, ''), COALESCE(a.output_name, ''),
		       a.name, a.artifact_type, a.artifact_version, a.media_type, a.storage_key, a.digest,
		       a.size_bytes, a.storage_digest, a.stored_size_bytes, a.encryption_nonce,
		       a.sensitive, a.verified_at, a.created_at
		FROM artifacts a
		JOIN operations o ON o.id = a.operation_id
		WHERE o.status = $1 AND a.verified_at IS NOT NULL
		ORDER BY a.created_at DESC
		LIMIT $2
	`, domain.OperationSucceeded, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Artifact{}
	for rows.Next() {
		var artifact domain.Artifact
		if err := rows.Scan(&artifact.ID, &artifact.OperationID, &artifact.WorkspaceID, &artifact.StepID, &artifact.OutputName, &artifact.Name, &artifact.Type, &artifact.Version, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.StorageDigest, &artifact.StoredSizeBytes, &artifact.EncryptionNonce, &artifact.Sensitive, &artifact.VerifiedAt, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (domain.Artifact, error) {
	var artifact domain.Artifact
	err := s.pool.QueryRow(ctx, `
		SELECT id, operation_id, COALESCE(step_id, ''), COALESCE(output_name, ''), name, artifact_type, artifact_version, media_type, storage_key, digest, size_bytes, storage_digest, stored_size_bytes, encryption_nonce, sensitive, verified_at, created_at
		FROM artifacts
		WHERE id = $1
	`, artifactID).Scan(&artifact.ID, &artifact.OperationID, &artifact.StepID, &artifact.OutputName, &artifact.Name, &artifact.Type, &artifact.Version, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.StorageDigest, &artifact.StoredSizeBytes, &artifact.EncryptionNonce, &artifact.Sensitive, &artifact.VerifiedAt, &artifact.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Artifact{}, ErrNotFound
	}
	return artifact, err
}

func (s *Store) GetArtifactByOutput(ctx context.Context, operationID, stepID, outputName string) (domain.Artifact, error) {
	var artifact domain.Artifact
	err := s.pool.QueryRow(ctx, `
		SELECT id, operation_id, COALESCE(step_id, ''), COALESCE(output_name, ''), name, artifact_type, artifact_version, media_type, storage_key, digest, size_bytes, storage_digest, stored_size_bytes, encryption_nonce, sensitive, verified_at, created_at
		FROM artifacts
		WHERE operation_id = $1 AND step_id = $2 AND output_name = $3
	`, operationID, stepID, outputName).Scan(&artifact.ID, &artifact.OperationID, &artifact.StepID, &artifact.OutputName, &artifact.Name, &artifact.Type, &artifact.Version, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.StorageDigest, &artifact.StoredSizeBytes, &artifact.EncryptionNonce, &artifact.Sensitive, &artifact.VerifiedAt, &artifact.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Artifact{}, ErrNotFound
	}
	return artifact, err
}

type scanner interface {
	Scan(...any) error
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
