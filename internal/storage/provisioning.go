package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// LockProvisioning serializes allocation checks through persistence, across app instances.
// A dedicated connection avoids holding a pooled connection while validation uses the pool.
func (s *Store) LockProvisioning(ctx context.Context, connectionID string) (func(), error) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, err
	}
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "provisioning:"+connectionID).Scan(&locked)
	if err != nil || !locked {
		conn.Close(ctx)
		if err != nil {
			return nil, err
		}
		return nil, ErrConflict
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Close(cleanup)
	}, nil
}

type ProvisioningClaim struct {
	Kind string
	Spec json.RawMessage
}

func (s *Store) ProvisioningClaims(ctx context.Context, connectionID string) ([]ProvisioningClaim, error) {
	rows, err := s.pool.Query(ctx, `SELECT kind,spec FROM managed_resources WHERE connection_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC`, connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ProvisioningClaim{}
	for rows.Next() {
		var claim ProvisioningClaim
		if err := rows.Scan(&claim.Kind, &claim.Spec); err != nil {
			return nil, err
		}
		result = append(result, claim)
	}
	return result, rows.Err()
}
