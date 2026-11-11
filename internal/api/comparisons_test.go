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

func TestWholeExperimentComparisonKeepsApproachesAndAllSuccessfulExecutions(t *testing.T) {
	source := domain.Experiment{ID: "first", Name: "First experiment", Variants: []domain.ExperimentVariant{{Name: "NAS", Trials: []domain.ExperimentTrial{{ID: "one", Status: "succeeded", ResultArtifactID: "data1"}, {ID: "failed", Status: "failed"}, {ID: "two", Status: "succeeded", ResultArtifactID: "data2"}}}}}
	first := experimentComparisonSeries(source, "#123456")
	if len(first) != 1 || len(first[0].Runs) != 2 || first[0].Label != source.Name || first[0].Runs[1].TrialID != "two" {
		t.Fatalf("all successful executions not included: %#v", first)
	}
	source.ID = "second"
	source.Name = "Second experiment"
	source.Variants[0].Trials = source.Variants[0].Trials[:1]
	second := experimentComparisonSeries(source, "#abcdef")
	combined := domain.ResultComparison{Name: "Comparison", Series: append(first, second...)}
	if err := validateComparison(&combined); err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || len(second[0].Runs) != 1 || first[0].Label == second[0].Label {
		t.Fatalf("different execution counts were mixed: %#v", combined)
	}
	source.Variants = append(source.Variants, domain.ExperimentVariant{Name: "Other approach", Trials: []domain.ExperimentTrial{{ID: "three", Status: "succeeded", ResultArtifactID: "data3"}}})
	suite := experimentComparisonSeries(source, "#abcdef")
	if len(suite) != 2 || suite[0].Label == suite[1].Label {
		t.Fatalf("suite configurations must remain separate: %#v", suite)
	}
}

