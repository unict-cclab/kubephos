package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreatePipelineRun(ctx context.Context, run domain.PipelineRun) (domain.PipelineRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PipelineRun{}, err
	}
	defer tx.Rollback(ctx)
	var workspaceID, pipelineHash, resultType, resultVersion string
	err = tx.QueryRow(ctx, `
		SELECT workspace_id, pipeline_hash, resolution->'result'->>'type', resolution->'result'->>'version'
		FROM pipelines
		WHERE id = $1
		FOR SHARE
	`, run.PipelineID).Scan(&workspaceID, &pipelineHash, &resultType, &resultVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PipelineRun{}, ErrNotFound
	}
	if err != nil {
		return domain.PipelineRun{}, err
	}
	if workspaceID != run.WorkspaceID || pipelineHash != run.PipelineHash || resultType != run.ResultType || resultVersion != run.ResultVersion {
		return domain.PipelineRun{}, ErrConflict
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, workspace_id, cluster_resource_id, name, status, pipeline_hash, result_type, result_version, scheduled_for)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, COALESCE($10, now()))
		RETURNING created_at, queued_at, scheduled_for
	`, run.ID, run.PipelineID, run.WorkspaceID, run.ClusterResourceID, run.Name, domain.OperationQueued, run.PipelineHash, run.ResultType, run.ResultVersion, run.ScheduledFor).Scan(&run.CreatedAt, &run.QueuedAt, &run.ScheduledFor)
	if err != nil {
		return domain.PipelineRun{}, err
	}
	run.Status = domain.OperationQueued
	for index := range run.Stages {
		stage := &run.Stages[index]
		stage.Status = domain.StepPending
		_, err := tx.Exec(ctx, `
			INSERT INTO pipeline_run_stages (id, run_id, position, stage_id, plugin_id, title, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, stage.ID, run.ID, stage.Position, stage.StageID, stage.PluginID, stage.Title, stage.Status)
		if err != nil {
			return domain.PipelineRun{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PipelineRun{}, err
	}
	return run, nil
}

