package storage

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type listQueryCounter struct{ queries atomic.Int64 }

func (c *listQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	return ctx
}
func (*listQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func batchedListStore(t *testing.T) (*Store, *listQueryCounter) {
	t.Helper()
	url := os.Getenv("KUBEPHOS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set KUBEPHOS_TEST_DATABASE_URL to run isolated PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{fmt.Sprintf("batch_lists_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	counter := &listQueryCounter{}
	config.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Only the tables needed by the public list methods; production data is
	// never read or modified, even when tests use an existing database.
	_, err = pool.Exec(ctx, `
CREATE TABLE users (id text PRIMARY KEY, username text NOT NULL);
CREATE TABLE pipeline_runs (
 id text PRIMARY KEY, pipeline_id text NOT NULL DEFAULT 'pipeline', workspace_id text NOT NULL DEFAULT 'workspace',
 cluster_resource_id text, owner_user_id text, isolation_mode text NOT NULL DEFAULT 'isolated', name text NOT NULL DEFAULT 'run',
 status text NOT NULL DEFAULT 'succeeded', pipeline_hash text NOT NULL DEFAULT 'hash', result_type text NOT NULL DEFAULT '',
 result_version text NOT NULL DEFAULT '', result_artifact_id text, cancel_requested boolean NOT NULL DEFAULT false,
 terminal_status text, error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 queued_at timestamptz NOT NULL DEFAULT now(), scheduled_for timestamptz, started_at timestamptz, completed_at timestamptz
);
CREATE TABLE pipeline_run_stages (
 id text PRIMARY KEY, run_id text NOT NULL, position integer NOT NULL, stage_id text NOT NULL DEFAULT 'stage',
 plugin_id text NOT NULL DEFAULT 'plugin', title text NOT NULL DEFAULT 'Stage', status text NOT NULL DEFAULT 'succeeded',
 operation_id text, spec jsonb NOT NULL DEFAULT '{}', error text NOT NULL DEFAULT '', started_at timestamptz, completed_at timestamptz,
 cleanup_operation_id text, cleanup_status text NOT NULL DEFAULT 'pending', cleanup_error text NOT NULL DEFAULT '',
 cleanup_started_at timestamptz, cleanup_completed_at timestamptz
);
CREATE TABLE experiments (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'workspace', configuration_id text, owner_user_id text NOT NULL,
 kind text NOT NULL DEFAULT 'single', name text NOT NULL DEFAULT 'experiment', description text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'succeeded', result_type text NOT NULL DEFAULT '', result_version text NOT NULL DEFAULT '',
 configuration_snapshot jsonb NOT NULL DEFAULT '{}', scheduled_for timestamptz, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE experiment_variants (
 id text PRIMARY KEY, experiment_id text NOT NULL, position integer NOT NULL, name text NOT NULL DEFAULT 'variant',
 display_alias text NOT NULL DEFAULT '', plot_color text, pipeline_id text, pipeline_hash text, configuration_id text,
 configuration jsonb NOT NULL DEFAULT '{}'
);
CREATE TABLE experiment_trials (
 id text PRIMARY KEY, variant_id text NOT NULL, position integer NOT NULL, status text NOT NULL DEFAULT 'succeeded',
 operation_id text, pipeline_run_id text, result_artifact_id text, error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
INSERT INTO users VALUES ('alice', 'Alice'), ('bob', 'Bob');
INSERT INTO pipeline_runs (id, owner_user_id, created_at, started_at)
 SELECT 'run-' || i, CASE WHEN i % 2 = 0 THEN 'alice' ELSE 'bob' END, '2026-01-01'::timestamptz + i * interval '1 hour', '2026-01-01'::timestamptz
 FROM generate_series(0, 29) AS i;
INSERT INTO pipeline_run_stages (id, run_id, position)
 SELECT 'stage-' || i || '-' || p, 'run-' || i, p FROM generate_series(0, 29) AS i CROSS JOIN generate_series(1, 2) AS p ORDER BY p DESC;
INSERT INTO experiments (id, owner_user_id, created_at)
 SELECT 'experiment-' || i, CASE WHEN i % 2 = 0 THEN 'alice' ELSE 'bob' END, '2026-01-01'::timestamptz + i * interval '1 hour'
 FROM generate_series(0, 29) AS i;
INSERT INTO experiment_variants (id, experiment_id, position)
 SELECT 'variant-' || i || '-' || p, 'experiment-' || i, p FROM generate_series(0, 29) AS i CROSS JOIN generate_series(1, 2) AS p ORDER BY p DESC;
INSERT INTO experiment_trials (id, variant_id, position, pipeline_run_id, result_artifact_id)
 SELECT 'trial-' || i || '-' || p || '-' || q, 'variant-' || i || '-' || p, q, 'run-' || i, 'artifact-' || i
 FROM generate_series(0, 29) AS i CROSS JOIN generate_series(1, 2) AS p CROSS JOIN generate_series(1, 2) AS q ORDER BY q DESC;
INSERT INTO pipeline_runs (id, owner_user_id, created_at) VALUES ('empty-run', 'alice', '2000-01-01');
INSERT INTO experiments (id, owner_user_id, created_at) VALUES ('empty-experiment', 'alice', '2000-01-01');
`)
	if err != nil {
		t.Fatal(err)
	}
	return &Store{pool: pool}, counter
}

func TestBatchedLists(t *testing.T) {
	store, counter := batchedListStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, user   string
		limit, count int
	}{
		{"all", "", 100, 31}, {"owner", "bob", 100, 15}, {"limited", "alice", 3, 3}, {"unknown owner", "nobody", 100, 0}, {"zero limit", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter.queries.Store(0)
			runs, err := store.ListPipelineRunsForUser(ctx, tc.user, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			queries := int64(2)
			if tc.count == 0 {
				queries = 1
			}
			if len(runs) != tc.count || counter.queries.Load() != queries {
				t.Fatalf("runs=%d queries=%d; want %d and %d", len(runs), counter.queries.Load(), tc.count, queries)
			}
			for _, run := range runs {
				if tc.user != "" && run.OwnerUserID != tc.user {
					t.Fatal("owner filter lost")
				}
				if run.Stages == nil {
					t.Fatal("stages must serialize as an empty array")
				}
				if run.ID == "empty-run" {
					if len(run.Stages) != 0 {
						t.Fatal("unexpected stages")
					}
					continue
				}
				if len(run.Stages) != 2 {
					t.Fatal("missing stages")
				}
				for i, stage := range run.Stages {
					if stage.RunID != run.ID || stage.Position != i+1 {
						t.Fatal("stage grouping or order lost")
					}
				}
			}
			counter.queries.Store(0)
			experiments, err := store.ListExperimentsForUser(ctx, tc.user, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			queries = 3
			if tc.count == 0 {
				queries = 1
			}
			if len(experiments) != tc.count || counter.queries.Load() != queries {
				t.Fatalf("experiments=%d queries=%d; want %d and %d", len(experiments), counter.queries.Load(), tc.count, queries)
			}
			for _, experiment := range experiments {
				if tc.user != "" && experiment.OwnerUserID != tc.user {
					t.Fatal("owner filter lost")
				}
				if experiment.Variants == nil {
					t.Fatal("variants must serialize as an empty array")
				}
				if experiment.ID == "empty-experiment" {
					if len(experiment.Variants) != 0 {
						t.Fatal("unexpected variants")
					}
					continue
				}
				if len(experiment.Variants) != 2 {
					t.Fatal("missing variants")
				}
				for i, variant := range experiment.Variants {
					if variant.ExperimentID != experiment.ID || variant.Position != i+1 || len(variant.Trials) != 2 {
						t.Fatal("variant grouping or order lost")
					}
					for j, trial := range variant.Trials {
						if trial.VariantID != variant.ID || trial.Position != j+1 || trial.StartedAt == nil {
							t.Fatal("trial grouping, order or run timestamp lost")
						}
					}
				}
			}
		})
	}
}
