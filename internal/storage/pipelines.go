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
		INSERT INTO pipeline_runs (id, pipeline_id, workspace_id, name, status, pipeline_hash, result_type, result_version, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		RETURNING created_at, queued_at, scheduled_for
	`, run.ID, run.PipelineID, run.WorkspaceID, run.Name, domain.OperationQueued, run.PipelineHash, run.ResultType, run.ResultVersion, run.ScheduledFor).Scan(&run.CreatedAt, &run.QueuedAt, &run.ScheduledFor)
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
		INSERT INTO experiments (id, workspace_id, name, description, status, result_type, result_version, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at
	`, experiment.ID, experiment.WorkspaceID, experiment.Name, experiment.Description, domain.OperationQueued, experiment.ResultType, experiment.ResultVersion, experiment.ScheduledFor).Scan(&experiment.CreatedAt, &experiment.UpdatedAt)
	if err != nil {
		return domain.Experiment{}, err
	}
	experiment.Status = domain.OperationQueued
	for variantIndex := range experiment.Variants {
		variant := &experiment.Variants[variantIndex]
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
			INSERT INTO experiment_variants (id, experiment_id, position, name, pipeline_id, pipeline_hash, configuration)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, variant.ID, experiment.ID, variant.Position, variant.Name, variant.PipelineID, variant.PipelineHash, variant.Configuration)
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
				INSERT INTO pipeline_runs (id, pipeline_id, workspace_id, name, status, pipeline_hash, result_type, result_version, scheduled_for)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
				RETURNING created_at, queued_at, scheduled_for
			`, run.ID, run.PipelineID, run.WorkspaceID, run.Name, domain.OperationQueued, run.PipelineHash, run.ResultType, run.ResultVersion, run.ScheduledFor).Scan(&run.CreatedAt, &run.QueuedAt, &run.ScheduledFor)
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
		SELECT id, pipeline_id, workspace_id, name, status, pipeline_hash, result_type, result_version,
		       COALESCE(result_artifact_id, ''), cancel_requested, error, created_at, queued_at, scheduled_for, started_at, completed_at
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
		SELECT id, pipeline_id, workspace_id, name, status, pipeline_hash, result_type, result_version,
		       COALESCE(result_artifact_id, ''), cancel_requested, error, created_at, queued_at, scheduled_for, started_at, completed_at
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

func scanPipelineRun(row scanner) (domain.PipelineRun, error) {
	var run domain.PipelineRun
	err := row.Scan(&run.ID, &run.PipelineID, &run.WorkspaceID, &run.Name, &run.Status, &run.PipelineHash, &run.ResultType, &run.ResultVersion, &run.ResultArtifactID, &run.CancelRequested, &run.Error, &run.CreatedAt, &run.QueuedAt, &run.ScheduledFor, &run.StartedAt, &run.CompletedAt)
	return run, err
}

func (s *Store) listPipelineRunStages(ctx context.Context, runID string) ([]domain.PipelineRunStage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, position, stage_id, plugin_id, title, status, COALESCE(operation_id, ''), spec, error, started_at, completed_at
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
		if err := rows.Scan(&stage.ID, &stage.RunID, &stage.Position, &stage.StageID, &stage.PluginID, &stage.Title, &stage.Status, &stage.OperationID, &stage.Spec, &stage.Error, &stage.StartedAt, &stage.CompletedAt); err != nil {
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
			SELECT id
			FROM pipeline_runs
			WHERE status IN ($1, $2) AND scheduled_for <= now() AND (lease_until IS NULL OR lease_until < now())
			ORDER BY scheduled_for, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE pipeline_runs r
		SET status = $2, started_at = COALESCE(started_at, now()), lease_owner = $3, lease_until = now() + interval '15 seconds'
		FROM candidate
		WHERE r.id = candidate.id
		RETURNING r.id
	`, domain.OperationQueued, domain.OperationRunning, owner).Scan(&runID)
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
	command, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs
		SET status = $3, result_artifact_id = $4, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND lease_owner = $2 AND status = $5
		  AND NOT EXISTS (SELECT 1 FROM pipeline_run_stages WHERE run_id = $1 AND status <> $6)
	`, runID, owner, domain.OperationSucceeded, artifactID, domain.OperationRunning, domain.StepSucceeded)
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
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_runs
		SET status = $3, error = $4, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND lease_owner = $2 AND status = $5
	`, runID, owner, status, message, domain.OperationRunning)
	return err
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
		    operation_id = (SELECT operation_id FROM pipeline_run_stages WHERE run_id = r.id ORDER BY position DESC LIMIT 1),
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
