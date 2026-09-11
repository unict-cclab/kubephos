package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

const pluginImportColumns = `id, status, progress, message, error, descriptor, descriptor_digest, COALESCE(manifest, 'null'::jsonb), COALESCE(plugin_id, ''), COALESCE(version, ''), COALESCE(digest, ''), package_sequence, created_at, started_at, completed_at, updated_at`
const claimedPluginImportColumns = `job.id, job.status, job.progress, job.message, job.error, job.descriptor, job.descriptor_digest, COALESCE(job.manifest, 'null'::jsonb), COALESCE(job.plugin_id, ''), COALESCE(job.version, ''), COALESCE(job.digest, ''), job.package_sequence, job.created_at, job.started_at, job.completed_at, job.updated_at`

func scanPluginImportJob(row pgx.Row) (domain.PluginImportJob, error) {
	var job domain.PluginImportJob
	err := row.Scan(&job.ID, &job.Status, &job.Progress, &job.Message, &job.Error, &job.Descriptor, &job.DescriptorDigest, &job.Manifest, &job.PluginID, &job.Version, &job.Digest, &job.PackageSequence, &job.CreatedAt, &job.StartedAt, &job.CompletedAt, &job.UpdatedAt)
	return job, err
}

func (s *Store) CreatePluginImportJob(ctx context.Context, job domain.PluginImportJob) (domain.PluginImportJob, error) {
	return scanPluginImportJob(s.pool.QueryRow(ctx, `
		INSERT INTO plugin_import_jobs (id, descriptor, descriptor_digest, status, progress, message)
		VALUES ($1, $2, $3, 'queued', 0, 'Waiting for an import worker.')
		RETURNING `+pluginImportColumns,
		job.ID, job.Descriptor, job.DescriptorDigest,
	))
}

func (s *Store) GetPluginImportJob(ctx context.Context, id string) (domain.PluginImportJob, error) {
	job, err := scanPluginImportJob(s.pool.QueryRow(ctx, `SELECT `+pluginImportColumns+` FROM plugin_import_jobs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PluginImportJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) ListPluginImportJobs(ctx context.Context, limit int) ([]domain.PluginImportJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+pluginImportColumns+` FROM plugin_import_jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PluginImportJob{}
	for rows.Next() {
		job, err := scanPluginImportJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

func (s *Store) ClaimPluginImportJob(ctx context.Context, owner string) (domain.PluginImportJob, bool, error) {
	job, err := scanPluginImportJob(s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM plugin_import_jobs
			WHERE status = 'queued'
			   OR (status IN ('inspecting', 'validating', 'activating') AND lease_until < now())
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE plugin_import_jobs AS job
		SET status = 'inspecting', progress = 10, message = 'Inspecting the immutable package descriptor.', error = '',
		    lease_owner = $1, lease_until = now() + interval '30 seconds', started_at = COALESCE(started_at, now()), updated_at = now()
		FROM candidate
		WHERE job.id = candidate.id
		RETURNING `+claimedPluginImportColumns,
		owner,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PluginImportJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *Store) SetPluginImportJobState(ctx context.Context, id, owner, status string, progress int, message string, manifest json.RawMessage, pluginID, version, digest string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE plugin_import_jobs
		SET status = $3, progress = $4, message = $5,
		    manifest = COALESCE($6, manifest), plugin_id = NULLIF($7, ''), version = NULLIF($8, ''), digest = NULLIF($9, ''),
		    lease_until = now() + interval '30 seconds', updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status IN ('inspecting', 'validating', 'activating')
	`, id, owner, status, progress, message, nullableJSON(manifest), pluginID, version, digest)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) RenewPluginImportJobLease(ctx context.Context, id, owner string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE plugin_import_jobs SET lease_until = now() + interval '30 seconds', updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status IN ('inspecting', 'validating', 'activating')
	`, id, owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailPluginImportJob(ctx context.Context, id, owner, failure string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE plugin_import_jobs
		SET status = 'failed', progress = 100, message = 'Package import failed.', error = $3,
		    lease_owner = NULL, lease_until = NULL, completed_at = now(), updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status IN ('inspecting', 'validating', 'activating')
	`, id, owner, failure)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CompletePluginImportJob(ctx context.Context, id, owner string, pluginPackage domain.PluginPackage) (domain.PluginPackage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PluginPackage{}, err
	}
	defer tx.Rollback(ctx)
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT lease_owner = $2 AND status = 'activating' AND lease_until > now() FROM plugin_import_jobs WHERE id = $1 FOR UPDATE`, id, owner).Scan(&valid); errors.Is(err, pgx.ErrNoRows) {
		return domain.PluginPackage{}, ErrNotFound
	} else if err != nil {
		return domain.PluginPackage{}, err
	}
	if !valid {
		return domain.PluginPackage{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, pluginPackage.PluginID); err != nil {
		return domain.PluginPackage{}, err
	}
	var changedDescriptor bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM plugin_packages
			WHERE plugin_id = $1 AND version = $2 AND digest = $3 AND descriptor_digest <> $4
		)
	`, pluginPackage.PluginID, pluginPackage.Version, pluginPackage.Digest, pluginPackage.DescriptorDigest).Scan(&changedDescriptor); err != nil {
		return domain.PluginPackage{}, err
	}
	if changedDescriptor {
		return domain.PluginPackage{}, ErrConflict
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
	command, err := tx.Exec(ctx, `
		UPDATE plugin_import_jobs
		SET status = 'succeeded', progress = 100, message = 'Package validated and activated.', error = '', package_sequence = $3,
		    lease_owner = NULL, lease_until = NULL, completed_at = now(), updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status = 'activating'
	`, id, owner, pluginPackage.Sequence)
	if err != nil {
		return domain.PluginPackage{}, err
	}
	if command.RowsAffected() != 1 {
		return domain.PluginPackage{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PluginPackage{}, err
	}
	return pluginPackage, nil
}
