package storage

import (
	"context"
	"strings"
)

// Register cleanup before uploading so failures and worker crashes cannot leave
// untracked objects. Saving the metadata cancels this intent in the same commit.
func (s *Store) BeginArtifactUpload(ctx context.Context, key string) error {
	if !strings.HasPrefix(key, "/kubephos/operations/op_") && !strings.HasPrefix(key, "/experiments/exp_") {
		return ErrConflict
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, "?%#") || strings.HasSuffix(key, "/") {
		return ErrConflict
	}
	result, err := s.pool.Exec(ctx, `INSERT INTO artifact_deletions(storage_key,retry_after)
 SELECT $1,now()+interval '1 hour'
 WHERE NOT EXISTS(SELECT 1 FROM artifacts WHERE storage_key=$1)
 AND NOT EXISTS(SELECT 1 FROM experiment_figures WHERE storage_key=$1)
 ON CONFLICT(storage_key) DO UPDATE SET retry_after=excluded.retry_after`, key)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
