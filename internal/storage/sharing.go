package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrInvalidSharing = errors.New("invalid sharing")

type Sharing struct {
	Visibility string   `json:"visibility"`
	UserIDs    []string `json:"userIds"`
}

type SharingUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func sharingTables(kind string) (string, string, string, error) {
	switch kind {
	case "experiments":
		return "experiments", "experiment_shares", "experiment_id", nil
	case "experiment-configurations":
		return "experiment_configurations", "configuration_shares", "configuration_id", nil
	}
	return "", "", "", errors.New("invalid sharing resource")
}

func (s *Store) CanViewResource(ctx context.Context, kind, id, userID string) (bool, error) {
	function := map[string]string{"experiments": "can_view_experiment", "experiment-configurations": "can_view_configuration", "pipeline-runs": "can_view_run", "operations": "can_view_operation"}[kind]
	if function == "" {
		return false, errors.New("invalid resource")
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, "SELECT "+function+"($1,$2)", userID, id).Scan(&allowed)
	return allowed, err
}

func (s *Store) SharingUsers(ctx context.Context) ([]SharingUser, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,username FROM users WHERE enabled ORDER BY lower(username),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SharingUser{}
	for rows.Next() {
		var user SharingUser
		if err := rows.Scan(&user.ID, &user.Username); err != nil {
			return nil, err
		}
		result = append(result, user)
	}
	return result, rows.Err()
}

func (s *Store) GetSharing(ctx context.Context, kind, id string) (Sharing, string, error) {
	table, grants, key, err := sharingTables(kind)
	if err != nil {
		return Sharing{}, "", err
	}
	var public bool
	var owner string
	if err := s.pool.QueryRow(ctx, "SELECT shared_with_everyone,owner_user_id FROM "+table+" WHERE id=$1", id).Scan(&public, &owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return Sharing{}, "", err
	}
	result := Sharing{Visibility: "private", UserIDs: []string{}}
	rows, err := s.pool.Query(ctx, "SELECT user_id FROM "+grants+" WHERE "+key+"=$1 ORDER BY user_id", id)
	if err != nil {
		return result, owner, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return result, owner, err
		}
		result.UserIDs = append(result.UserIDs, userID)
	}
	if public {
		result.Visibility = "everyone"
	} else if len(result.UserIDs) > 0 {
		result.Visibility = "selected"
	}
	return result, owner, rows.Err()
}

func (s *Store) SetSharing(ctx context.Context, kind, id, actorID string, admin bool, value Sharing) error {
	table, grants, key, err := sharingTables(kind)
	if err != nil {
		return err
	}
	if value.Visibility != "private" && value.Visibility != "selected" && value.Visibility != "everyone" {
		return fmt.Errorf("%w: invalid sharing visibility", ErrInvalidSharing)
	}
	if value.Visibility == "selected" && len(value.UserIDs) == 0 {
		return fmt.Errorf("%w: select at least one user", ErrInvalidSharing)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	if err := tx.QueryRow(ctx, "SELECT owner_user_id FROM "+table+" WHERE id=$1 FOR UPDATE", id).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !admin && owner != actorID {
		return ErrNotFound
	}
	if value.Visibility == "selected" {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=ANY($1) AND enabled AND id<>$2`, value.UserIDs, owner).Scan(&count); err != nil {
			return err
		}
		if count != len(value.UserIDs) {
			return fmt.Errorf("%w: select distinct enabled users other than the resource owner", ErrInvalidSharing)
		}
	}
	if _, err = tx.Exec(ctx, "DELETE FROM "+grants+" WHERE "+key+"=$1", id); err != nil {
		return err
	}
	if value.Visibility == "selected" {
		if _, err = tx.Exec(ctx, "INSERT INTO "+grants+"("+key+",user_id) SELECT $1,unnest($2::text[])", id, value.UserIDs); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE "+table+" SET shared_with_everyone=$2,updated_at=now() WHERE id=$1", id, value.Visibility == "everyone"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
