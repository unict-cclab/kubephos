package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

func scanComparison(row pgx.Row) (domain.ResultComparison, error) {
	var item domain.ResultComparison
	var raw, snapshot []byte
	err := row.Scan(&item.ID, &item.Name, &item.OwnerUserID, &item.CreatedBy, &raw, &snapshot, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.ModifiedBy = item.CreatedBy
	if err = json.Unmarshal(raw, &item.Series); err != nil {
		return item, err
	}
	if len(snapshot) > 0 {
		var frozen domain.ComparisonSnapshot
		if err := json.Unmarshal(snapshot, &frozen); err != nil {
			return item, err
		}
		item.Snapshot = &frozen
		capturedAt := frozen.CapturedAt
		item.SnapshotCapturedAt = &capturedAt
	}
	return item, nil
}

const comparisonColumns = `c.id,c.name,c.owner_user_id,u.username,c.series,c.snapshot,c.created_at,c.updated_at`

// comparisonSnapshotArtifacts lists the result artifacts frozen by a snapshot
// with their source experiment. Only sources owned by the comparison owner are
// retained after deletion: deleting an experiment revokes it from every other
// user's comparisons, exactly like unsharing it.
func comparisonSnapshotArtifacts(snapshot *domain.ComparisonSnapshot, owner string) (artifacts, experiments []string) {
	if snapshot == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, source := range snapshot.Sources {
		if source.OwnerUserID != owner {
			continue
		}
		for _, variant := range source.Variants {
			for _, trial := range variant.Trials {
				if trial.ResultArtifactID != "" && !seen[trial.ResultArtifactID] {
					seen[trial.ResultArtifactID] = true
					artifacts = append(artifacts, trial.ResultArtifactID)
					experiments = append(experiments, source.ID)
				}
			}
		}
	}
	return artifacts, experiments
}

// replaceComparisonArtifacts records the artifacts a snapshot freezes and
// releases those it no longer references.
func replaceComparisonArtifacts(ctx context.Context, tx pgx.Tx, comparisonID, owner string, snapshot *domain.ComparisonSnapshot) error {
	var previous []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(artifact_id),'{}') FROM result_comparison_artifacts WHERE comparison_id=$1`, comparisonID).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM result_comparison_artifacts WHERE comparison_id=$1`, comparisonID); err != nil {
		return err
	}
	artifacts, experiments := comparisonSnapshotArtifacts(snapshot, owner)
	if len(artifacts) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO result_comparison_artifacts(comparison_id,artifact_id,experiment_id) SELECT $1,a,e FROM unnest($2::text[],$3::text[]) AS refs(a,e) ON CONFLICT DO NOTHING`, comparisonID, artifacts, experiments); err != nil {
			return err
		}
	}
	return purgeReleasedComparisonArtifacts(ctx, tx, previous)
}

// ComparisonArtifactRetained reports whether the viewer owns a comparison whose
// snapshot froze this artifact of their own experiment after it was deleted.
// Sources that still exist keep their own sharing rules.
func (s *Store) ComparisonArtifactRetained(ctx context.Context, viewer, artifactID string) (bool, error) {
	var retained bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM result_comparison_artifacts r JOIN result_comparisons c ON c.id=r.comparison_id
 WHERE r.artifact_id=$2 AND c.owner_user_id=$1 AND NOT EXISTS(SELECT 1 FROM experiments e WHERE e.id=r.experiment_id))`, viewer, artifactID).Scan(&retained)
	return retained, err
}

// ExistingExperimentIDs reports which of the given experiments still exist, so
// comparisons can flag sources deleted after they were saved.
func (s *Store) ExistingExperimentIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	existing := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return existing, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM experiments WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

// A direct lookup avoids the pagination of the experiment history endpoint.
func (s *Store) GetComparisonSource(ctx context.Context, id string) (domain.Experiment, error) {
	var item domain.Experiment
	err := s.pool.QueryRow(ctx, `SELECT id,workspace_id,name,status,kind,owner_user_id,created_at,updated_at,rest_seconds_between_trials,first_trial_pilot FROM experiments WHERE id=$1`, id).Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Status, &item.Kind, &item.OwnerUserID, &item.CreatedAt, &item.UpdatedAt, &item.RestSecondsBetweenTrials, &item.FirstTrialPilot)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	variants, err := s.listExperimentVariants(ctx, []string{id})
	item.Variants = variants[id]
	return item, err
}

func (s *Store) ListResultComparisons(ctx context.Context, owner string) ([]domain.ResultComparison, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+comparisonColumns+` FROM result_comparisons c JOIN users u ON u.id=c.owner_user_id WHERE c.owner_user_id=$1 ORDER BY c.updated_at DESC,c.id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ResultComparison{}
	for rows.Next() {
		item, err := scanComparison(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetResultComparison(ctx context.Context, id, owner string) (domain.ResultComparison, error) {
	return scanComparison(s.pool.QueryRow(ctx, `SELECT `+comparisonColumns+` FROM result_comparisons c JOIN users u ON u.id=c.owner_user_id WHERE c.id=$1 AND c.owner_user_id=$2`, id, owner))
}

// SaveResultComparison stores the selection and, when given, its frozen
// snapshot. The artifacts referenced by the snapshot are protected from
// deletion together with their experiment.
func (s *Store) SaveResultComparison(ctx context.Context, item domain.ResultComparison, update bool) (domain.ResultComparison, error) {
	raw, err := json.Marshal(item.Series)
	if err != nil {
		return item, err
	}
	var snapshot []byte
	if item.Snapshot != nil {
		if snapshot, err = json.Marshal(item.Snapshot); err != nil {
			return item, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return item, err
	}
	defer tx.Rollback(ctx)
	if update {
		result, err := tx.Exec(ctx, `UPDATE result_comparisons SET name=$3,series=$4,snapshot=COALESCE($5::jsonb,snapshot),updated_at=now() WHERE id=$1 AND owner_user_id=$2`, item.ID, item.OwnerUserID, item.Name, raw, snapshot)
		if err != nil {
			return item, err
		}
		if result.RowsAffected() == 0 {
			return item, ErrNotFound
		}
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO result_comparisons(id,owner_user_id,name,series,snapshot) VALUES($1,$2,$3,$4,$5::jsonb)`, item.ID, item.OwnerUserID, item.Name, raw, snapshot)
		if err != nil {
			return item, err
		}
	}
	if item.Snapshot != nil {
		if err := replaceComparisonArtifacts(ctx, tx, item.ID, item.OwnerUserID, item.Snapshot); err != nil {
			return item, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return item, err
	}
	return s.GetResultComparison(ctx, item.ID, item.OwnerUserID)
}

func (s *Store) DeleteResultComparison(ctx context.Context, id, owner string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var released []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(r.artifact_id),'{}') FROM result_comparison_artifacts r JOIN result_comparisons c ON c.id=r.comparison_id WHERE c.id=$1 AND c.owner_user_id=$2`, id, owner).Scan(&released); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM result_comparisons WHERE id=$1 AND owner_user_id=$2`, id, owner)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := purgeReleasedComparisonArtifacts(ctx, tx, released); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
