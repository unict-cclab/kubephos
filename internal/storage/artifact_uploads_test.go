package storage

import (
	"context"
	"errors"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/testutil"
	"testing"
)

func TestArtifactUploadCleanupSurvivesFailureAndPreservesRegisteredObjects(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err = s.pool.Exec(ctx, `INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash)
 VALUES('op_upload','ws_default','test','upload','succeeded','{}','{}','{}',repeat('a',64))`); err != nil {
		t.Fatal(err)
	}
	key := "/kubephos/operations/op_upload/result.json"
	if err = s.BeginArtifactUpload(ctx, key); err != nil {
		t.Fatal(err)
	}
	value := domain.Artifact{ID: "registered", OperationID: "op_upload", Name: "result", Type: "Result", Version: "v1", MediaType: "application/json", StorageKey: key, Digest: "sha256:test"}
	if _, err = s.CreateArtifact(ctx, value); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("registered object still queued", pending, err)
	}
	if err = s.BeginArtifactUpload(ctx, key); !errors.Is(err, ErrConflict) {
		t.Fatal("allowed overwrite of a registered object", err)
	}
	orphan := "/kubephos/operations/op_upload/orphan.json"
	if err = s.BeginArtifactUpload(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE artifact_deletions SET retry_after=now()`); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessArtifactDeletions(ctx, func(context.Context, string) error { return errors.New("storage offline") }); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("lost failed cleanup", pending, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE artifact_deletions SET retry_after=now()`); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessArtifactDeletions(ctx, func(_ context.Context, key string) error {
		if key != orphan {
			t.Fatal("deleted registered data", key)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
