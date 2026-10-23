package storage

import (
	"context"

	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) IdentityUserCount(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE external_id IS NOT NULL`).Scan(&count)
	return count, err
}

func (s *Store) CreateInitialIdentityUser(ctx context.Context, user domain.User) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(457213684)`); err != nil {
		return domain.User{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE external_id IS NOT NULL)`).Scan(&exists); err != nil {
		return domain.User{}, err
	}
	if exists {
		return domain.User{}, fmt.Errorf("%w: initial administrator already exists", ErrConflict)
	}
	var legacyAdminID string
	legacyErr := tx.QueryRow(ctx, `
		SELECT id FROM users
		WHERE role = 'admin' AND external_id IS NULL
		ORDER BY created_at
		LIMIT 1
		FOR UPDATE
	`).Scan(&legacyAdminID)
	if legacyErr == nil {
		err = tx.QueryRow(ctx, `
			UPDATE users
			SET username = $2, password_hash = '', external_id = $3, email = $4, role = 'admin', enabled = true, updated_at = now()
			WHERE id = $1
			RETURNING id, username, external_id, email, role, enabled, created_at, updated_at
		`, legacyAdminID, user.Username, user.ExternalID, user.Email).Scan(&user.ID, &user.Username, &user.ExternalID, &user.Email, &user.Role, &user.Enabled, &user.CreatedAt, &user.UpdatedAt)
	} else if errors.Is(legacyErr, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO users (id, username, password_hash, external_id, email, role, enabled)
			VALUES ($1, $2, '', $3, $4, 'admin', true)
			RETURNING id, username, external_id, email, role, enabled, created_at, updated_at
		`, user.ID, user.Username, user.ExternalID, user.Email).Scan(&user.ID, &user.Username, &user.ExternalID, &user.Email, &user.Role, &user.Enabled, &user.CreatedAt, &user.UpdatedAt)
	} else {
		return domain.User{}, legacyErr
	}
	if duplicate(err) {
		return domain.User{}, ErrConflict
	}
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) CreateIdentityUser(ctx context.Context, user domain.User) (domain.User, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (id, username, password_hash, external_id, email, role, enabled)
		VALUES ($1, $2, '', $3, $4, $5, true)
		RETURNING created_at, updated_at
	`, user.ID, user.Username, user.ExternalID, user.Email, user.Role).Scan(&user.CreatedAt, &user.UpdatedAt)
	if duplicate(err) {
		return domain.User{}, ErrConflict
	}
	user.Enabled = err == nil
	return user, err
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, username, COALESCE(external_id, ''), email, role, enabled, created_at, updated_at
		FROM users
		WHERE external_id IS NOT NULL
		ORDER BY username
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.User{}
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Username, &user.ExternalID, &user.Email, &user.Role, &user.Enabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, user)
	}
	return result, rows.Err()
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (domain.User, error) {
	return scanIdentityUser(s.pool.QueryRow(ctx, `
		SELECT id, username, COALESCE(external_id, ''), email, role, enabled, created_at, updated_at
		FROM users WHERE id = $1
	`, userID))
}

func (s *Store) GetUserByExternalID(ctx context.Context, externalID string) (domain.User, error) {
	return scanIdentityUser(s.pool.QueryRow(ctx, `
		SELECT id, username, COALESCE(external_id, ''), email, role, enabled, created_at, updated_at
		FROM users WHERE external_id = $1 AND enabled = true
	`, externalID))
}

func scanIdentityUser(row scanner) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Username, &user.ExternalID, &user.Email, &user.Role, &user.Enabled, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) DeleteIdentityUser(ctx context.Context, userID string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1 AND role <> 'admin'`, userID)
	if err != nil {
		if foreignKeyViolation(err) {
			return ErrConflict
		}
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ReplaceClusterAssignments(ctx context.Context, userID, assignedBy string, clusterIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM cluster_user_assignments WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, clusterID := range clusterIDs {
		command, err := tx.Exec(ctx, `
			INSERT INTO cluster_user_assignments (user_id, cluster_resource_id, assigned_by)
			SELECT $1, id, $3 FROM managed_resources WHERE id = $2 AND kind = 'kubernetes-cluster'
		`, userID, clusterID, assignedBy)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrNotFound
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListClusterAssignments(ctx context.Context, userID string) ([]domain.ClusterUserAssignment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, cluster_resource_id, assigned_by, created_at
		FROM cluster_user_assignments
		WHERE $1 = '' OR user_id = $1
		ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ClusterUserAssignment{}
	for rows.Next() {
		var assignment domain.ClusterUserAssignment
		if err := rows.Scan(&assignment.UserID, &assignment.ClusterResourceID, &assignment.AssignedBy, &assignment.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, assignment)
	}
	return result, rows.Err()
}

func (s *Store) UserCanUseCluster(ctx context.Context, userID, clusterID string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cluster_user_assignments WHERE user_id = $1 AND cluster_resource_id = $2)`, userID, clusterID).Scan(&allowed)
	return allowed, err
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_sessions (token_hash, user_id, csrf_token, expires_at)
		VALUES ($1, $2, $3, $4)
	`, session.TokenHash, session.User.ID, session.CSRFToken, session.ExpiresAt)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (domain.Session, error) {
	var session domain.Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.token_hash, s.csrf_token, s.expires_at,
		       u.id, u.username, COALESCE(u.external_id, ''), u.email, u.role, u.enabled, u.created_at, u.updated_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.enabled = true
	`, tokenHash).Scan(&session.TokenHash, &session.CSRFToken, &session.ExpiresAt, &session.User.ID, &session.User.Username, &session.User.ExternalID, &session.User.Email, &session.User.Role, &session.User.Enabled, &session.User.CreatedAt, &session.User.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err == nil {
		_, _ = s.pool.Exec(ctx, `UPDATE auth_sessions SET last_seen_at = now() WHERE token_hash = $1 AND last_seen_at < now() - interval '5 minutes'`, tokenHash)
	}
	return session, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at <= now()`)
	return err
}
