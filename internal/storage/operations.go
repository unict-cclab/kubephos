package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) CreateOperation(ctx context.Context, operation domain.Operation) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer tx.Rollback(ctx)
	if err := insertOperation(ctx, tx, &operation); err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

func insertOperation(ctx context.Context, tx pgx.Tx, operation *domain.Operation) error {
	plan, err := json.Marshal(operation.Plan)
	if err != nil {
		return err
	}
	validation, err := json.Marshal(operation.Validation)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO operations (id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $7 = $12 THEN now() ELSE NULL END)
		RETURNING created_at, queued_at
	`, operation.ID, operation.WorkspaceID, operation.PluginID, operation.PluginVersion, operation.PluginDigest, operation.Title, operation.Status, operation.Spec, plan, validation, operation.PlanHash, domain.OperationQueued).Scan(&operation.CreatedAt, &operation.QueuedAt)
	if err != nil {
		return err
	}
	operation.Steps = nil
	for position, planned := range operation.Plan.Steps {
		step := domain.OperationStep{ID: operation.ID + "_" + planned.ID, OperationID: operation.ID, Position: position + 1, Name: planned.Name, Status: domain.StepPending, Input: planned.Input}
		if _, err := tx.Exec(ctx, `
			INSERT INTO operation_steps (id, operation_id, position, name, status, input)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, step.ID, step.OperationID, step.Position, step.Name, step.Status, step.Input); err != nil {
			return err
		}
		operation.Steps = append(operation.Steps, step)
	}
	message := fmt.Sprintf("Validation passed. Plan %s is ready for confirmation.", operation.PlanHash[:12])
	source := "validation"
	if operation.Status == domain.OperationQueued {
		message = fmt.Sprintf("Validation passed. Plan %s was queued by the managed resource workflow.", operation.PlanHash[:12])
		source = "queue"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, level, source, message)
		VALUES ($1, 'info', $2, $3)
	`, operation.ID, source, message)
	return err
}

func (s *Store) CreateManagedResource(ctx context.Context, resource domain.ManagedResource, operation domain.Operation) (domain.ManagedResource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ManagedResource{}, err
	}
	defer tx.Rollback(ctx)
	if err := insertOperation(ctx, tx, &operation); err != nil {
		return domain.ManagedResource{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO managed_resources (id, workspace_id, name, kind, provider, connection_id, plugin_id, plugin_version, plugin_digest, spec, operation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at, updated_at
	`, resource.ID, resource.WorkspaceID, resource.Name, resource.Kind, resource.Provider, resource.ConnectionID, resource.PluginID, resource.PluginVersion, resource.PluginDigest, resource.Spec, operation.ID).Scan(&resource.CreatedAt, &resource.UpdatedAt)
	if err != nil {
		return domain.ManagedResource{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ManagedResource{}, err
	}
	resource.OperationID = operation.ID
	resource.Status = "pending"
	resource.Validation = operation.Validation
	return resource, nil
}

