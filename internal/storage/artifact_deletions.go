package storage

import "context"

// ProcessArtifactDeletions keeps only pending storage keys, never deleted
// experiment records. Jobs survive an unavailable filer or a worker restart.
func (s *Store) ProcessArtifactDeletions(ctx context.Context, remove func(context.Context, string) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT storage_key FROM artifact_deletions WHERE retry_after <= now() ORDER BY queued_at LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := remove(ctx, key); err != nil {
			if _, err := tx.Exec(ctx, `UPDATE artifact_deletions SET retry_after=now()+interval '30 seconds' WHERE storage_key=$1`, key); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM artifact_deletions WHERE storage_key=$1`, key); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
