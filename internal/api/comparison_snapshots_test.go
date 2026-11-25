package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func loadConfiguration(rps int, extra string) string {
	return fmt.Sprintf(`{"components":[{"capability":"load.session.control","configuration":{"scenarioId":"journey","replicas":2,"steps":[{"type":"constant","rps":%d,"durationSeconds":60}]%s}},{"capability":"metrics.timeseries.collect","configuration":{"resolution":"15s"}}]}`, rps, extra)
}

func TestComparisonCompatibilityReportsLoadProfileDifferences(t *testing.T) {
	variants := []domain.ExperimentVariant{
		{Name: "Baseline", Configuration: json.RawMessage(loadConfiguration(10, ""))},
		{Name: "Baseline", Configuration: json.RawMessage(loadConfiguration(10, `,"warmupSeconds":0,"spawnRate":10`))},
	}
	report := comparisonCompatibilityReport(variants)
	if !report.Compatible || len(report.Differences) != 0 || len(report.Sources) != 2 || report.Sources[1].Label != "Baseline (2)" || report.Sources[0].Fingerprint != report.Sources[1].Fingerprint {
		t.Fatalf("defaults must compare equal: %+v", report)
	}
	variants = append(variants, domain.ExperimentVariant{Alias: "Faster", Configuration: json.RawMessage(strings.Replace(loadConfiguration(20, ""), `"resolution":"15s"`, `"resolution":"5s","sloMilliseconds":250`, 1))})
	report = comparisonCompatibilityReport(variants)
	fields := []string{}
	for _, difference := range report.Differences {
		fields = append(fields, difference.Field)
	}
	if report.Compatible || strings.Join(fields, ",") != "load.steps,metrics.resolution,metrics.sloMilliseconds" {
		t.Fatalf("differences: %v", fields)
	}
	if report.Differences[1].Values["Faster"] != "5s" || report.Differences[1].Values["Baseline"] != "15s" || report.Differences[2].Values["Baseline"] != nil {
		t.Fatalf("values: %+v", report.Differences)
	}
	duration := comparisonFingerprint(json.RawMessage(loadConfiguration(10, "")))["load.durationSeconds"]
	if duration != float64(60) {
		t.Fatalf("duration: %v", duration)
	}
	if fingerprint := comparisonFingerprint(json.RawMessage(`{}`)); fingerprint["load.steps"] != nil || len(fingerprint) != len(comparisonFingerprintFields) {
		t.Fatalf("configurations without a load profile: %v", fingerprint)
	}
}

