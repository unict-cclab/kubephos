package storage

import (
	"context"
	"encoding/json"
	"errors"

	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) SyncCatalogApplications(ctx context.Context, applications []domain.CatalogApplication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockModelCatalog(ctx, tx); err != nil {
		return err
	}

	for _, application := range applications {
		_, err := tx.Exec(ctx, `
   INSERT INTO catalog_applications (id, name, description, origin, descriptor, digest, enabled)
   VALUES ($1, $2, $3, 'built-in', $4, $5, true)
   ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description,
    descriptor = EXCLUDED.descriptor, digest = EXCLUDED.digest, updated_at = now()
   WHERE catalog_applications.origin = 'built-in' AND catalog_applications.digest <> EXCLUDED.digest
  `, application.ID, application.Name, application.Description, application.Descriptor, application.Digest)
		if err != nil {
			return err
		}
	}

	for _, application := range applications {
		if err := seedCatalogModels(ctx, tx, application.Descriptor); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) CreateCatalogApplication(ctx context.Context, application domain.CatalogApplication) (domain.CatalogApplication, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO catalog_applications (id, name, description, origin, descriptor, digest, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, true)
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, description = EXCLUDED.description, origin = EXCLUDED.origin,
            descriptor = EXCLUDED.descriptor, digest = EXCLUDED.digest, enabled = true, updated_at = now()
		WHERE catalog_applications.enabled = false
		RETURNING created_at, updated_at
	`, application.ID, application.Name, application.Description, application.Origin, application.Descriptor, application.Digest).Scan(&application.CreatedAt, &application.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CatalogApplication{}, ErrConflict
	}
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return domain.CatalogApplication{}, ErrConflict
		}
	}
	return application, err
}

func (s *Store) DeleteCatalogApplication(ctx context.Context, applicationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var reference string
	if err := tx.QueryRow(ctx, `
		SELECT 'app:' || id
		FROM catalog_applications
		WHERE id = $1 AND enabled = true
		FOR UPDATE
	`, applicationID).Scan(&reference); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experiment_configurations WHERE application_ref = $1)`, reference).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrConflict
	}
	command, err := tx.Exec(ctx, `UPDATE catalog_applications SET enabled = false, updated_at = now() WHERE id = $1 AND enabled = true`, applicationID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) GetCatalogApplicationDescriptor(ctx context.Context, applicationID string) (json.RawMessage, error) {
	var descriptor json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT descriptor
		FROM catalog_applications
		WHERE id = $1 AND enabled = true
	`, applicationID).Scan(&descriptor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return descriptor, err
}

func (s *Store) GetCatalogApplication(ctx context.Context, applicationID string) (domain.CatalogApplication, error) {
	var application domain.CatalogApplication
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, origin, descriptor, digest, enabled, created_at, updated_at
		FROM catalog_applications
		WHERE id = $1 AND enabled = true
	`, applicationID).Scan(&application.ID, &application.Name, &application.Description, &application.Origin, &application.Descriptor, &application.Digest, &application.Enabled, &application.CreatedAt, &application.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CatalogApplication{}, ErrNotFound
	}
	application.Reference = catalogReference(application.ID)
	return application, err
}

func (s *Store) GetCatalogApplicationPreview(ctx context.Context, applicationID, digest string) (json.RawMessage, error) {
	var content json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT content
		FROM catalog_application_previews
		WHERE application_id = $1 AND application_digest = $2
	`, applicationID, digest).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return content, err
}

func (s *Store) PutCatalogApplicationPreview(ctx context.Context, applicationID, digest string, content json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO catalog_application_previews (application_id, application_digest, content)
		VALUES ($1, $2, $3)
		ON CONFLICT (application_id) DO UPDATE
		SET application_digest = EXCLUDED.application_digest, content = EXCLUDED.content, updated_at = now()
	`, applicationID, digest, content)
	return err
}

func catalogReference(applicationID string) string {
	return "app:" + applicationID
}

func configurationConstraintConflict(err error) bool {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return false
	}
	return databaseError.Code == "23505" || databaseError.Code == "23503" || databaseError.Code == "23001" || databaseError.Code == "23514"
}