func TestComparisonAPISelectsExperimentsWithUnequalExecutionCounts(t *testing.T) {
	s, pool := transferServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES('alice','alice','user')`)
	for _, source := range []string{"first", "second"} {
		exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind) VALUES($1,'ws_default',$1,'succeeded','TimeSeriesDataset','v1alpha1','alice','instance')`, source)
		exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,plot_color,configuration) VALUES($1,$2,1,'Same approach','Same approach','#123456','{}')`, "v-"+source, source)
	}
	for i := 1; i <= 4; i++ {
		key := fmt.Sprint(i)
		source := "second"
		if i == 1 {
			source = "first"
		}
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','source','succeeded','{}','{}','{}',repeat('a',64))`, "op"+key)
		if _, err := s.store.CreateArtifact(ctx, domain.Artifact{ID: "data" + key, OperationID: "op" + key, Name: "data", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", StorageKey: "/results/" + key, Digest: "sha256:" + strings.Repeat("a", 64), SizeBytes: 1}); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id) VALUES($1,$2,$3,'succeeded',$4,$5)`, "trial"+key, "v-"+source, i, "op"+key, "data"+key)
	}
	r := httptest.NewRequest("POST", "/comparisons", strings.NewReader(`{"name":"Paper","experiments":[{"experimentId":"first","color":"#123456"},{"experimentId":"second","color":"#abcdef"}]}`))
	r = r.WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: "alice", Role: "user"}))
	w := httptest.NewRecorder()
	s.saveComparison(w, r)
	if w.Code != 201 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var item domain.ResultComparison
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Series) != 2 || len(item.Series[0].Runs) != 1 || len(item.Series[1].Runs) != 3 || item.Series[0].Label != "first" || item.Series[1].Color != "#abcdef" {
		t.Fatalf("incorrect experiment aggregation: %#v", item)
	}
	r = httptest.NewRequest("GET", "/comparisons/"+item.ID, nil)
	r.SetPathValue("id", item.ID)
	r = r.WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: "alice", Role: "user"}))
	w = httptest.NewRecorder()
	s.getComparison(w, r)
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var view comparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Result.Variants) != 2 || len(view.Artifacts) != 4 {
		t.Fatalf("missing aggregation sources: %#v", view)
	}

	// Candidate search uses the full visible history and returns metadata only.
	page, err := s.store.ListComparisonCandidates(ctx, "alice", "alice", "SECOND", 0)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "second" {
		t.Fatalf("candidate search: %#v %v", page, err)
	}
	page, err = s.store.ListComparisonCandidates(ctx, "unknown-user", "", "", 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("candidate visibility: %#v %v", page, err)
	}
	// An explicit label survives reloading without renaming the source.
	aliasRequest := httptest.NewRequest("POST", "/comparisons", strings.NewReader(`{"name":"Aliases","experiments":[{"experimentId":"first","color":"#123456","label":"  Baseline  "},{"experimentId":"second","color":"#abcdef"}]}`))
	aliasRequest = aliasRequest.WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: "alice", Role: "user"}))
	aliasResponse := httptest.NewRecorder()
	s.saveComparison(aliasResponse, aliasRequest)
	if aliasResponse.Code != 201 {
		t.Fatalf("alias save: %d %s", aliasResponse.Code, aliasResponse.Body.String())
	}
	var aliased domain.ResultComparison
	if err := json.Unmarshal(aliasResponse.Body.Bytes(), &aliased); err != nil {
		t.Fatal(err)
	}
	if aliased.Series[0].LabelOverride != "Baseline" || aliased.Series[0].Label != "Baseline" {
		t.Fatalf("alias missing: %#v", aliased)
	}
	aliasGet := httptest.NewRequest("GET", "/comparisons/"+aliased.ID, nil)
	aliasGet.SetPathValue("id", aliased.ID)
	aliasGet = aliasGet.WithContext(aliasRequest.Context())
	aliasResponse = httptest.NewRecorder()
	s.getComparison(aliasResponse, aliasGet)
	var aliasView comparisonView
	if aliasResponse.Code != 200 {
		t.Fatalf("alias reload: %d %s", aliasResponse.Code, aliasResponse.Body.String())
	}
	if err := json.Unmarshal(aliasResponse.Body.Bytes(), &aliasView); err != nil {
		t.Fatal(err)
	}
	if aliasView.Result.Variants[0].Alias != "Baseline" {
		t.Fatal("alias not used in results", aliasView.Result.Variants)
	}
	var sourceName string
	if err := pool.QueryRow(ctx, `SELECT name FROM experiments WHERE id='first'`).Scan(&sourceName); err != nil || sourceName != "first" {
		t.Fatal("original source renamed", sourceName, err)
	}
	exec(`UPDATE experiments SET name='Same name' WHERE id IN ('first','second')`)
	w = httptest.NewRecorder()
	s.getComparison(w, r)
	if w.Code != 200 {
		t.Fatalf("same names: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Result.Variants[0].Alias != "Same name" || view.Result.Variants[1].Alias != "Same name" {
		t.Fatal("plot labels were numbered", view.Result.Variants)
	}
	// Legacy selections may mix experiments or include only one repetition.
	exec(`UPDATE result_comparisons SET series=$2 WHERE id=$1`, item.ID, `[{"label":"Mixed","color":"#123456","runs":[{"experimentId":"first","trialId":"trial1"},{"experimentId":"second","trialId":"trial2"}]}]`)
	w = httptest.NewRecorder()
	s.getComparison(w, r)
	if w.Code != 200 {
		t.Fatalf("legacy get: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Result.Variants) != 2 || len(view.Result.Variants[1].Trials) != 3 || len(view.Artifacts) != 4 {
		t.Fatalf("legacy comparison still mixes or drops executions: %#v", view.Result)
	}

	exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,kind)
 SELECT 'historical-'||n,'ws_default','History '||n,'succeeded','TimeSeriesDataset','v1alpha1','alice','instance' FROM generate_series(1,205) n`)
	exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,plot_color,configuration)
 SELECT 'historical-v-'||n,'historical-'||n,1,'A','A','#123456','{}' FROM generate_series(1,205) n`)
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id)
 SELECT 'historical-t-'||n,'historical-v-'||n,1,'succeeded','op1','data1' FROM generate_series(1,205) n`)
	page, err = s.store.ListComparisonCandidates(ctx, "alice", "", "History 205", 0)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "historical-205" {
		t.Fatalf("historical search: %#v %v", page, err)
	}
	page, err = s.store.ListComparisonCandidates(ctx, "alice", "", "", 200)
	if err != nil || page.Total != 207 || len(page.Items) != 7 {
		t.Fatalf("pagination: %#v %v", page, err)
	}
	page, err = s.store.ListComparisonCandidates(ctx, "alice", "another-owner", "", 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("owner filter: %#v %v", page, err)
	}
}

func TestWholeExperimentsKeepOriginalNamesWhenTheyMatch(t *testing.T) {
	first := domain.Experiment{ID: "first", Name: "Same name", Variants: []domain.ExperimentVariant{{Name: "NAS", Trials: []domain.ExperimentTrial{{ID: "one", Status: "succeeded", ResultArtifactID: "data1"}}}}}
	second := first
	second.ID = "second"
	groups := append(experimentComparisonSeries(first, "#123456"), experimentComparisonSeries(second, "#abcdef")...)
	comparison := domain.ResultComparison{Name: "Paper", Series: groups}
	if err := validateComparisonSelection(&comparison, true); err != nil {
		t.Fatal(err)
	}
	if groups[0].Label != "Same name" || groups[1].Label != "Same name" {
		t.Fatal("experiment names changed", groups)
	}
	if err := validateComparison(&comparison); err == nil {
		t.Fatal("legacy duplicate label validation was relaxed")
	}
}
