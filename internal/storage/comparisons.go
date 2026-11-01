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
	var raw []byte
	err := row.Scan(&item.ID, &item.Name, &item.OwnerUserID, &item.CreatedBy, &raw, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.ModifiedBy = item.CreatedBy
	err = json.Unmarshal(raw, &item.Series)
	return item, err
}

const comparisonColumns = `c.id,c.name,c.owner_user_id,u.username,c.series,c.created_at,c.updated_at`

// A direct lookup avoids the pagination of the experiment history endpoint.
func (s *Store) GetComparisonSource(ctx context.Context, id string) (domain.Experiment, error) {
	var item domain.Experiment
	err := s.pool.QueryRow(ctx, `SELECT id,workspace_id,name,status,kind,owner_user_id,created_at,updated_at FROM experiments WHERE id=$1`, id).Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Status, &item.Kind, &item.OwnerUserID, &item.CreatedAt, &item.UpdatedAt)
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

func (s *Store) SaveResultComparison(ctx context.Context, item domain.ResultComparison, update bool) (domain.ResultComparison, error) {
	raw, err := json.Marshal(item.Series)
	if err != nil {
		return item, err
	}
	if update {
		result, err := s.pool.Exec(ctx, `UPDATE result_comparisons SET name=$3,series=$4,updated_at=now() WHERE id=$1 AND owner_user_id=$2`, item.ID, item.OwnerUserID, item.Name, raw)
		if err != nil {
			return item, err
		}
		if result.RowsAffected() == 0 {
			return item, ErrNotFound
		}
	} else {
		_, err = s.pool.Exec(ctx, `INSERT INTO result_comparisons(id,owner_user_id,name,series) VALUES($1,$2,$3,$4)`, item.ID, item.OwnerUserID, item.Name, raw)
		if err != nil {
			return item, err
		}
	}
	return s.GetResultComparison(ctx, item.ID, item.OwnerUserID)
}

func (s *Store) DeleteResultComparison(ctx context.Context, id, owner string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM result_comparisons WHERE id=$1 AND owner_user_id=$2`, id, owner)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
