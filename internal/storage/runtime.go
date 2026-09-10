package storage

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) GetPluginRuntimeProfile(ctx context.Context) (domain.PluginRuntimeProfile, error) {
	var profile domain.PluginRuntimeProfile
	rows, err := s.pool.Query(ctx, `
		SELECT p.endpoint_artifact_id, p.credential_artifact_id, p.created_at, p.updated_at,
		       r.endpoint_artifact_id, r.credential_artifact_id
		FROM plugin_runtime_profiles p
		LEFT JOIN plugin_runtime_registries r ON r.profile_id = p.id
		WHERE p.id = 'default'
		ORDER BY r.position
	`)
	if err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var endpoint, credential pgtype.Text
		if err := rows.Scan(&profile.EndpointArtifactID, &profile.CredentialArtifactID, &profile.CreatedAt, &profile.UpdatedAt, &endpoint, &credential); err != nil {
			return domain.PluginRuntimeProfile{}, err
		}
		found = true
		if endpoint.Valid && credential.Valid {
			profile.Registries = append(profile.Registries, domain.PluginRuntimeRegistry{EndpointArtifactID: endpoint.String, CredentialArtifactID: credential.String})
		}
	}
	if err := rows.Err(); err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	if !found {
		return domain.PluginRuntimeProfile{}, ErrNotFound
	}
	return profile, nil
}

func (s *Store) SavePluginRuntimeProfile(ctx context.Context, profile domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(172903417)`); err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO plugin_runtime_profiles (id, endpoint_artifact_id, credential_artifact_id)
		VALUES ('default', $1, $2)
		ON CONFLICT (id) DO UPDATE
		SET endpoint_artifact_id = EXCLUDED.endpoint_artifact_id,
		    credential_artifact_id = EXCLUDED.credential_artifact_id,
		    updated_at = now()
		RETURNING created_at, updated_at
	`, profile.EndpointArtifactID, profile.CredentialArtifactID).Scan(&profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plugin_runtime_registries WHERE profile_id = 'default'`); err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	for position, registry := range profile.Registries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO plugin_runtime_registries (profile_id, position, endpoint_artifact_id, credential_artifact_id)
			VALUES ('default', $1, $2, $3)
		`, position, registry.EndpointArtifactID, registry.CredentialArtifactID); err != nil {
			return domain.PluginRuntimeProfile{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	return profile, nil
}

func (s *Store) DeletePluginRuntimeProfile(ctx context.Context) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM plugin_runtime_profiles WHERE id = 'default'`)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
