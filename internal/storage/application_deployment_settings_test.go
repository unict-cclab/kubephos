package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/testutil"
)

func TestApplicationPlacementAndRetryKeepNamespaceReserved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.pool.Exec(ctx, `INSERT INTO pipelines(id,workspace_id,name,pipeline_hash,definition,resolution,validation) VALUES('base','ws_default','Base',repeat('a',64),'{}','{}','{}');
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('cluster-run','base','ws_default','Cluster','succeeded',repeat('a',64),'','');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','Cluster','kubernetes-cluster','kubernetes','test','1','{}','base','cluster-run');
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version,cluster_resource_id,application_namespace,isolation_mode) VALUES('app-run','base','ws_default','App','running',repeat('a',64),'','','cluster','app','namespaced');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('app','ws_default','App','application-deployment','kubernetes','test','1','{"clusterResourceId":"cluster","definition":{"namespace":"app"}}','base','app-run');`)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := func() time.Time {
		var value time.Time
		if err := store.pool.QueryRow(ctx, `SELECT updated_at FROM managed_resources WHERE id='app'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	desired := json.RawMessage(`{"clusterResourceId":"cluster","definition":{"namespace":"app","applicationPlacement":{"nodeSelector":{"kubephos.dev/pool":"edge"}}}}`)
	original := timestamp()
	applied := false
	err = store.UpdateApplicationDeploymentSettings(ctx, "app", "cluster", original, desired, func() error { applied = true; return nil })
	if err != nil || !applied {
		t.Fatal("blocked edit during deployment", err)
	}
	if err = store.UpdateApplicationDeploymentSettings(ctx, "app", "cluster", original, desired, func() error { t.Fatal("stale edit reached cluster"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted stale edit", err)
	}
	retry := func(expected time.Time) error {
		p := domain.Pipeline{ID: "retry-pipeline", WorkspaceID: "ws_default", Name: "Retry", Hash: strings.Repeat("a", 64), Validation: domain.ValidationReport{Valid: true}}
		p.Resolution.Stages = []domain.ResolvedPipelineStage{{PluginVersion: "0.13.0", PluginDigest: "sha256:" + strings.Repeat("b", 64)}}
		r := domain.PipelineRun{ID: "retry-run", WorkspaceID: "ws_default", PipelineID: p.ID, Name: "Retry", PipelineHash: p.Hash, ClusterResourceID: "cluster", ApplicationNamespace: "app", IsolationMode: "namespaced"}
		return store.RetryApplicationDeployment(ctx, "app", "cluster", expected, desired, p, r)
	}
	if err = retry(timestamp()); !errors.Is(err, ErrConflict) {
		t.Fatal("retried active deployment", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='failed' WHERE id='app-run'`); err != nil {
		t.Fatal(err)
	}
	beforeRetry := timestamp()
	if err = retry(beforeRetry); err != nil {
		t.Fatal("failed deployment could not retry with its own namespace", err)
	}
	var runID string
	_ = store.pool.QueryRow(ctx, `SELECT pipeline_run_id FROM managed_resources WHERE id='app'`).Scan(&runID)
	if runID != "retry-run" {
		t.Fatal(runID)
	}
	var version, digest string
	if err := store.pool.QueryRow(ctx, `SELECT plugin_version,plugin_digest FROM managed_resources WHERE id='app'`).Scan(&version, &digest); err != nil || version != "0.13.0" || digest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatal("retry retained old plugin metadata", version, digest, err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='running' WHERE id='retry-run'`); err != nil {
		t.Fatal("retry blocked by its own reservation", err)
	}
	if err = store.CheckApplicationNamespaceAvailable(ctx, "cluster", "app"); !errors.Is(err, ErrConflict) {
		t.Fatal("retry released namespace", err)
	}
	if err = retry(beforeRetry); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate retry accepted", err)
	}
}