func TestComparisonSnapshotIsReproducibleAndReportsStaleSources(t *testing.T) {
	s, pool := transferServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	artifact := func(key string) {
		t.Helper()
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','source','succeeded','{}','{}','{}',repeat('a',64))`, "op"+key)
		if _, err := s.store.CreateArtifact(ctx, domain.Artifact{ID: "data" + key, OperationID: "op" + key, Name: "data", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", StorageKey: "/results/" + key, Digest: "sha256:" + strings.Repeat("a", 64), SizeBytes: 1}); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES('alice','alice','user')`)
	for i, source := range []string{"first", "second"} {
		exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind) VALUES($1,'ws_default',$1,'succeeded','TimeSeriesDataset','v1alpha1','alice','instance')`, source)
		exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,plot_color,configuration) VALUES($1,$2,1,'Approach','Approach','#123456',$3)`, "v-"+source, source, loadConfiguration(10*(i+1), ""))
	}
	for i := 1; i <= 5; i++ {
		artifact(fmt.Sprint(i))
	}
	// The pilot run of the second experiment never enters a comparison.
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id,pilot) VALUES
 ('trial1','v-first',1,'succeeded','op1','data1',false),('trial2','v-first',2,'succeeded','op2','data2',false),
 ('pilot3','v-second',0,'succeeded','op3','data3',true),('trial4','v-second',1,'succeeded','op4','data4',false)`)
	call := func(handler http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/comparisons/"+id, strings.NewReader(body))
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: "alice", Role: "user"}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	read := func(id string, code int) comparisonView {
		t.Helper()
		w := call(s.getComparison, "GET", id, "")
		var view comparisonView
		if w.Code != code || json.Unmarshal(w.Body.Bytes(), &view) != nil {
			t.Fatalf("get: %d %s", w.Code, w.Body.String())
		}
		return view
	}
	w := call(s.saveComparison, "POST", "", `{"name":"Paper","experiments":[{"experimentId":"first","color":"#123456"},{"experimentId":"second","color":"#abcdef"}]}`)
	var saved domain.ResultComparison
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.SnapshotCapturedAt == nil {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	view := read(saved.ID, 200)
	if view.Snapshot == nil || view.Stale || len(view.Artifacts) != 3 || len(view.Result.Variants[1].Trials) != 1 {
		t.Fatalf("snapshot view: %+v", view)
	}
	if view.Compatibility.Compatible || len(view.Compatibility.Differences) != 1 || view.Compatibility.Differences[0].Field != "load.steps" {
		t.Fatalf("load profile difference not reported: %+v", view.Compatibility)
	}

	// Later re-runs and new runs do not change what the comparison shows.
	exec(`UPDATE experiment_trials SET result_artifact_id='data5',operation_id='op5' WHERE id='trial2'`)
	artifact("6")
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id) VALUES('trial6','v-second',2,'succeeded','op6','data6')`)
	view = read(saved.ID, 200)
	ids := []string{}
	for _, item := range view.Artifacts {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "data1,data2,data4" || !view.Stale || len(view.StaleReasons) != 2 {
		t.Fatalf("frozen results changed or staleness missing: %v %v", ids, view.StaleReasons)
	}

	// A deleted experiment remains in its owner's frozen comparison.
	if err := s.store.DeleteExperiment(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	view = read(saved.ID, 200)
	if len(view.Artifacts) != 3 || !strings.Contains(strings.Join(view.StaleReasons, " "), "deleted") {
		t.Fatalf("deleted source: %+v", view.StaleReasons)
	}
	if retained, err := s.store.ComparisonArtifactRetained(ctx, "alice", "data1"); err != nil || !retained {
		t.Fatalf("frozen artifact not readable by the comparison owner: %v %v", retained, err)
	}
	if retained, _ := s.store.ComparisonArtifactRetained(ctx, "bob", "data1"); retained {
		t.Fatal("frozen artifact readable by another user")
	}
	if w := call(s.refreshComparison, "POST", saved.ID, ""); w.Code != 409 {
		t.Fatalf("refresh with a deleted source: %d", w.Code)
	}

	// Refreshing re-resolves the live sources.
	other := call(s.saveComparison, "POST", "", `{"name":"Second only","series":[{"label":"Second","color":"#abcdef","runs":[{"experimentId":"second","trialId":"trial4"},{"experimentId":"second","trialId":"trial6"}]}]}`)
	var second domain.ResultComparison
	if other.Code != 201 || json.Unmarshal(other.Body.Bytes(), &second) != nil {
		t.Fatalf("second save: %d %s", other.Code, other.Body.String())
	}
	exec(`UPDATE experiment_variants SET plot_color='#000000' WHERE id='v-second'`)
	if view := read(second.ID, 200); !view.Stale {
		t.Fatal("presentation change not reported")
	}
	w = call(s.refreshComparison, "POST", second.ID, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Stale || view.Snapshot == nil {
		t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
	}

	// Deleting the comparison releases results retained only by its snapshot.
	if w := call(s.deleteComparison, "DELETE", saved.ID, ""); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	var remaining, queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE id='data1'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("released artifact kept: %d %v", remaining, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions WHERE storage_key='/kubephos/operations/op1/'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("released object not queued: %d %v", queued, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE id IN ('data4','data6')`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("live source artifacts removed: %d %v", remaining, err)
	}
}

func TestComparisonCompatibilityReportsRequestMixDifferences(t *testing.T) {
	variants := []domain.ExperimentVariant{
		{Name: "Default mix", Configuration: json.RawMessage(loadConfiguration(10, ""))},
		{Name: "Empty mix", Configuration: json.RawMessage(loadConfiguration(10, `,"taskWeights":{}`))},
	}
	if report := comparisonCompatibilityReport(variants); !report.Compatible {
		t.Fatalf("an empty mix keeps the Locust file weights: %+v", report)
	}
	variants = append(variants, domain.ExperimentVariant{Name: "Browsing", Configuration: json.RawMessage(loadConfiguration(10, `,"taskWeights":{"browse":90,"checkout":10}`))})
	report := comparisonCompatibilityReport(variants)
	if report.Compatible || len(report.Differences) != 1 || report.Differences[0].Field != "load.taskWeights" || report.Differences[0].Values["Default mix"] != nil {
		t.Fatalf("a different request mix must be reported: %+v", report.Differences)
	}
}
