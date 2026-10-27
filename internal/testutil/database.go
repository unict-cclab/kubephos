// Package testutil provides isolated, fully migrated databases for integration tests.
package testutil

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"kubephos.dev/kubephos/internal/id"
)

func Database(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("KUBEPHOS_TEST_DATABASE_URL")
	if uri == "" {
		t.Skip("KUBEPHOS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	schema := id.New("transfer_test")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); admin.Close() })
	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(file), "../../migrations/changes/*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if _, err = pool.Exec(ctx, `CREATE TABLE databasechangelog(id text,md5sum text,orderexecuted integer)`); err != nil {
		t.Fatal(err)
	}
	for i, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatalf("migrate %s: %v", filepath.Base(file), e)
		}
		sum := sha256.Sum256(raw)
		if _, e = pool.Exec(ctx, `INSERT INTO databasechangelog VALUES($1,$2,$3)`, filepath.Base(file), fmt.Sprintf("%x", sum), i); e != nil {
			t.Fatal(e)
		}
	}
	// pgx's quoted URL keeps the schema scoped to this fixture's connections.
	if strings.Contains(uri, "?") {
		return uri + "&search_path=" + schema
	}
	return uri + "?search_path=" + schema
}
