package storage

import (
	"context"
	"kubephos.dev/kubephos/internal/domain"
)

const comparisonCandidateSource = ` FROM experiments e JOIN users u ON u.id=e.owner_user_id
 WHERE ($1='' OR can_view_experiment($1,e.id)) AND e.kind <> 'comparison' AND e.status IN ('succeeded','failed','canceled')
 AND EXISTS(SELECT 1 FROM experiment_variants v JOIN experiment_trials t ON t.variant_id=v.id JOIN artifacts a ON a.id=t.result_artifact_id
 WHERE v.experiment_id=e.id AND t.status='succeeded' AND a.verified_at IS NOT NULL AND NOT a.sensitive AND a.artifact_type='TimeSeriesDataset' AND a.artifact_version='v1alpha1')`

func (s *Store) CountComparisonCandidates(ctx context.Context, viewer string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*)`+comparisonCandidateSource, viewer).Scan(&count)
	return count, err
}

// Picker queries are independent of the paginated experiment history and never
// load configurations or metric data. Visibility is applied before every filter.
func (s *Store) ListComparisonCandidates(ctx context.Context, viewer, owner, search string, offset int) (domain.ComparisonCandidatePage, error) {
	page := domain.ComparisonCandidatePage{Items: []domain.ComparisonCandidate{}, Owners: []domain.ComparisonCandidateOwner{}}
	filtered := comparisonCandidateSource + ` AND ($2='' OR e.owner_user_id=$2) AND strpos(lower(e.name),lower($3))>0`
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+filtered, viewer, owner, search).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.name,e.owner_user_id,u.username,e.created_at`+filtered+` ORDER BY e.created_at DESC,e.id LIMIT 40 OFFSET $4`, viewer, owner, search, offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var item domain.ComparisonCandidate
		if err := rows.Scan(&item.ID, &item.Name, &item.OwnerUserID, &item.OwnerUsername, &item.CreatedAt); err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	owners, err := s.pool.Query(ctx, `SELECT DISTINCT u.id,u.username`+comparisonCandidateSource+` ORDER BY u.username,u.id`, viewer)
	if err != nil {
		return page, err
	}
	defer owners.Close()
	for owners.Next() {
		var owner domain.ComparisonCandidateOwner
		if err := owners.Scan(&owner.ID, &owner.Username); err != nil {
			return page, err
		}
		page.Owners = append(page.Owners, owner)
	}
	return page, owners.Err()
}
