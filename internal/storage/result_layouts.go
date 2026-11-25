package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ResultScope names the results a layout arranges: an experiment or a saved comparison.
type ResultScope struct {
	Comparison bool
	ID         string
}

func (scope ResultScope) column() string {
	if scope.Comparison {
		return "comparison_id"
	}
	return "experiment_id"
}

// ResultLayout is the user's arrangement of the scope's result charts; ErrNotFound
// when the user has not changed the default one.
func (s *Store) ResultLayout(ctx context.Context, userID string, scope ResultScope) (json.RawMessage, error) {
	var layout json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT layout FROM result_layouts WHERE user_id=$1 AND `+scope.column()+`=$2`, userID, scope.ID).Scan(&layout)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return layout, err
}

// SaveResultLayout stores the user's layout; ErrNotFound when the results no longer exist.
func (s *Store) SaveResultLayout(ctx context.Context, userID string, scope ResultScope, layout json.RawMessage) error {
	column := scope.column()
	_, err := s.pool.Exec(ctx, `INSERT INTO result_layouts(user_id,`+column+`,layout) VALUES($1,$2,$3)
 ON CONFLICT (user_id,`+column+`) WHERE `+column+` IS NOT NULL DO UPDATE SET layout=excluded.layout,updated_at=now()`, userID, scope.ID, layout)
	if foreignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}

// DeleteResultLayout returns the user to the default layout.
func (s *Store) DeleteResultLayout(ctx context.Context, userID string, scope ResultScope) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM result_layouts WHERE user_id=$1 AND `+scope.column()+`=$2`, userID, scope.ID)
	return err
}
