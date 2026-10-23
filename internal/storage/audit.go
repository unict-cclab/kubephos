package storage

import (
	"context"
	"encoding/json"

	"kubephos.dev/kubephos/internal/domain"
)

func (s *Store) AppendAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	details := event.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor, action, target_type, target_id, outcome, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.Actor, event.Action, event.TargetType, event.TargetID, event.Outcome, details)
	return err
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, actor, action, target_type, target_id, outcome, details, created_at
		FROM audit_events
		ORDER BY sequence DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AuditEvent{}
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.Sequence, &event.Actor, &event.Action, &event.TargetType, &event.TargetID, &event.Outcome, &event.Details, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
