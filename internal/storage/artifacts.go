package storage

import (
	"context"

	"errors"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

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
	return s.ListAvailableArtifactsForUser(ctx, "", limit)
}

func (s *Store) ListAvailableArtifactsForUser(ctx context.Context, userID string, limit int) ([]domain.Artifact, error) {
	return s.readListAvailableArtifactsForUser(ctx, userID, limit, false)
}

func (s *Store) ListAvailableArtifactsVisibleToUser(ctx context.Context, userID string, limit int) ([]domain.Artifact, error) {
	return s.readListAvailableArtifactsForUser(ctx, userID, limit, true)
}

func (s *Store) readListAvailableArtifactsForUser(ctx context.Context, userID string, limit int, shared bool) ([]domain.Artifact, error) {
	visibility := "run.owner_user_id = $2"
	extraVisibility := ""
	if shared {
		visibility = "NOT a.sensitive AND can_view_run($2, run.id)"
		extraVisibility = " OR (NOT a.sensitive AND can_view_operation($2,o.id))"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.operation_id, o.workspace_id, COALESCE(a.step_id, ''), COALESCE(a.output_name, ''),
		       a.name, a.artifact_type, a.artifact_version, a.media_type, a.storage_key, a.digest,
		       a.size_bytes, a.storage_digest, a.stored_size_bytes, a.encryption_nonce,
		       a.sensitive, a.verified_at, a.created_at
		FROM artifacts a
		JOIN operations o ON o.id = a.operation_id
		WHERE o.status = $1 AND a.verified_at IS NOT NULL
		  AND ($2 = '' OR EXISTS (
			SELECT 1 FROM pipeline_run_stages stage
			JOIN pipeline_runs run ON run.id = stage.run_id
			WHERE `+visibility+` AND (stage.operation_id = o.id OR stage.cleanup_operation_id = o.id)
		  )`+extraVisibility+`)
		ORDER BY a.created_at DESC
		LIMIT $3
	`, domain.OperationSucceeded, userID, limit)
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

func (s *Store) UserOwnsOperation(ctx context.Context, userID, operationID string) (bool, error) {
	var owned bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pipeline_run_stages stage
			JOIN pipeline_runs run ON run.id = stage.run_id
			WHERE run.owner_user_id = $1 AND (stage.operation_id = $2 OR stage.cleanup_operation_id = $2)
		)
	`, userID, operationID).Scan(&owned)
	return owned, err
}

func (s *Store) GetExperimentOwner(ctx context.Context, experimentID string) (string, error) {
	var ownerID string
	err := s.pool.QueryRow(ctx, `SELECT owner_user_id FROM experiments WHERE id = $1`, experimentID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return ownerID, err
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