func (s *Store) CreatePipelineExperiment(ctx context.Context, experiment domain.Experiment, runs map[string]domain.PipelineRun) (domain.Experiment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Experiment{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		INSERT INTO experiments (id, workspace_id, configuration_id, kind, name, description, status, result_type, result_version, scheduled_for)
		VALUES ($1, $2, NULLIF($3, ''), COALESCE(NULLIF($4, ''), 'comparison'), $5, $6, $7, $8, $9, $10)
		RETURNING created_at, updated_at
	`, experiment.ID, experiment.WorkspaceID, experiment.ConfigurationID, experiment.Kind, experiment.Name, experiment.Description, domain.OperationQueued, experiment.ResultType, experiment.ResultVersion, experiment.ScheduledFor).Scan(&experiment.CreatedAt, &experiment.UpdatedAt)
	if err != nil {
		return domain.Experiment{}, err
	}
	experiment.Status = domain.OperationQueued
	for variantIndex := range experiment.Variants {
		variant := &experiment.Variants[variantIndex]
		if variant.Alias == "" {
			variant.Alias = variant.Name
		}
		if len(variant.Configuration) == 0 {
			variant.Configuration = json.RawMessage(`{}`)
		}
		var workspaceID, pipelineHash, resultType, resultVersion string
		err := tx.QueryRow(ctx, `
			SELECT workspace_id, pipeline_hash, resolution->'result'->>'type', resolution->'result'->>'version'
			FROM pipelines
			WHERE id = $1
			FOR SHARE
		`, variant.PipelineID).Scan(&workspaceID, &pipelineHash, &resultType, &resultVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Experiment{}, ErrNotFound
		}
		if err != nil {
			return domain.Experiment{}, err
		}
		if workspaceID != experiment.WorkspaceID || pipelineHash != variant.PipelineHash || resultType != experiment.ResultType || resultVersion != experiment.ResultVersion {
			return domain.Experiment{}, ErrConflict
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO experiment_variants (id, experiment_id, position, name, display_alias, plot_color, pipeline_id, pipeline_hash, configuration_id, configuration)
			VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), $4), NULLIF($6, ''), $7, $8, NULLIF($9, ''), $10)
		`, variant.ID, experiment.ID, variant.Position, variant.Name, variant.Alias, variant.Color, variant.PipelineID, variant.PipelineHash, variant.ConfigurationID, variant.Configuration)
		if err != nil {
			return domain.Experiment{}, err
		}
		for trialIndex := range variant.Trials {
			trial := &variant.Trials[trialIndex]
			run, ok := runs[trial.ID]
			if !ok || run.PipelineID != variant.PipelineID || run.PipelineHash != variant.PipelineHash || run.WorkspaceID != experiment.WorkspaceID {
				return domain.Experiment{}, ErrConflict
			}
			err = tx.QueryRow(ctx, `
				INSERT INTO pipeline_runs (id, pipeline_id, workspace_id, cluster_resource_id, name, status, pipeline_hash, result_type, result_version, scheduled_for)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, COALESCE($10, now()))
				RETURNING created_at, queued_at, scheduled_for
			`, run.ID, run.PipelineID, run.WorkspaceID, run.ClusterResourceID, run.Name, domain.OperationQueued, run.PipelineHash, run.ResultType, run.ResultVersion, run.ScheduledFor).Scan(&run.CreatedAt, &run.QueuedAt, &run.ScheduledFor)
			if err != nil {
				return domain.Experiment{}, err
			}
			for stageIndex := range run.Stages {
				stage := &run.Stages[stageIndex]
				_, err = tx.Exec(ctx, `
					INSERT INTO pipeline_run_stages (id, run_id, position, stage_id, plugin_id, title, status)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
				`, stage.ID, run.ID, stage.Position, stage.StageID, stage.PluginID, stage.Title, domain.StepPending)
				if err != nil {
					return domain.Experiment{}, err
				}
			}
			trial.PipelineRunID = run.ID
			trial.Status = domain.OperationQueued
			err = tx.QueryRow(ctx, `
				INSERT INTO experiment_trials (id, variant_id, position, status, pipeline_run_id)
				VALUES ($1, $2, $3, $4, $5)
				RETURNING created_at
			`, trial.ID, variant.ID, trial.Position, trial.Status, run.ID).Scan(&trial.CreatedAt)
			if err != nil {
				return domain.Experiment{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Experiment{}, err
	}
	return experiment, nil
}

func (s *Store) ListPipelineRuns(ctx context.Context, limit int) ([]domain.PipelineRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pipeline_id, workspace_id, COALESCE(cluster_resource_id, ''), name, status, pipeline_hash, result_type, result_version,
		       COALESCE(result_artifact_id, ''), cancel_requested, COALESCE(terminal_status, ''), error, created_at, queued_at, scheduled_for, started_at, completed_at
		FROM pipeline_runs
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	result := []domain.PipelineRun{}
	for rows.Next() {
		run, err := scanPipelineRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range result {
		stages, err := s.listPipelineRunStages(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Stages = stages
	}
	return result, nil
}

func (s *Store) GetPipelineRun(ctx context.Context, runID string) (domain.PipelineRun, error) {
	run, err := scanPipelineRun(s.pool.QueryRow(ctx, `
		SELECT id, pipeline_id, workspace_id, COALESCE(cluster_resource_id, ''), name, status, pipeline_hash, result_type, result_version,
		       COALESCE(result_artifact_id, ''), cancel_requested, COALESCE(terminal_status, ''), error, created_at, queued_at, scheduled_for, started_at, completed_at
		FROM pipeline_runs
		WHERE id = $1
	`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PipelineRun{}, ErrNotFound
	}
	if err != nil {
		return domain.PipelineRun{}, err
	}
	run.Stages, err = s.listPipelineRunStages(ctx, runID)
	return run, err
}

func (s *Store) GetPipelineRunArtifact(ctx context.Context, runID, outputName string) (domain.Artifact, error) {
	var artifact domain.Artifact
	err := s.pool.QueryRow(ctx, `
		SELECT artifact.id, artifact.operation_id, run.workspace_id, COALESCE(artifact.step_id, ''), COALESCE(artifact.output_name, ''), artifact.name,
		       artifact.artifact_type, artifact.artifact_version, artifact.media_type, artifact.storage_key, artifact.digest, artifact.size_bytes,
		       artifact.storage_digest, artifact.stored_size_bytes, artifact.encryption_nonce, artifact.sensitive, artifact.verified_at, artifact.created_at
		FROM pipeline_runs run
		JOIN pipeline_run_stages stage ON stage.run_id = run.id
		JOIN artifacts artifact ON artifact.operation_id = stage.operation_id
		WHERE run.id = $1 AND artifact.output_name = $2
		ORDER BY stage.position, artifact.created_at
		LIMIT 1
	`, runID, outputName).Scan(&artifact.ID, &artifact.OperationID, &artifact.WorkspaceID, &artifact.StepID, &artifact.OutputName, &artifact.Name,
		&artifact.Type, &artifact.Version, &artifact.MediaType, &artifact.StorageKey, &artifact.Digest, &artifact.SizeBytes,
		&artifact.StorageDigest, &artifact.StoredSizeBytes, &artifact.EncryptionNonce, &artifact.Sensitive, &artifact.VerifiedAt, &artifact.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Artifact{}, ErrNotFound
	}
	return artifact, err
}

func scanPipelineRun(row scanner) (domain.PipelineRun, error) {
	var run domain.PipelineRun
	err := row.Scan(&run.ID, &run.PipelineID, &run.WorkspaceID, &run.ClusterResourceID, &run.Name, &run.Status, &run.PipelineHash, &run.ResultType, &run.ResultVersion, &run.ResultArtifactID, &run.CancelRequested, &run.TerminalStatus, &run.Error, &run.CreatedAt, &run.QueuedAt, &run.ScheduledFor, &run.StartedAt, &run.CompletedAt)
	return run, err
}

func (s *Store) listPipelineRunStages(ctx context.Context, runID string) ([]domain.PipelineRunStage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, position, stage_id, plugin_id, title, status, COALESCE(operation_id, ''), spec, error, started_at, completed_at,
		       COALESCE(cleanup_operation_id, ''), cleanup_status, cleanup_error, cleanup_started_at, cleanup_completed_at
		FROM pipeline_run_stages
		WHERE run_id = $1
		ORDER BY position
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PipelineRunStage{}
	for rows.Next() {
		var stage domain.PipelineRunStage
		if err := rows.Scan(&stage.ID, &stage.RunID, &stage.Position, &stage.StageID, &stage.PluginID, &stage.Title, &stage.Status, &stage.OperationID, &stage.Spec, &stage.Error, &stage.StartedAt, &stage.CompletedAt, &stage.CleanupOperationID, &stage.CleanupStatus, &stage.CleanupError, &stage.CleanupStartedAt, &stage.CleanupCompletedAt); err != nil {
			return nil, err
		}
		result = append(result, stage)
	}
	return result, rows.Err()
}

func (s *Store) ClaimPipelineRun(ctx context.Context, owner string) (domain.PipelineRun, bool, error) {
	var runID string
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT candidate.id
			FROM pipeline_runs candidate
			LEFT JOIN experiment_trials candidate_trial ON candidate_trial.pipeline_run_id = candidate.id
			LEFT JOIN experiment_variants candidate_variant ON candidate_variant.id = candidate_trial.variant_id
			WHERE candidate.status IN ($1, $2)
			  AND candidate.scheduled_for <= now()
			  AND (candidate.lease_until IS NULL OR candidate.lease_until < now())
			  AND NOT EXISTS (
				SELECT 1
				FROM experiment_variants earlier_variant
				JOIN experiment_trials earlier_trial ON earlier_trial.variant_id = earlier_variant.id
				WHERE candidate_variant.experiment_id IS NOT NULL
				  AND earlier_variant.experiment_id = candidate_variant.experiment_id
				  AND (earlier_variant.position < candidate_variant.position OR earlier_variant.position = candidate_variant.position AND earlier_trial.position < candidate_trial.position)
				  AND earlier_trial.status NOT IN ($4, $5, $6)
			  )
			  AND NOT EXISTS (
				SELECT 1
				FROM pipeline_runs active
				WHERE candidate.cluster_resource_id IS NOT NULL
				  AND active.cluster_resource_id = candidate.cluster_resource_id
				  AND active.id <> candidate.id
				  AND active.status = $2
			  )
			ORDER BY candidate.scheduled_for, candidate.created_at
			FOR UPDATE OF candidate SKIP LOCKED
			LIMIT 1
		)
		UPDATE pipeline_runs r
		SET status = $2, started_at = COALESCE(started_at, now()), lease_owner = $3, lease_until = now() + interval '15 seconds'
		FROM candidate
		WHERE r.id = candidate.id
		RETURNING r.id
	`, domain.OperationQueued, domain.OperationRunning, owner, domain.OperationSucceeded, domain.OperationFailed, domain.OperationCanceled).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PipelineRun{}, false, nil
	}
	if err != nil {
		return domain.PipelineRun{}, false, err
	}
	run, err := s.GetPipelineRun(ctx, runID)
	return run, err == nil, err
}

func (s *Store) ReleasePipelineRun(ctx context.Context, runID, owner string) error {
	_, err := s.pool.Exec(ctx, `UPDATE pipeline_runs SET lease_owner = NULL, lease_until = NULL WHERE id = $1 AND lease_owner = $2`, runID, owner)
	return err
}

func (s *Store) CreatePipelineStageOperation(ctx context.Context, runID, runStageID, owner string, operation domain.Operation) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `
		SELECT s.status
		FROM pipeline_run_stages s
		JOIN pipeline_runs r ON r.id = s.run_id
		WHERE s.id = $1 AND s.run_id = $2 AND r.status = $3 AND r.lease_owner = $4 AND r.lease_until > now()
		FOR UPDATE OF s, r
	`, runStageID, runID, domain.OperationRunning, owner).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, ErrConflict
	}
	if err != nil {
		return domain.Operation{}, err
	}
	if status != domain.StepPending {
		return domain.Operation{}, ErrConflict
	}
	plan, err := json.Marshal(operation.Plan)
	if err != nil {
		return domain.Operation{}, err
	}
	validation, err := json.Marshal(operation.Validation)
	if err != nil {
		return domain.Operation{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO operations (id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		RETURNING created_at, queued_at
	`, operation.ID, operation.WorkspaceID, operation.PluginID, operation.PluginVersion, operation.PluginDigest, operation.Title, domain.OperationQueued, operation.Spec, plan, validation, operation.PlanHash).Scan(&operation.CreatedAt, &operation.QueuedAt)
	if err != nil {
		return domain.Operation{}, err
	}
	operation.Status = domain.OperationQueued
	for position, planned := range operation.Plan.Steps {
		step := domain.OperationStep{ID: operation.ID + "_" + planned.ID, OperationID: operation.ID, Position: position + 1, Name: planned.Name, Status: domain.StepPending, Input: planned.Input}
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
		VALUES ($1, 'info', 'pipeline', $2)
	`, operation.ID, fmt.Sprintf("Pipeline run %s queued validated stage %s.", runID, runStageID))
	if err != nil {
		return domain.Operation{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE pipeline_run_stages
		SET status = $2, operation_id = $3, spec = $4, started_at = now()
		WHERE id = $1 AND status = $5
	`, runStageID, domain.StepQueued, operation.ID, operation.Spec, domain.StepPending)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = ErrConflict
		}
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

func (s *Store) SyncPipelineRunStage(ctx context.Context, runStageID string, operation domain.Operation) error {
	status := operation.Status
	if status == domain.OperationReady || status == domain.OperationQueued {
		status = domain.StepQueued
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_run_stages
		SET status = $2, error = $3,
		    completed_at = CASE WHEN $2 IN ($4, $5, $6) THEN COALESCE(completed_at, now()) ELSE completed_at END
		WHERE id = $1 AND operation_id = $7
	`, runStageID, status, operation.Error, domain.StepSucceeded, domain.StepFailed, domain.StepCanceled, operation.ID)
	return err
}

func (s *Store) CreatePipelineCleanupOperation(ctx context.Context, runID, runStageID, owner string, operation domain.Operation) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback(ctx)
	var cleanupStatus string
	err = tx.QueryRow(ctx, `
		SELECT s.cleanup_status
		FROM pipeline_run_stages s
		JOIN pipeline_runs r ON r.id = s.run_id
		WHERE s.id = $1 AND s.run_id = $2 AND s.status IN ($3, $4, $5) AND r.status = $6 AND r.lease_owner = $7 AND r.lease_until > now()
		FOR UPDATE OF s, r
	`, runStageID, runID, domain.StepSucceeded, domain.StepFailed, domain.StepCanceled, domain.OperationRunning, owner).Scan(&cleanupStatus)
	if errors.Is(err, pgx.ErrNoRows) || cleanupStatus != domain.StepPending {
		return domain.Operation{}, ErrConflict
	}
	if err != nil {
		return domain.Operation{}, err
	}
	plan, err := json.Marshal(operation.Plan)
	if err != nil {
		return domain.Operation{}, err
	}
	validation, err := json.Marshal(operation.Validation)
	if err != nil {
		return domain.Operation{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO operations (id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		RETURNING created_at, queued_at
	`, operation.ID, operation.WorkspaceID, operation.PluginID, operation.PluginVersion, operation.PluginDigest, operation.Title, domain.OperationQueued, operation.Spec, plan, validation, operation.PlanHash).Scan(&operation.CreatedAt, &operation.QueuedAt)
	if err != nil {
		return domain.Operation{}, err
	}
	operation.Status = domain.OperationQueued
	for position, planned := range operation.Plan.Steps {
		step := domain.OperationStep{ID: operation.ID + "_" + planned.ID, OperationID: operation.ID, Position: position + 1, Name: planned.Name, Status: domain.StepPending, Input: planned.Input}
		if _, err = tx.Exec(ctx, `
			INSERT INTO operation_steps (id, operation_id, position, name, status, input)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, step.ID, step.OperationID, step.Position, step.Name, step.Status, step.Input); err != nil {
			return domain.Operation{}, err
		}
		operation.Steps = append(operation.Steps, step)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, level, source, message)
		VALUES ($1, 'info', 'pipeline', $2)
	`, operation.ID, fmt.Sprintf("Pipeline run %s queued verified cleanup for stage %s.", runID, runStageID)); err != nil {
		return domain.Operation{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE pipeline_run_stages
		SET cleanup_status = $2, cleanup_operation_id = $3, cleanup_started_at = now()
		WHERE id = $1 AND cleanup_status = $4
	`, runStageID, domain.StepQueued, operation.ID, domain.StepPending)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = ErrConflict
		}
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

func (s *Store) SyncPipelineRunStageCleanup(ctx context.Context, runStageID string, operation domain.Operation) error {
	status := operation.Status
	if status == domain.OperationReady || status == domain.OperationQueued {
		status = domain.StepQueued
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_run_stages
		SET cleanup_status = $2, cleanup_error = $3,
		    cleanup_completed_at = CASE WHEN $2 IN ($4, $5, $6) THEN COALESCE(cleanup_completed_at, now()) ELSE cleanup_completed_at END
		WHERE id = $1 AND cleanup_operation_id = $7
	`, runStageID, status, operation.Error, domain.StepSucceeded, domain.StepFailed, domain.StepCanceled, operation.ID)
	return err
}

func (s *Store) SkipPipelineRunStageCleanup(ctx context.Context, runID, runStageID, owner string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE pipeline_run_stages s
		SET cleanup_status = 'skipped', cleanup_completed_at = now()
		FROM pipeline_runs r
		WHERE s.id = $1 AND s.run_id = $2 AND s.cleanup_status = $3
		  AND r.id = s.run_id AND r.status = $4 AND r.lease_owner = $5 AND r.lease_until > now()
	`, runStageID, runID, domain.StepPending, domain.OperationRunning, owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailPipelineRunStage(ctx context.Context, runStageID, status, message string) error {
	if status != domain.StepFailed && status != domain.StepCanceled {
		return errors.New("pipeline stage terminal status is invalid")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_run_stages
		SET status = $2, error = $3, completed_at = now()
		WHERE id = $1 AND status NOT IN ($4, $5, $6)
	`, runStageID, status, message, domain.StepSucceeded, domain.StepFailed, domain.StepCanceled)
	return err
}

func (s *Store) CompletePipelineRun(ctx context.Context, runID, owner, artifactID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE pipeline_runs
		SET status = $3, result_artifact_id = NULLIF($4, ''), completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND lease_owner = $2 AND status = $5
		  AND NOT EXISTS (SELECT 1 FROM pipeline_run_stages WHERE run_id = $1 AND status <> $6)
	`, runID, owner, domain.OperationSucceeded, artifactID, domain.OperationRunning, domain.StepSucceeded)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE managed_resources
		SET deleted_at = now(), updated_at = now()
		WHERE deletion_pipeline_run_id = $1 AND deleted_at IS NULL
	`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE managed_resources
		SET pipeline_id = recreation_pipeline_id,
		    pipeline_run_id = recreation_pipeline_run_id,
		    recreation_pipeline_id = NULL,
		    recreation_pipeline_run_id = NULL,
		    updated_at = now()
		WHERE recreation_pipeline_run_id = $1 AND deleted_at IS NULL
	`, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginPipelineRunTermination(ctx context.Context, runID, owner, status, message string) error {
	if status != domain.OperationFailed && status != domain.OperationCanceled {
		return errors.New("pipeline terminal status is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs
		SET terminal_status = $3, error = $4
		WHERE id = $1 AND lease_owner = $2 AND status = $5
	`, runID, owner, status, message, domain.OperationRunning)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailPipelineRun(ctx context.Context, runID, owner, status, message string) error {
	if status != domain.OperationFailed && status != domain.OperationCanceled {
		return errors.New("pipeline terminal status is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE pipeline_runs
		SET status = $3, error = $4, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND lease_owner = $2 AND status = $5
	`, runID, owner, status, message, domain.OperationRunning)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `
		WITH current_trial AS (
			SELECT variant.experiment_id, variant.position AS variant_position, trial.position AS trial_position
			FROM experiment_trials trial
			JOIN experiment_variants variant ON variant.id = trial.variant_id
			WHERE trial.pipeline_run_id = $1
		), canceled_runs AS (
			UPDATE pipeline_runs pending
			SET status = $2, error = $3, completed_at = now(), lease_owner = NULL, lease_until = NULL
			FROM experiment_trials trial
			JOIN experiment_variants variant ON variant.id = trial.variant_id
			JOIN current_trial current ON current.experiment_id = variant.experiment_id
			WHERE pending.id = trial.pipeline_run_id AND pending.status = $4
			  AND (variant.position > current.variant_position OR variant.position = current.variant_position AND trial.position > current.trial_position)
			RETURNING pending.id
		)
		UPDATE experiment_trials trial
		SET status = $2, error = $3, completed_at = now(), updated_at = now()
		FROM canceled_runs
		WHERE trial.pipeline_run_id = canceled_runs.id AND trial.status = $4
	`, runID, domain.OperationCanceled, "Not started because an earlier sequential run did not complete safely.", domain.OperationQueued)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RequestPipelineRunCancel(ctx context.Context, runID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs
		SET cancel_requested = true
		WHERE id = $1 AND status IN ($2, $3)
	`, runID, domain.OperationQueued, domain.OperationRunning)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) RequestExperimentCancel(ctx context.Context, experimentID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM experiments WHERE id = $1 FOR UPDATE`, experimentID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !cancelableExperimentStatus(status) {
		return ErrConflict
	}
	const message = "Canceled by user before start."
	queued, err := tx.Exec(ctx, `
		UPDATE pipeline_runs run
		SET status = $2, cancel_requested = true, error = $3, completed_at = now(), lease_owner = NULL, lease_until = NULL
		FROM experiment_trials trial
		JOIN experiment_variants variant ON variant.id = trial.variant_id
		WHERE variant.experiment_id = $1 AND run.id = trial.pipeline_run_id AND run.status = $4
	`, experimentID, domain.OperationCanceled, message, domain.OperationQueued)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_run_stages stage
		SET status = $2, error = $3, completed_at = now()
		FROM pipeline_runs run
		JOIN experiment_trials trial ON trial.pipeline_run_id = run.id
		JOIN experiment_variants variant ON variant.id = trial.variant_id
		WHERE variant.experiment_id = $1 AND stage.run_id = run.id AND run.status = $4 AND stage.status = $5
	`, experimentID, domain.StepCanceled, message, domain.OperationCanceled, domain.StepPending); err != nil {
		return err
	}
	running, err := tx.Exec(ctx, `
		UPDATE pipeline_runs run
		SET cancel_requested = true
		FROM experiment_trials trial
		JOIN experiment_variants variant ON variant.id = trial.variant_id
		WHERE variant.experiment_id = $1 AND run.id = trial.pipeline_run_id AND run.status = $2
	`, experimentID, domain.OperationRunning)
	if err != nil {
		return err
	}
	if queued.RowsAffected()+running.RowsAffected() == 0 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE experiment_trials trial
		SET status = $2, error = $3, completed_at = now(), updated_at = now()
		FROM pipeline_runs run, experiment_variants variant
		WHERE variant.experiment_id = $1 AND trial.variant_id = variant.id AND trial.pipeline_run_id = run.id
		  AND run.status = $2 AND trial.status = $4
	`, experimentID, domain.OperationCanceled, message, domain.OperationQueued); err != nil {
		return err
	}
	nextStatus := domain.OperationCanceled
	if running.RowsAffected() > 0 {
		nextStatus = domain.OperationRunning
	}
	if _, err := tx.Exec(ctx, `UPDATE experiments SET status = $2, updated_at = now() WHERE id = $1`, experimentID, nextStatus); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func cancelableExperimentStatus(status string) bool {
	return status == domain.OperationQueued || status == domain.OperationRunning
}

func (s *Store) PipelineRunCount(ctx context.Context, pipelineID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pipeline_runs WHERE pipeline_id = $1`, pipelineID).Scan(&count)
	return count, err
}

func (s *Store) SyncExperimentPipelineRun(ctx context.Context, runID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var experimentID string
	err = tx.QueryRow(ctx, `
		UPDATE experiment_trials t
		SET status = r.status,
		    operation_id = (SELECT operation_id FROM pipeline_run_stages WHERE run_id = r.id AND operation_id IS NOT NULL ORDER BY position DESC LIMIT 1),
		    result_artifact_id = r.result_artifact_id,
		    error = r.error,
		    updated_at = now(),
		    completed_at = r.completed_at
		FROM pipeline_runs r, experiment_variants v
		WHERE t.pipeline_run_id = r.id AND r.id = $1 AND v.id = t.variant_id
		RETURNING v.experiment_id
	`, runID).Scan(&experimentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE experiments e
		SET status = summary.status,
		    updated_at = now()
		FROM (
			SELECT v.experiment_id,
			       CASE
			           WHEN bool_and(t.status = $2) THEN $2
			           WHEN bool_and(t.status IN ($2, $3, $4)) AND bool_or(t.status = $3) THEN $3
			           WHEN bool_and(t.status IN ($2, $3, $4)) THEN $4
			           WHEN bool_or(t.status <> $5) THEN $6
			           ELSE $5
			       END AS status
			FROM experiment_variants v
			JOIN experiment_trials t ON t.variant_id = v.id
			WHERE v.experiment_id = $1
			GROUP BY v.experiment_id
		) summary
		WHERE e.id = summary.experiment_id
	`, experimentID, domain.OperationSucceeded, domain.OperationFailed, domain.OperationCanceled, domain.OperationQueued, domain.OperationRunning)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
