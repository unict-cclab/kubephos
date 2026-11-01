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

func TestComparisonValidation(t *testing.T) {
	base := func() domain.ResultComparison {
		return domain.ResultComparison{Name: "Paper", Series: []domain.ComparisonSeries{{Label: "NAS", Color: "#1f77b4", Runs: []domain.ComparisonRun{{ExperimentID: "source", TrialID: "one"}, {ExperimentID: "source", TrialID: "two"}}}}}
	}
	valid := base()
	if err := validateComparison(&valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*domain.ResultComparison)
	}{
		{"one run", func(c *domain.ResultComparison) { c.Series[0].Runs = c.Series[0].Runs[:1] }},
		{"duplicate run", func(c *domain.ResultComparison) { c.Series[0].Runs[1] = c.Series[0].Runs[0] }},
		{"unsafe color", func(c *domain.ResultComparison) { c.Series[0].Color = "url(example)" }},
		{"duplicate label", func(c *domain.ResultComparison) {
			c.Series = append(c.Series, domain.ComparisonSeries{Label: " nas ", Color: "#ffffff", Runs: []domain.ComparisonRun{{ExperimentID: "source", TrialID: "three"}}})
		}},
		{"empty series", func(c *domain.ResultComparison) { c.Series[0].Runs = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := base()
			test.change(&c)
			if validateComparison(&c) == nil {
				t.Fatal("invalid comparison accepted")
			}
		})
	}
}

func TestComparisonIsolationAndSourcePermissions(t *testing.T) {
	s, pool := transferServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES('alice','alice','user'),('bob','bob','user')`)
	for i := 1; i <= 2; i++ {
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','source','succeeded','{}','{}','{}',repeat('a',64))`, fmt.Sprint("op", i))
		_, err := s.store.CreateArtifact(ctx, domain.Artifact{ID: fmt.Sprint("data", i), OperationID: fmt.Sprint("op", i), Name: "data", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", StorageKey: fmt.Sprint("/results/", i), Digest: "sha256:" + strings.Repeat("a", 64), SizeBytes: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind) VALUES('source','ws_default','Suite','succeeded','TimeSeriesDataset','v1alpha1','alice','suite')`)
	exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,plot_color,configuration) VALUES('variant','source',1,'NAS','Original','#123456','{"components":[{"capability":"metrics.timeseries.collect","configuration":{"sloMilliseconds":250}}]}')`)
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id,completed_at) VALUES('one','variant',1,'succeeded','op1','data1',now()),('two','variant',2,'succeeded','op2','data2',now())`)
	call := func(handler http.HandlerFunc, method, path, id, user, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: user, Role: "user"}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	beforeSource, err := s.store.GetComparisonSource(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(beforeSource)
	body := `{"name":"Paper","series":[{"label":"Paper label","color":"#abcdef","runs":[{"experimentId":"source","trialId":"one"},{"experimentId":"source","trialId":"two"}]}]}`
	// A non-owner cannot turn a private source into their own saved comparison.
	if w := call(s.saveComparison, "POST", "/comparisons", "", "bob", body); w.Code != 422 {
		t.Fatalf("private source: %d %s", w.Code, w.Body.String())
	}
	w := call(s.saveComparison, "POST", "/comparisons", "", "alice", body)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var saved domain.ResultComparison
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	for _, h := range []struct {
		handler http.HandlerFunc
		method  string
	}{{s.getComparison, "GET"}, {s.saveComparison, "PUT"}, {s.deleteComparison, "DELETE"}} {
		if w := call(h.handler, h.method, "/comparisons/"+saved.ID, saved.ID, "bob", body); w.Code != 404 {
			t.Fatalf("owner isolation %s: %d", h.method, w.Code)
		}
	}
	w = call(s.getComparison, "GET", "/comparisons/"+saved.ID, saved.ID, "alice", "")
	if w.Code != 200 {
		t.Fatalf("read: %s", w.Body.String())
	}
	var view comparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Result.Variants) != 1 || len(view.Result.Variants[0].Trials) != 2 || view.Result.Variants[0].Color != "#abcdef" || len(view.SourceConfigurations) != 2 {
		t.Fatalf("invalid view: %+v", view)
	}
	// Presentational edits and deleting a comparison cannot change a suite.
	edited := strings.Replace(body, "Paper label", "Changed label", 1)
	if w := call(s.saveComparison, "PUT", "/comparisons/"+saved.ID, saved.ID, "alice", edited); w.Code != 200 {
		t.Fatalf("edit: %s", w.Body.String())
	}
	if w := call(s.deleteComparison, "DELETE", "/comparisons/"+saved.ID, saved.ID, "alice", ""); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	var alias, color string
	if err := pool.QueryRow(ctx, `SELECT display_alias,plot_color FROM experiment_variants WHERE id='variant'`).Scan(&alias, &color); err != nil || alias != "Original" || color != "#123456" {
		t.Fatalf("suite mutated: %s %s %v", alias, color, err)
	}
	for table, expected := range map[string]int{"experiments": 1, "experiment_trials": 2, "operations": 2, "artifacts": 2} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != expected {
			t.Fatalf("source data removed: %s %d %v", table, n, err)
		}
	}
	afterSource, err := s.store.GetComparisonSource(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(afterSource)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("source suite changed after comparison CRUD")
	}
	// Sharing is checked again on every opening, even after a comparison was saved.
	exec(`INSERT INTO experiment_shares(experiment_id,user_id) VALUES('source','bob')`)
	w = call(s.saveComparison, "POST", "/comparisons", "", "bob", body)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM experiment_shares WHERE user_id='bob'`)
	if w := call(s.getComparison, "GET", "/comparisons/"+saved.ID, saved.ID, "bob", ""); w.Code != 409 || strings.Contains(w.Body.String(), "Original") {
		t.Fatalf("revoked source exposed: %d %s", w.Code, w.Body.String())
	}
	exec(`DELETE FROM experiments WHERE id='source'`)
	if w := call(s.getComparison, "GET", "/comparisons/"+saved.ID, saved.ID, "bob", ""); w.Code != 409 {
		t.Fatalf("missing source: %d", w.Code)
	}
}