func (s *Store) CreateExperimentConfiguration(ctx context.Context, configuration domain.ExperimentConfiguration) (domain.ExperimentConfiguration, error) {
	validation, err := json.Marshal(configuration.Validation)
	if err != nil {
		return domain.ExperimentConfiguration{}, err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO experiment_configurations (id, workspace_id, cluster_resource_id, owner_user_id, name, description, application_ref, application_digest, definition, validation)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at, updated_at
	`, configuration.ID, configuration.WorkspaceID, configuration.ClusterResourceID, configuration.OwnerUserID, configuration.Name, configuration.Description, configuration.ApplicationRef, configuration.ApplicationDigest, configuration.Definition, validation).Scan(&configuration.CreatedAt, &configuration.UpdatedAt)
	if configurationConstraintConflict(err) {
		return domain.ExperimentConfiguration{}, ErrConflict
	}
	return configuration, err
}

func (s *Store) UpdateExperimentConfiguration(ctx context.Context, configuration domain.ExperimentConfiguration, expectedUpdatedAt time.Time) (domain.ExperimentConfiguration, error) {
	validation, err := json.Marshal(configuration.Validation)
	if err != nil {
		return domain.ExperimentConfiguration{}, err
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE experiment_configurations
		SET workspace_id = $2, cluster_resource_id = NULLIF($3, ''), name = $4, description = $5,
		    application_ref = $6, application_digest = $7, definition = $8, validation = $9, updated_at = now()
		WHERE id = $1 AND updated_at = $10
		RETURNING created_at, updated_at
	`, configuration.ID, configuration.WorkspaceID, configuration.ClusterResourceID, configuration.Name, configuration.Description, configuration.ApplicationRef, configuration.ApplicationDigest, configuration.Definition, validation, expectedUpdatedAt).Scan(&configuration.CreatedAt, &configuration.UpdatedAt)
	if configurationConstraintConflict(err) {
		return domain.ExperimentConfiguration{}, ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if existsErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experiment_configurations WHERE id = $1)`, configuration.ID).Scan(&exists); existsErr != nil {
			return domain.ExperimentConfiguration{}, existsErr
		}
		if !exists {
			return domain.ExperimentConfiguration{}, ErrNotFound
		}
		return domain.ExperimentConfiguration{}, ErrConflict
	}
	return configuration, err
}

func (s *Store) ListExperimentConfigurations(ctx context.Context) ([]domain.ExperimentConfiguration, error) {
	return s.ListExperimentConfigurationsForUser(ctx, "")
}

func (s *Store) ListExperimentConfigurationsForUser(ctx context.Context, userID string) ([]domain.ExperimentConfiguration, error) {
	return s.readListExperimentConfigurationsForUser(ctx, userID, false)
}

func (s *Store) ListExperimentConfigurationsVisibleToUser(ctx context.Context, userID string) ([]domain.ExperimentConfiguration, error) {
	return s.readListExperimentConfigurationsForUser(ctx, userID, true)
}

func (s *Store) readListExperimentConfigurationsForUser(ctx context.Context, userID string, shared bool) ([]domain.ExperimentConfiguration, error) {
	visibility := "$1 = '' OR configuration.owner_user_id = $1"
	if shared {
		visibility = "$1 = '' OR can_view_configuration($1, configuration.id)"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT configuration.id, configuration.workspace_id, COALESCE(configuration.cluster_resource_id, ''), configuration.owner_user_id, owner.username,
		       configuration.name, configuration.description, configuration.application_ref, configuration.application_digest, configuration.definition, configuration.validation, configuration.created_at, configuration.updated_at
		FROM experiment_configurations configuration
		JOIN users owner ON owner.id = configuration.owner_user_id
		WHERE `+visibility+`
		ORDER BY configuration.updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ExperimentConfiguration{}
	for rows.Next() {
		configuration, err := scanExperimentConfiguration(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, configuration)
	}
	return result, rows.Err()
}

func (s *Store) GetExperimentConfiguration(ctx context.Context, configurationID string) (domain.ExperimentConfiguration, error) {
	configuration, err := scanExperimentConfiguration(s.pool.QueryRow(ctx, `
		SELECT configuration.id, configuration.workspace_id, COALESCE(configuration.cluster_resource_id, ''), configuration.owner_user_id, owner.username,
		       configuration.name, configuration.description, configuration.application_ref, configuration.application_digest, configuration.definition, configuration.validation, configuration.created_at, configuration.updated_at
		FROM experiment_configurations configuration
		JOIN users owner ON owner.id = configuration.owner_user_id
		WHERE configuration.id = $1
	`, configurationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExperimentConfiguration{}, ErrNotFound
	}
	return configuration, err
}

func (s *Store) DeleteExperimentConfiguration(ctx context.Context, configurationID string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM experiment_configurations WHERE id = $1`, configurationID)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && (databaseError.Code == "23503" || databaseError.Code == "23001") {
			return ErrConflict
		}
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClusterHasExperimentConfigurations(ctx context.Context, clusterResourceID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experiment_configurations WHERE cluster_resource_id = $1)`, clusterResourceID).Scan(&exists)
	return exists, err
}

func scanExperimentConfiguration(row scanner) (domain.ExperimentConfiguration, error) {
	var configuration domain.ExperimentConfiguration
	var validation []byte
	err := row.Scan(&configuration.ID, &configuration.WorkspaceID, &configuration.ClusterResourceID, &configuration.OwnerUserID, &configuration.OwnerUsername, &configuration.Name, &configuration.Description, &configuration.ApplicationRef, &configuration.ApplicationDigest, &configuration.Definition, &validation, &configuration.CreatedAt, &configuration.UpdatedAt)
	if err != nil {
		return domain.ExperimentConfiguration{}, err
	}
	configuration.Kind = "single"
	var definition struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(configuration.Definition, &definition); err == nil && definition.Kind == "suite" {
		configuration.Kind = "suite"
	}
	if err := json.Unmarshal(validation, &configuration.Validation); err != nil {
		return domain.ExperimentConfiguration{}, err
	}
	return configuration, nil
}

func (s *Store) ListCatalogApplications(ctx context.Context) ([]domain.CatalogApplication, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, origin, descriptor, digest, enabled, created_at, updated_at
		FROM catalog_applications
		WHERE enabled = true
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CatalogApplication{}
	for rows.Next() {
		var application domain.CatalogApplication
		if err := rows.Scan(&application.ID, &application.Name, &application.Description, &application.Origin, &application.Descriptor, &application.Digest, &application.Enabled, &application.CreatedAt, &application.UpdatedAt); err != nil {
			return nil, err
		}
		application.Reference = catalogReference(application.ID)
		result = append(result, application)
	}
	return result, rows.Err()
}
