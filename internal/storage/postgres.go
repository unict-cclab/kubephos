package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
		INSERT INTO operations (id, workspace_id, plugin_id, title, status, spec, plan, validation, plan_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at
	`, operation.ID, operation.WorkspaceID, operation.PluginID, operation.Title, operation.Status, operation.Spec, plan, validation, operation.PlanHash).Scan(&operation.CreatedAt)
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
		SELECT id, workspace_id, plugin_id, title, status, spec, plan, validation, plan_hash,
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
		SELECT id, workspace_id, plugin_id, title, status, spec, plan, validation, plan_hash,
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
		RETURNING operation.id, operation.workspace_id, operation.plugin_id, operation.title, operation.status,
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

func (s *Store) RequeueExpiredOperations(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		WITH expired AS (
			UPDATE operations
			SET status = 'queued', lease_owner = NULL, lease_until = NULL, error = ''
			WHERE status IN ('prechecking', 'running', 'verifying') AND lease_until < now()
			RETURNING id
		)
		UPDATE operation_steps
		SET status = 'pending', started_at = NULL, completed_at = NULL, error = ''
		WHERE operation_id IN (SELECT id FROM expired) AND status IN ('prechecking', 'running', 'verifying')
	`)
	return err
}

func (s *Store) RequeueOperation(ctx context.Context, operationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE operations
		SET status = 'queued', lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND cancel_requested = false
	`, operationID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE operation_steps
		SET status = 'pending', started_at = NULL, completed_at = NULL, error = ''
		WHERE operation_id = $1 AND status IN ('prechecking', 'running', 'verifying')
	`, operationID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, level, source, message)
		VALUES ($1, 'warning', 'worker', 'Worker stopped; operation returned to the queue.')
	`, operationID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	var stepID any
	if artifact.StepID != "" {
		stepID = artifact.StepID
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO artifacts (id, operation_id, step_id, name, media_type, storage_key, digest, size_bytes, sensitive)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at
	`, artifact.ID, artifact.OperationID, stepID, artifact.Name, artifact.MediaType, artifact.StorageKey, artifact.Digest, artifact.SizeBytes, artifact.Sensitive).Scan(&artifact.CreatedAt)
	return artifact, err
}

func (s *Store) ListArtifacts(ctx context.Context, operationID string) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, operation_id, COALESCE(step_id, ''), name, media_type, storage_key, digest, size_bytes, sensitive, created_at
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
		if err := rows.Scan(&artifact.ID, &artifact.OperationID, &artifact.StepID, &artifact.Name, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.Sensitive, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (domain.Artifact, error) {
	var artifact domain.Artifact
	err := s.pool.QueryRow(ctx, `
		SELECT id, operation_id, COALESCE(step_id, ''), name, media_type, storage_key, digest, size_bytes, sensitive, created_at
		FROM artifacts
		WHERE id = $1
	`, artifactID).Scan(&artifact.ID, &artifact.OperationID, &artifact.StepID, &artifact.Name, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes, &artifact.Sensitive, &artifact.CreatedAt)
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
