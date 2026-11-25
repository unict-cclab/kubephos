package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestResultLayoutsArePerUserAndFollowResultAccess(t *testing.T) {
	s, pool := transferServer(t, "")
	ctx := context.Background()
	exec := func(query string) {
		t.Helper()
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES('alice','alice','user'),('bob','bob','user'),('carol','carol','user')`)
	exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id) VALUES('exp_a','ws_default','A','succeeded','TimeSeriesDataset','v1alpha1','alice')`)
	exec(`INSERT INTO experiment_shares(experiment_id,user_id) VALUES('exp_a','bob')`)
	exec(`INSERT INTO result_comparisons(id,owner_user_id,name,series) VALUES('cmp_a','alice','Comparison','[]')`)
	call := func(handler http.HandlerFunc, user, kind, id, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		r.SetPathValue("kind", kind)
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: user, Role: "user"}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	// Bodies are compared as JSON values: jsonb does not keep the key order.
	expect := func(w *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		var got, want any
		if body != "" && (json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(body), &want) != nil) {
			t.Fatalf("invalid JSON: %s", w.Body.String())
		}
		if w.Code != code || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), code, body)
		}
	}
	// No layout yet: the frontend shows the default charts.
	expect(call(s.getResultLayout, "alice", "experiments", "exp_a", ""), 200, `{"layout":null}`)
	expect(call(s.saveResultLayout, "alice", "experiments", "exp_a", `{"layout":{"version":1,"items":[]}}`), 200, "")
	expect(call(s.getResultLayout, "alice", "experiments", "exp_a", ""), 200, `{"layout":{"version":1,"items":[]}}`)
	// A user with access to the same experiment keeps a layout of their own.
	expect(call(s.getResultLayout, "bob", "experiments", "exp_a", ""), 200, `{"layout":null}`)
	expect(call(s.saveResultLayout, "bob", "experiments", "exp_a", `{"layout":{"version":1,"items":[{"id":"x"}]}}`), 200, "")
	expect(call(s.getResultLayout, "alice", "experiments", "exp_a", ""), 200, `{"layout":{"version":1,"items":[]}}`)
	// Without access to the results nothing is read or stored.
	expect(call(s.getResultLayout, "carol", "experiments", "exp_a", ""), 404, "")
	expect(call(s.saveResultLayout, "carol", "comparisons", "cmp_a", `{"layout":{}}`), 404, "")
	expect(call(s.saveResultLayout, "alice", "pipelines", "exp_a", `{"layout":{}}`), 404, "")
	// Comparisons have their own layouts; only JSON objects are accepted.
	expect(call(s.saveResultLayout, "alice", "comparisons", "cmp_a", `{"layout":[1]}`), 400, "")
	expect(call(s.saveResultLayout, "alice", "comparisons", "cmp_a", `{"layout":{"version":1,"items":[{"id":"c"}]}}`), 200, "")
	expect(call(s.getResultLayout, "alice", "comparisons", "cmp_a", ""), 200, `{"layout":{"version":1,"items":[{"id":"c"}]}}`)
	// Reset returns to the default charts.
	expect(call(s.deleteResultLayout, "alice", "comparisons", "cmp_a", ""), 204, "")
	expect(call(s.getResultLayout, "alice", "comparisons", "cmp_a", ""), 200, `{"layout":null}`)
	// Layouts go with the results they arrange.
	exec(`DELETE FROM experiments WHERE id='exp_a'`)
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM result_layouts`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("layouts left after deleting the experiment: %d %v", left, err)
	}
}