func (s *Store) CreateManagedPipelineResource(ctx context.Context, resource domain.ManagedResource, pipeline domain.Pipeline, run domain.PipelineRun, dependencies []domain.ManagedResourceDependency) (domain.ManagedResource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ManagedResource{}, err
	}
	defer tx.Rollback(ctx)
	if err := insertPipelineLifecycle(ctx, tx, &pipeline, &run); err != nil {
		return domain.ManagedResource{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO managed_resources (id, workspace_id, name, kind, provider, connection_id, plugin_id, plugin_version, plugin_digest, spec, pipeline_id, pipeline_run_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING created_at, updated_at
	`, resource.ID, resource.WorkspaceID, resource.Name, resource.Kind, resource.Provider, resource.ConnectionID, resource.PluginID, resource.PluginVersion, resource.PluginDigest, resource.Spec, pipeline.ID, run.ID).Scan(&resource.CreatedAt, &resource.UpdatedAt)
	if err != nil {
		return domain.ManagedResource{}, err
	}
	for _, dependency := range dependencies {
		if dependency.ResourceID != resource.ID || dependency.DependsOnID == "" || dependency.Relation == "" {
			return domain.ManagedResource{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO managed_resource_dependencies (resource_id, depends_on_id, relation)
			VALUES ($1, $2, $3)
		`, dependency.ResourceID, dependency.DependsOnID, dependency.Relation); err != nil {
			return domain.ManagedResource{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ManagedResource{}, err
	}
	resource.PipelineID = pipeline.ID
	resource.PipelineRunID = run.ID
	resource.Status = "provisioning"
	resource.Validation = pipeline.Validation
	return resource, nil
}

func insertPipelineLifecycle(ctx context.Context, tx pgx.Tx, pipeline *domain.Pipeline, run *domain.PipelineRun) error {
	definition, err := json.Marshal(pipeline.Definition)
	if err != nil {
		return err
	}
	resolution, err := json.Marshal(pipeline.Resolution)
	if err != nil {
		return err
	}
	validation, err := json.Marshal(pipeline.Validation)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO pipelines (id, workspace_id, name, description, definition, resolution, validation, pipeline_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at
	`, pipeline.ID, pipeline.WorkspaceID, pipeline.Name, pipeline.Description, definition, resolution, validation, pipeline.Hash).Scan(&pipeline.CreatedAt, &pipeline.UpdatedAt)
	if err != nil {
		return err
	}
	if run.PipelineID != pipeline.ID || run.WorkspaceID != pipeline.WorkspaceID || run.PipelineHash != pipeline.Hash || run.ResultType != pipeline.Resolution.Result.Type || run.ResultVersion != pipeline.Resolution.Result.Version {
		return ErrConflict
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, workspace_id, name, status, pipeline_hash, result_type, result_version, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		RETURNING created_at, queued_at, scheduled_for
	`, run.ID, run.PipelineID, run.WorkspaceID, run.Name, domain.OperationQueued, run.PipelineHash, run.ResultType, run.ResultVersion, run.ScheduledFor).Scan(&run.CreatedAt, &run.QueuedAt, &run.ScheduledFor)
	if err != nil {
		return err
	}
	for index := range run.Stages {
		stage := &run.Stages[index]
		stage.Status = domain.StepPending
		if _, err := tx.Exec(ctx, `
			INSERT INTO pipeline_run_stages (id, run_id, position, stage_id, plugin_id, title, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, stage.ID, run.ID, stage.Position, stage.StageID, stage.PluginID, stage.Title, stage.Status); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListManagedResources(ctx context.Context, kind string) ([]domain.ManagedResource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT resource.id, resource.workspace_id, resource.name, resource.kind, resource.provider, COALESCE(resource.connection_id, ''),
		       resource.plugin_id, resource.plugin_version, resource.plugin_digest, resource.spec, COALESCE(resource.operation_id, ''),
		       COALESCE(resource.pipeline_id, ''), COALESCE(resource.pipeline_run_id, ''),
		       COALESCE((SELECT artifact.id FROM artifacts artifact WHERE artifact.operation_id = resource.operation_id AND artifact.artifact_type <> 'RunResult' ORDER BY artifact.created_at LIMIT 1), creation_run.result_artifact_id, ''),
		       COALESCE(resource.deletion_operation_id, ''), COALESCE(resource.deletion_pipeline_id, ''), COALESCE(resource.deletion_pipeline_run_id, ''),
		       COALESCE(creation.status, creation_run.status), COALESCE(creation.error, creation_run.error, ''), COALESCE(creation.validation, creation_pipeline.validation),
		       COALESCE(deletion.status, deletion_run.status, ''), COALESCE(deletion.error, deletion_run.error, ''),
		       COALESCE(resource.recreation_pipeline_id, ''), COALESCE(resource.recreation_pipeline_run_id, ''), COALESCE(recreation_run.status, ''), COALESCE(recreation_run.error, ''), COALESCE(recreation_run.name, ''),
		       resource.created_at, resource.updated_at
		FROM managed_resources resource
		LEFT JOIN operations creation ON creation.id = resource.operation_id
		LEFT JOIN pipelines creation_pipeline ON creation_pipeline.id = resource.pipeline_id
		LEFT JOIN pipeline_runs creation_run ON creation_run.id = resource.pipeline_run_id
		LEFT JOIN operations deletion ON deletion.id = resource.deletion_operation_id
		LEFT JOIN pipeline_runs deletion_run ON deletion_run.id = resource.deletion_pipeline_run_id
		LEFT JOIN pipeline_runs recreation_run ON recreation_run.id = resource.recreation_pipeline_run_id
		WHERE resource.kind = $1
		  AND COALESCE(deletion.status, deletion_run.status, '') <> $2
		ORDER BY resource.created_at DESC
	`, kind, domain.OperationSucceeded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ManagedResource{}
	for rows.Next() {
		resource, err := scanManagedResource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, resource)
	}
	return result, rows.Err()
}

func (s *Store) GetManagedResource(ctx context.Context, resourceID string) (domain.ManagedResource, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT resource.id, resource.workspace_id, resource.name, resource.kind, resource.provider, COALESCE(resource.connection_id, ''),
		       resource.plugin_id, resource.plugin_version, resource.plugin_digest, resource.spec, COALESCE(resource.operation_id, ''),
		       COALESCE(resource.pipeline_id, ''), COALESCE(resource.pipeline_run_id, ''),
		       COALESCE((SELECT artifact.id FROM artifacts artifact WHERE artifact.operation_id = resource.operation_id AND artifact.artifact_type <> 'RunResult' ORDER BY artifact.created_at LIMIT 1), creation_run.result_artifact_id, ''),
		       COALESCE(resource.deletion_operation_id, ''), COALESCE(resource.deletion_pipeline_id, ''), COALESCE(resource.deletion_pipeline_run_id, ''),
		       COALESCE(creation.status, creation_run.status), COALESCE(creation.error, creation_run.error, ''), COALESCE(creation.validation, creation_pipeline.validation),
		       COALESCE(deletion.status, deletion_run.status, ''), COALESCE(deletion.error, deletion_run.error, ''),
		       COALESCE(resource.recreation_pipeline_id, ''), COALESCE(resource.recreation_pipeline_run_id, ''), COALESCE(recreation_run.status, ''), COALESCE(recreation_run.error, ''), COALESCE(recreation_run.name, ''),
		       resource.created_at, resource.updated_at
		FROM managed_resources resource
		LEFT JOIN operations creation ON creation.id = resource.operation_id
		LEFT JOIN pipelines creation_pipeline ON creation_pipeline.id = resource.pipeline_id
		LEFT JOIN pipeline_runs creation_run ON creation_run.id = resource.pipeline_run_id
		LEFT JOIN operations deletion ON deletion.id = resource.deletion_operation_id
		LEFT JOIN pipeline_runs deletion_run ON deletion_run.id = resource.deletion_pipeline_run_id
		LEFT JOIN pipeline_runs recreation_run ON recreation_run.id = resource.recreation_pipeline_run_id
		WHERE resource.id = $1
	`, resourceID)
	resource, err := scanManagedResource(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ManagedResource{}, ErrNotFound
	}
	return resource, err
}

func scanManagedResource(row pgx.Row) (domain.ManagedResource, error) {
	var resource domain.ManagedResource
	var creationStatus, creationError, deletionStatus, deletionError, recreationStatus, recreationError, recreationName string
	var validation []byte
	err := row.Scan(&resource.ID, &resource.WorkspaceID, &resource.Name, &resource.Kind, &resource.Provider, &resource.ConnectionID,
		&resource.PluginID, &resource.PluginVersion, &resource.PluginDigest, &resource.Spec, &resource.OperationID, &resource.PipelineID, &resource.PipelineRunID, &resource.ArtifactID,
		&resource.DeletionOperationID, &resource.DeletionPipelineID, &resource.DeletionPipelineRunID, &creationStatus, &creationError, &validation, &deletionStatus, &deletionError,
		&resource.RecreationPipelineID, &resource.RecreationPipelineRunID, &recreationStatus, &recreationError, &recreationName, &resource.CreatedAt, &resource.UpdatedAt)
	if err != nil {
		return domain.ManagedResource{}, err
	}
	if err := json.Unmarshal(validation, &resource.Validation); err != nil {
		return domain.ManagedResource{}, err
	}
	resource.Status, resource.Error = managedResourceState(creationStatus, creationError, deletionStatus, deletionError, recreationStatus, recreationError)
	if strings.HasPrefix(recreationName, "Maintain ") {
		if resource.Status == "recreating" {
			resource.Status = "maintaining"
		}
		if resource.Status == "recreation-failed" {
			resource.Status = "maintenance-failed"
		}
	}
	return resource, nil
}

func managedResourceState(creationStatus, creationError, deletionStatus, deletionError, recreationStatus, recreationError string) (string, string) {
	if deletionStatus != "" {
		switch deletionStatus {
		case domain.OperationSucceeded:
			return "deleted", ""
		case domain.OperationFailed, domain.OperationCanceled:
			return "deletion-failed", deletionError
		default:
			return "deleting", ""
		}
	}
	if recreationStatus != "" {
		switch recreationStatus {
		case domain.OperationFailed, domain.OperationCanceled:
			return "recreation-failed", recreationError
		default:
			return "recreating", ""
		}
	}
	switch creationStatus {
	case domain.OperationSucceeded:
		return "ready", ""
	case domain.OperationFailed, domain.OperationCanceled:
		return "failed", creationError
	default:
		return "provisioning", ""
	}
}

func (s *Store) CreateManagedResourceDeletion(ctx context.Context, resourceID string, operation domain.Operation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var creationStatus, deletionID string
	err = tx.QueryRow(ctx, `
		SELECT creation.status, COALESCE(resource.deletion_operation_id, '')
		FROM managed_resources resource
		JOIN operations creation ON creation.id = resource.operation_id
		WHERE resource.id = $1
		FOR UPDATE OF resource
	`, resourceID).Scan(&creationStatus, &deletionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if creationStatus != domain.OperationSucceeded && creationStatus != domain.OperationFailed && creationStatus != domain.OperationCanceled || deletionID != "" {
		return ErrConflict
	}
	var used bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM managed_resource_dependencies dependency
			JOIN managed_resources consumer ON consumer.id = dependency.resource_id
			LEFT JOIN operations deletion ON deletion.id = consumer.deletion_operation_id
			LEFT JOIN pipeline_runs deletion_run ON deletion_run.id = consumer.deletion_pipeline_run_id
			WHERE dependency.depends_on_id = $1
			  AND COALESCE(deletion.status, deletion_run.status, '') <> $2
		)
	`, resourceID, domain.OperationSucceeded).Scan(&used)
	if err != nil {
		return err
	}
	if used {
		return ErrConflict
	}
	if err := insertOperation(ctx, tx, &operation); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE managed_resources SET deletion_operation_id = $2, updated_at = now() WHERE id = $1`, resourceID, operation.ID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateManagedPipelineResourceDeletion(ctx context.Context, resourceID string, pipeline domain.Pipeline, run domain.PipelineRun) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var creationStatus, deletionOperationID, deletionRunID string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(creation.status, creation_run.status), COALESCE(resource.deletion_operation_id, ''), COALESCE(resource.deletion_pipeline_run_id, '')
		FROM managed_resources resource
		LEFT JOIN operations creation ON creation.id = resource.operation_id
		LEFT JOIN pipeline_runs creation_run ON creation_run.id = resource.pipeline_run_id
		WHERE resource.id = $1
		FOR UPDATE OF resource
	`, resourceID).Scan(&creationStatus, &deletionOperationID, &deletionRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if creationStatus != domain.OperationSucceeded && creationStatus != domain.OperationFailed && creationStatus != domain.OperationCanceled || deletionOperationID != "" || deletionRunID != "" {
		return ErrConflict
	}
	var used bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM managed_resource_dependencies dependency
			JOIN managed_resources consumer ON consumer.id = dependency.resource_id
			LEFT JOIN operations deletion ON deletion.id = consumer.deletion_operation_id
			LEFT JOIN pipeline_runs deletion_run ON deletion_run.id = consumer.deletion_pipeline_run_id
			WHERE dependency.depends_on_id = $1
			  AND COALESCE(deletion.status, deletion_run.status, '') <> $2
		)
	`, resourceID, domain.OperationSucceeded).Scan(&used)
	if err != nil {
		return err
	}
	if used {
		return ErrConflict
	}
	if err := insertPipelineLifecycle(ctx, tx, &pipeline, &run); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE managed_resources
		SET deletion_pipeline_id = $2, deletion_pipeline_run_id = $3, updated_at = now()
		WHERE id = $1
	`, resourceID, pipeline.ID, run.ID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) ResetFailedManagedResourceDeletion(ctx context.Context, resourceID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE managed_resources resource
		SET deletion_operation_id = NULL, deletion_pipeline_id = NULL, deletion_pipeline_run_id = NULL, updated_at = now()
		WHERE resource.id = $1
		  AND (
		      resource.deletion_operation_id IS NOT NULL
		      AND EXISTS (
		          SELECT 1 FROM operations deletion
		          WHERE deletion.id = resource.deletion_operation_id AND deletion.status IN ($2, $3)
		      )
		      OR resource.deletion_pipeline_run_id IS NOT NULL
		      AND EXISTS (
		          SELECT 1 FROM pipeline_runs deletion_run
		          WHERE deletion_run.id = resource.deletion_pipeline_run_id AND deletion_run.status IN ($2, $3)
		      )
		  )
	`, resourceID, domain.OperationFailed, domain.OperationCanceled)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CreateManagedResourceRecreation(ctx context.Context, resourceID, expectedRunID string, desiredSpec json.RawMessage, pipeline domain.Pipeline, run domain.PipelineRun) error {
	return s.createManagedResourceRecreation(ctx, resourceID, expectedRunID, desiredSpec, pipeline, run, false)
}

func (s *Store) RetryManagedClusterMesh(ctx context.Context, resourceID, expectedRunID string, desiredSpec json.RawMessage, pipeline domain.Pipeline, run domain.PipelineRun) error {
	return s.createManagedResourceRecreation(ctx, resourceID, expectedRunID, desiredSpec, pipeline, run, true)
}

func (s *Store) createManagedResourceRecreation(ctx context.Context, resourceID, expectedRunID string, desiredSpec json.RawMessage, pipeline domain.Pipeline, run domain.PipelineRun, reuseCompleted bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind, creationStatus, creationRunID, recreationRunID, recreationStatus string
	err = tx.QueryRow(ctx, `
		SELECT resource.kind, COALESCE(creation.status, creation_run.status), COALESCE(resource.pipeline_run_id, ''),
		       COALESCE(resource.recreation_pipeline_run_id, ''), COALESCE(recreation.status, '')
		FROM managed_resources resource
		LEFT JOIN operations creation ON creation.id = resource.operation_id
		LEFT JOIN pipeline_runs creation_run ON creation_run.id = resource.pipeline_run_id
		LEFT JOIN pipeline_runs recreation ON recreation.id = resource.recreation_pipeline_run_id
		WHERE resource.id = $1
		  AND resource.deleted_at IS NULL
		  AND resource.deletion_operation_id IS NULL
		  AND resource.deletion_pipeline_run_id IS NULL
		FOR UPDATE OF resource
	`, resourceID).Scan(&kind, &creationStatus, &creationRunID, &recreationRunID, &recreationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	currentRunID := creationRunID
	if recreationRunID != "" {
		currentRunID = recreationRunID
		if recreationStatus != domain.OperationFailed && recreationStatus != domain.OperationCanceled {
			return ErrConflict
		}
	}
	if kind != "kubernetes-cluster" || expectedRunID == "" || currentRunID != expectedRunID ||
		creationStatus != domain.OperationSucceeded && creationStatus != domain.OperationFailed && creationStatus != domain.OperationCanceled {
		return ErrConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pipeline_runs
			WHERE cluster_resource_id = $1 AND status IN ($2, $3)
        ) OR EXISTS (
          SELECT 1 FROM experiments e
          JOIN experiment_variants v ON v.experiment_id=e.id
          LEFT JOIN experiment_configurations c ON c.id=v.configuration_id
          LEFT JOIN experiment_trials t ON t.variant_id=v.id
          LEFT JOIN pipeline_runs r ON r.id=t.pipeline_run_id
          WHERE e.status IN ('queued','running') AND (c.cluster_resource_id=$1 OR r.cluster_resource_id=$1)
        )
	`, resourceID, domain.OperationQueued, domain.OperationRunning).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	if err := insertPipelineLifecycle(ctx, tx, &pipeline, &run); err != nil {
		return err
	}
	if reuseCompleted {
		var reusable bool
		if err := tx.QueryRow(ctx, `
			SELECT count(*) > 1 AND count(*) = $3
			 AND (SELECT count(*) FROM pipeline_run_stages WHERE run_id=$1) = $3
			 AND (SELECT status FROM pipeline_runs WHERE id=$1) IN ('failed','canceled')
			 AND bool_and(old.stage_id = current.stage_id AND old.plugin_id = current.plugin_id)
			 AND bool_and(CASE WHEN old.position = $3 THEN old.stage_id = 'mesh'
			   AND old.plugin_id = 'io.kubephos.cluster.cilium.mesh' AND old.status IN ('failed','canceled')
			   ELSE old.status = 'succeeded' AND COALESCE(operation.status = 'succeeded' AND operation.workspace_id = $4, false) END)
			FROM pipeline_run_stages old
			JOIN pipeline_run_stages current ON current.run_id = $2 AND current.position = old.position
			LEFT JOIN operations operation ON operation.id = old.operation_id
			WHERE old.run_id = $1
		`, expectedRunID, run.ID, len(run.Stages), pipeline.WorkspaceID).Scan(&reusable); err != nil {
			return err
		}
		if !reusable {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pipeline_run_stages current SET status = old.status, operation_id = old.operation_id,
			 spec = old.spec, started_at = old.started_at, completed_at = old.completed_at
			FROM pipeline_run_stages old WHERE current.run_id = $1 AND old.run_id = $2
			 AND current.position = old.position AND old.position < $3
		`, run.ID, expectedRunID, len(run.Stages)); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE managed_resources
		SET spec = $2, recreation_pipeline_id = $3, recreation_pipeline_run_id = $4, updated_at = now()
		WHERE id = $1
	`, resourceID, desiredSpec, pipeline.ID, run.ID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteFailedManagedResource(ctx context.Context, resourceID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(creation.status, creation_run.status)
		FROM managed_resources resource
		LEFT JOIN operations creation ON creation.id = resource.operation_id
		LEFT JOIN pipeline_runs creation_run ON creation_run.id = resource.pipeline_run_id
		WHERE resource.id = $1
		  AND resource.deletion_operation_id IS NULL
		  AND resource.deletion_pipeline_run_id IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM pipeline_run_stages stage
		      WHERE stage.run_id = resource.pipeline_run_id AND stage.operation_id IS NOT NULL
		  )
		FOR UPDATE OF resource
	`, resourceID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if status != domain.OperationFailed && status != domain.OperationCanceled {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM managed_resources WHERE id = $1`, resourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

func (s *Store) ListOperationsForUser(ctx context.Context, userID string, limit int) ([]domain.Operation, error) {
	return s.readListOperationsForUser(ctx, userID, limit, false)
}

func (s *Store) ListOperationsVisibleToUser(ctx context.Context, userID string, limit int) ([]domain.Operation, error) {
	return s.readListOperationsForUser(ctx, userID, limit, true)
}

func (s *Store) readListOperationsForUser(ctx context.Context, userID string, limit int, shared bool) ([]domain.Operation, error) {
	visibility := "run.owner_user_id = $1"
	extraVisibility := ""
	if shared {
		visibility = "can_view_run($1, run.id)"
		extraVisibility = " OR can_view_operation($1,operations.id)"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, plugin_id, plugin_version, plugin_digest, title, status, spec, plan, validation, plan_hash,
		       cancel_requested, error, created_at, queued_at, started_at, completed_at
		FROM operations
		WHERE $1 = '' OR EXISTS (
			SELECT 1 FROM pipeline_run_stages stage
			JOIN pipeline_runs run ON run.id = stage.run_id
			WHERE `+visibility+` AND (stage.operation_id = operations.id OR stage.cleanup_operation_id = operations.id)
		)
`+extraVisibility+`
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE operations
		SET status = $2, completed_at = now(), lease_owner = NULL, lease_until = NULL
		WHERE id = $1
	`, operationID, domain.OperationSucceeded)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE managed_resources
		SET deleted_at = now(), updated_at = now()
		WHERE deletion_operation_id = $1 AND deleted_at IS NULL
	`, operationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operation_logs (operation_id, step_id, level, source, message)
		VALUES ($1, NULL, 'info', 'engine', 'Operation completed after all health gates passed.')
	`, operationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
