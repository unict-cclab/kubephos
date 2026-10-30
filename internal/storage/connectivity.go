package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"kubephos.dev/kubephos/internal/domain"
)

func connectivityTable(connection bool) string {
	if connection {
		return "provider_connections"
	}
	return "managed_resources"
}

// Claiming the next check also bounds monitoring across multiple workers.
func (s *Store) ClaimConnectivityCheck(ctx context.Context, connection bool, resourceID, lease string) (bool, error) {
	var claimed bool
	err := s.pool.QueryRow(ctx, `UPDATE `+connectivityTable(connection)+` SET connectivity_next_check_at=now()+interval '1 minute',connectivity_lease_id=$2 WHERE id=$1 AND connectivity_next_check_at<=now() RETURNING true`, resourceID, lease).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return claimed, err
}

// A failure is confirmed by two consecutive checks; one success recovers it.
// Monitoring never changes user modification timestamps.
func (s *Store) SaveConnectivity(ctx context.Context, connection bool, resourceID, lease string, report domain.Connectivity) error {
	if report.CheckedAt.IsZero() {
		report.CheckedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	failed := report.Status == "unreachable" || report.Status == "unavailable" || report.Status == "access-denied"
	_, err = s.pool.Exec(ctx, `UPDATE `+connectivityTable(connection)+` SET
 connectivity=CASE WHEN $4 AND connectivity_failures=0 THEN jsonb_set(jsonb_set($3::jsonb,'{status}','"checking"'),'{message}','"Connection check failed; retrying."') ELSE $3::jsonb END,
 connectivity_failures=CASE WHEN $4 THEN connectivity_failures+1 ELSE 0 END
 WHERE id=$1 AND connectivity_lease_id=$2`, resourceID, lease, raw, failed)
	return err
}
