package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateCompletedExperiment(ctx context.Context, experiment domain.Experiment) (domain.Experiment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Experiment{}, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `
		INSERT INTO experiments (id, workspace_id, configuration_id, owner_user_id, kind, name, description, status, result_type, result_version)
		VALUES ($1, $2, NULLIF($3, ''), $4, COALESCE(NULLIF($5, ''), 'comparison'), $6, $7, $8, $9, $10)
		RETURNING created_at, updated_at
	`, experiment.ID, experiment.WorkspaceID, experiment.ConfigurationID, experiment.OwnerUserID, experiment.Kind, experiment.Name, experiment.Description, experiment.Status, experiment.ResultType, experiment.ResultVersion).Scan(&experiment.CreatedAt, &experiment.UpdatedAt); err != nil {
		return domain.Experiment{}, err
	}
	for variantIndex := range experiment.Variants {
		variant := &experiment.Variants[variantIndex]
		if variant.Alias == "" {
			variant.Alias = variant.Name
		}
		if len(variant.Configuration) == 0 {
			variant.Configuration = json.RawMessage(`{}`)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO experiment_variants (id, experiment_id, position, name, display_alias, plot_color, configuration_id, configuration)
			VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), $4), NULLIF($6, ''), NULLIF($7, ''), $8)
		`, variant.ID, experiment.ID, variant.Position, variant.Name, variant.Alias, variant.Color, variant.ConfigurationID, variant.Configuration); err != nil {
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

func (s *Store) ListExperimentsForUser(ctx context.Context, userID string, limit int) ([]domain.Experiment, error) {
	return s.readListExperimentsForUser(ctx, userID, limit, false)
}

func (s *Store) ListExperimentsVisibleToUser(ctx context.Context, userID string, limit int) ([]domain.Experiment, error) {
	return s.readListExperimentsForUser(ctx, userID, limit, true)
}

func (s *Store) readListExperimentsForUser(ctx context.Context, userID string, limit int, shared bool) ([]domain.Experiment, error) {
	visibility := "$1 = '' OR experiment.owner_user_id = $1"
	if shared {
		visibility = "$1 = '' OR can_view_experiment($1, experiment.id)"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT experiment.id, experiment.workspace_id, COALESCE(experiment.configuration_id, ''), experiment.owner_user_id, owner.username,
		       experiment.kind, experiment.name, experiment.description, experiment.status, experiment.result_type, experiment.result_version, experiment.scheduled_for, experiment.created_at, experiment.updated_at,
               EXISTS (
                   SELECT 1 FROM experiment_variants variant
                   JOIN experiment_trials trial ON trial.variant_id = variant.id
                   JOIN pipeline_runs run ON run.id = trial.pipeline_run_id
                   WHERE variant.experiment_id = experiment.id AND run.cancel_requested
               )
		FROM experiments experiment
		JOIN users owner ON owner.id = experiment.owner_user_id
		WHERE `+visibility+`
		ORDER BY experiment.created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	result := []domain.Experiment{}
	for rows.Next() {
		var experiment domain.Experiment
		if err := rows.Scan(&experiment.ID, &experiment.WorkspaceID, &experiment.ConfigurationID, &experiment.OwnerUserID, &experiment.OwnerUsername, &experiment.Kind, &experiment.Name, &experiment.Description, &experiment.Status, &experiment.ResultType, &experiment.ResultVersion, &experiment.ScheduledFor, &experiment.CreatedAt, &experiment.UpdatedAt, &experiment.CancelRequested); err != nil {
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
	experimentIDs := make([]string, len(result))
	for index := range result {
		experimentIDs[index] = result[index].ID
	}
	variants, err := s.listExperimentVariants(ctx, experimentIDs)
	if err != nil {
		return nil, err
	}
	for index := range result {
		result[index].Variants = variants[result[index].ID]
	}
	return result, nil
}

func (s *Store) DeleteExperiment(ctx context.Context, experimentID string) error {
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
	if !deletableExperimentStatus(status) {
		return ErrConflict
	}
	var runIDs []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(DISTINCT trial.pipeline_run_id) FILTER (WHERE trial.pipeline_run_id IS NOT NULL), '{}') FROM experiment_trials trial JOIN experiment_variants variant ON variant.id=trial.variant_id WHERE variant.experiment_id=$1`, experimentID).Scan(&runIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO artifact_deletions(storage_key) SELECT storage_key FROM experiment_figures WHERE experiment_id=$1 ON CONFLICT DO NOTHING`, experimentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM experiments WHERE id = $1`, experimentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE target_id=$1`, experimentID); err != nil {
		return err
	}
	if err := deleteExperimentRuns(ctx, tx, runIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func deletableExperimentStatus(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

func (s *Store) listExperimentVariants(ctx context.Context, experimentIDs []string) (map[string][]domain.ExperimentVariant, error) {
	result := make(map[string][]domain.ExperimentVariant, len(experimentIDs))
	for _, experimentID := range experimentIDs {
		result[experimentID] = []domain.ExperimentVariant{}
	}
	if len(experimentIDs) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, experiment_id, position, name, display_alias, COALESCE(plot_color, ''), COALESCE(pipeline_id, ''), COALESCE(pipeline_hash, ''), COALESCE(configuration_id, ''), configuration
		FROM experiment_variants
		WHERE experiment_id = ANY($1::text[])
		ORDER BY experiment_id, position
	`, experimentIDs)
	if err != nil {
		return nil, err
	}
	variants := []domain.ExperimentVariant{}
	for rows.Next() {
		var variant domain.ExperimentVariant
		if err := rows.Scan(&variant.ID, &variant.ExperimentID, &variant.Position, &variant.Name, &variant.Alias, &variant.Color, &variant.PipelineID, &variant.PipelineHash, &variant.ConfigurationID, &variant.Configuration); err != nil {
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
	variantIDs := make([]string, len(variants))
	for index := range variants {
		variantIDs[index] = variants[index].ID
	}
	trials, err := s.listExperimentTrials(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	for _, variant := range variants {
		variant.Trials = trials[variant.ID]
		result[variant.ExperimentID] = append(result[variant.ExperimentID], variant)
	}
	return result, nil
}

func (s *Store) listExperimentTrials(ctx context.Context, variantIDs []string) (map[string][]domain.ExperimentTrial, error) {
	trials := make(map[string][]domain.ExperimentTrial, len(variantIDs))
	for _, variantID := range variantIDs {
		trials[variantID] = []domain.ExperimentTrial{}
	}
	if len(variantIDs) == 0 {
		return trials, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT trial.id, trial.variant_id, trial.position, trial.status, COALESCE(trial.operation_id, ''), COALESCE(trial.pipeline_run_id, ''), COALESCE(trial.result_artifact_id, ''), trial.error, trial.created_at, run.started_at, trial.completed_at
		FROM experiment_trials trial
		LEFT JOIN pipeline_runs run ON run.id = trial.pipeline_run_id
		WHERE trial.variant_id = ANY($1::text[])
		ORDER BY trial.variant_id, trial.position
	`, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var trial domain.ExperimentTrial
		if err := rows.Scan(&trial.ID, &trial.VariantID, &trial.Position, &trial.Status, &trial.OperationID, &trial.PipelineRunID, &trial.ResultArtifactID, &trial.Error, &trial.CreatedAt, &trial.StartedAt, &trial.CompletedAt); err != nil {
			return nil, err
		}
		trials[trial.VariantID] = append(trials[trial.VariantID], trial)
	}
	return trials, rows.Err()
}
