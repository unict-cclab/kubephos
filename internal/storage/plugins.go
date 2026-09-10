package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) ActivatePluginPackage(ctx context.Context, pluginPackage domain.PluginPackage) (domain.PluginPackage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PluginPackage{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, pluginPackage.PluginID); err != nil {
		return domain.PluginPackage{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE plugin_packages SET active = false, updated_at = now() WHERE plugin_id = $1 AND active`, pluginPackage.PluginID); err != nil {
		return domain.PluginPackage{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO plugin_packages (plugin_id, version, digest, descriptor, descriptor_digest, active)
		VALUES ($1, $2, $3, $4, $5, true)
		ON CONFLICT (plugin_id, version, digest, descriptor_digest)
		DO UPDATE SET descriptor = EXCLUDED.descriptor, active = true, updated_at = now()
		RETURNING sequence, active, created_at, updated_at
	`, pluginPackage.PluginID, pluginPackage.Version, pluginPackage.Digest, pluginPackage.Descriptor, pluginPackage.DescriptorDigest).Scan(
		&pluginPackage.Sequence, &pluginPackage.Active, &pluginPackage.CreatedAt, &pluginPackage.UpdatedAt,
	)
	if err != nil {
		return domain.PluginPackage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PluginPackage{}, err
	}
	return pluginPackage, nil
}

func (s *Store) GetActivePluginPackage(ctx context.Context, pluginID string) (domain.PluginPackage, error) {
	var pluginPackage domain.PluginPackage
	err := s.pool.QueryRow(ctx, `
		SELECT sequence, plugin_id, version, digest, descriptor, descriptor_digest, active, created_at, updated_at
		FROM plugin_packages
		WHERE plugin_id = $1 AND active
	`, pluginID).Scan(&pluginPackage.Sequence, &pluginPackage.PluginID, &pluginPackage.Version, &pluginPackage.Digest, &pluginPackage.Descriptor, &pluginPackage.DescriptorDigest, &pluginPackage.Active, &pluginPackage.CreatedAt, &pluginPackage.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PluginPackage{}, ErrNotFound
	}
	return pluginPackage, err
}

func (s *Store) ListActivePluginPackages(ctx context.Context) ([]domain.PluginPackage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, plugin_id, version, digest, descriptor, descriptor_digest, active, created_at, updated_at
		FROM plugin_packages
		WHERE active
		ORDER BY plugin_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PluginPackage{}
	for rows.Next() {
		var pluginPackage domain.PluginPackage
		if err := rows.Scan(&pluginPackage.Sequence, &pluginPackage.PluginID, &pluginPackage.Version, &pluginPackage.Digest, &pluginPackage.Descriptor, &pluginPackage.DescriptorDigest, &pluginPackage.Active, &pluginPackage.CreatedAt, &pluginPackage.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, pluginPackage)
	}
	return result, rows.Err()
}

func (s *Store) ListPluginPackages(ctx context.Context, limit int) ([]domain.PluginPackage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, plugin_id, version, digest, '', descriptor_digest, active, created_at, updated_at
		FROM plugin_packages
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PluginPackage{}
	for rows.Next() {
		var pluginPackage domain.PluginPackage
		if err := rows.Scan(&pluginPackage.Sequence, &pluginPackage.PluginID, &pluginPackage.Version, &pluginPackage.Digest, &pluginPackage.Descriptor, &pluginPackage.DescriptorDigest, &pluginPackage.Active, &pluginPackage.CreatedAt, &pluginPackage.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, pluginPackage)
	}
	return result, rows.Err()
}
