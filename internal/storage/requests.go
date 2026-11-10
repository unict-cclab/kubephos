package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

const adminRequestQuery = `SELECT r.id,r.requester_user_id,u.username,r.message,r.status,COALESCE(r.reply,''),COALESCE(a.username,''),r.resolved_at,r.created_at,r.updated_at
FROM admin_requests r JOIN users u ON u.id=r.requester_user_id LEFT JOIN users a ON a.id=r.resolved_by_user_id`

func scanAdminRequest(row pgx.Row) (domain.AdminRequest, error) {
	var item domain.AdminRequest
	err := row.Scan(&item.ID, &item.RequesterUserID, &item.Requester, &item.Message, &item.Status, &item.Reply, &item.ResolvedBy, &item.ResolvedAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

// ListAdminRequests returns every request when requester is empty, otherwise only
// that member's. Open requests come first, newest first within each status.
func (s *Store) ListAdminRequests(ctx context.Context, requester string) ([]domain.AdminRequest, error) {
	rows, err := s.pool.Query(ctx, adminRequestQuery+` WHERE ($1='' OR r.requester_user_id=$1) ORDER BY r.status='open' DESC, r.created_at DESC, r.id LIMIT 500`, requester)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.AdminRequest{}
	for rows.Next() {
		item, err := scanAdminRequest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetAdminRequest(ctx context.Context, id string) (domain.AdminRequest, error) {
	return scanAdminRequest(s.pool.QueryRow(ctx, adminRequestQuery+` WHERE r.id=$1`, id))
}

// CreateAdminRequest stores a request unless the member already has maxOpen open
// ones, in which case it returns ErrConflict. The requester row is locked so
// concurrent submissions cannot exceed the limit.
func (s *Store) CreateAdminRequest(ctx context.Context, item domain.AdminRequest, maxOpen int) (domain.AdminRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return item, err
	}
	defer tx.Rollback(ctx)
	var open int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM admin_requests WHERE requester_user_id=u.id AND status='open') FROM users u WHERE u.id=$1 FOR UPDATE`, item.RequesterUserID).Scan(&open); err != nil {
		return item, err
	}
	if open >= maxOpen {
		return item, ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_requests(id,requester_user_id,message) VALUES($1,$2,$3)`, item.ID, item.RequesterUserID, item.Message); err != nil {
		return item, err
	}
	if err = tx.Commit(ctx); err != nil {
		return item, err
	}
	return s.GetAdminRequest(ctx, item.ID)
}

// SetAdminRequestStatus resolves a request on behalf of an administrator, with an
// optional reply, or reopens it. Reopening clears the resolution and the reply.
func (s *Store) SetAdminRequestStatus(ctx context.Context, id, status, reply, administratorID string) (domain.AdminRequest, error) {
	result, err := s.pool.Exec(ctx, `UPDATE admin_requests SET status=$2,
		reply=CASE WHEN $2='resolved' THEN NULLIF($3,'') END,
		resolved_by_user_id=CASE WHEN $2='resolved' THEN $4 END,
		resolved_at=CASE WHEN $2='resolved' THEN now() END,
		updated_at=now()
		WHERE id=$1`, id, status, reply, administratorID)
	if err != nil {
		return domain.AdminRequest{}, err
	}
	if result.RowsAffected() == 0 {
		return domain.AdminRequest{}, ErrNotFound
	}
	return s.GetAdminRequest(ctx, id)
}

// UpdateOwnAdminRequest lets a member edit the text of their own open request,
// resolve it themselves, or both. An empty message keeps the current text.
// Another member's request reads as missing; a resolved one is a conflict.
func (s *Store) UpdateOwnAdminRequest(ctx context.Context, id, requesterID, message string, resolve bool) (domain.AdminRequest, error) {
	result, err := s.pool.Exec(ctx, `UPDATE admin_requests SET
		message=COALESCE(NULLIF($3,''),message),
		status=CASE WHEN $4 THEN 'resolved' ELSE status END,
		resolved_by_user_id=CASE WHEN $4 THEN requester_user_id ELSE resolved_by_user_id END,
		resolved_at=CASE WHEN $4 THEN now() ELSE resolved_at END,
		updated_at=now()
		WHERE id=$1 AND requester_user_id=$2 AND status='open'`, id, requesterID, message, resolve)
	if err != nil {
		return domain.AdminRequest{}, err
	}
	if result.RowsAffected() == 0 {
		item, err := s.GetAdminRequest(ctx, id)
		if err != nil || item.RequesterUserID != requesterID {
			return domain.AdminRequest{}, ErrNotFound
		}
		return domain.AdminRequest{}, ErrConflict
	}
	return s.GetAdminRequest(ctx, id)
}

// DeleteAdminRequest removes a request on behalf of an administrator.
func (s *Store) DeleteAdminRequest(ctx context.Context, id string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM admin_requests WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
