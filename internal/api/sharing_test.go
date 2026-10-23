package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/storage"
)

func TestSharingHTTPReadAndWriteBoundaries(t *testing.T) {
	databaseURL := os.Getenv("KUBEPHOS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set KUBEPHOS_TEST_DATABASE_URL for isolated PostgreSQL integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := fmt.Sprintf("sharing_http_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE") }()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	if _, err = isolated.Exec(ctx, `
 CREATE TABLE users(id text PRIMARY KEY,username text NOT NULL,enabled boolean NOT NULL DEFAULT true);
 INSERT INTO users(id,username) VALUES ('alice','Alice'),('bob','Bob'),('carol','Carol');
 CREATE TABLE experiment_configurations(id text PRIMARY KEY,workspace_id text NOT NULL DEFAULT 'workspace',cluster_resource_id text DEFAULT 'cluster',owner_user_id text NOT NULL,name text NOT NULL DEFAULT 'Recipe',description text NOT NULL DEFAULT '',application_ref text NOT NULL DEFAULT '',application_digest text NOT NULL DEFAULT '',definition jsonb NOT NULL DEFAULT '{"kind":"single"}',validation jsonb NOT NULL DEFAULT '{"valid":true,"issues":[]}',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
 CREATE TABLE experiments(id text PRIMARY KEY,owner_user_id text NOT NULL,configuration_id text,updated_at timestamptz NOT NULL DEFAULT now());
 CREATE TABLE experiment_variants(id text PRIMARY KEY,experiment_id text,configuration_id text);
 CREATE TABLE experiment_trials(variant_id text,pipeline_run_id text,operation_id text);
 CREATE TABLE pipeline_runs(id text PRIMARY KEY,owner_user_id text);
 CREATE TABLE pipeline_run_stages(run_id text,operation_id text,cleanup_operation_id text);
 INSERT INTO experiment_configurations(id,owner_user_id) VALUES ('cfg','alice');
 INSERT INTO experiments(id,owner_user_id,configuration_id) VALUES ('exp','alice','cfg');
 `); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/043-resource-sharing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = isolated.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := storage.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store, registry: plugins.NewRegistry()}
	call := func(handler http.HandlerFunc, method, path, id, kind, user, role, body string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("id", id)
		r.SetPathValue("resourceKind", kind)
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: user, Role: role}))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != want {
			t.Fatalf("%s %s as %s: %d %s; want %d", method, path, user, w.Code, w.Body.String(), want)
		}
	}
	call(server.getExperimentConfiguration, "GET", "/api/v1/experiment-configurations/cfg", "cfg", "", "bob", "user", "", 404)
	call(server.resourceSharing, "PUT", "/api/v1/experiments/exp/sharing", "exp", "experiments", "alice", "user", `{"visibility":"selected","userIds":["bob"]}`, 200)
	call(server.getExperimentConfiguration, "GET", "/api/v1/experiment-configurations/cfg", "cfg", "", "bob", "user", "", 200)
	call(server.getExperimentConfiguration, "GET", "/api/v1/experiment-configurations/cfg", "cfg", "", "carol", "user", "", 404)
	call(server.resourceSharing, "PUT", "/api/v1/experiments/exp/sharing", "exp", "experiments", "bob", "user", `{"visibility":"everyone","userIds":[]}`, 404)
	call(server.deleteExperimentConfiguration, "DELETE", "/api/v1/experiment-configurations/cfg", "cfg", "", "bob", "user", "", 404)
	call(server.cancelExperiment, "POST", "/api/v1/experiments/exp/cancel", "exp", "", "bob", "user", "{}", 404)
	call(server.resourceSharing, "PUT", "/api/v1/experiments/exp/sharing", "exp", "experiments", "alice", "user", `{"visibility":"private","userIds":[]}`, 200)
	call(server.getExperimentConfiguration, "GET", "/api/v1/experiment-configurations/cfg", "cfg", "", "bob", "user", "", 404)
	call(server.getExperimentConfiguration, "GET", "/api/v1/experiment-configurations/cfg", "cfg", "", "carol", "admin", "", 200)
}
