package grafanadashboards

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGrafana records the calls a dashboard installation makes.
type fakeGrafana struct {
	mu        sync.Mutex
	calls     []string
	saved     map[string]any
	folder    bool
	stored    []string
	authFails bool
	fromFiles bool
}

func (grafana *fakeGrafana) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	grafana.mu.Lock()
	defer grafana.mu.Unlock()
	grafana.calls = append(grafana.calls, r.Method+" "+r.URL.Path)
	if user, password, ok := r.BasicAuth(); !ok || user != "admin" || password != "secret" || grafana.authFails {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/dashboards/uid/" + ExperimentUID:
		if grafana.stored == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dashboard": map[string]any{"tags": grafana.stored}, "meta": map[string]any{"provisioned": grafana.fromFiles}})
	case "POST /api/folders":
		if grafana.folder {
			w.WriteHeader(http.StatusConflict)
			return
		}
		grafana.folder = true
	case "POST /api/dashboards/db":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		grafana.saved = body
		tags := []string{}
		for _, tag := range body["dashboard"].(map[string]any)["tags"].([]any) {
			tags = append(tags, tag.(string))
		}
		grafana.stored = tags
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestEnsureExperimentInstallsOnceAndSkipsAnUpToDateDashboard(t *testing.T) {
	fake := &fakeGrafana{}
	server := httptest.NewServer(fake)
	defer server.Close()
	grafana := Grafana{BaseURL: server.URL, Username: "admin", Password: "secret"}
	if err := grafana.EnsureExperiment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.saved["folderUid"] != folderUID || fake.saved["overwrite"] != true {
		t.Fatalf("saved = %#v", fake.saved)
	}
	dashboard := fake.saved["dashboard"].(map[string]any)
	if dashboard["uid"] != ExperimentUID || dashboard["id"] != nil || !slices.Contains(fake.stored, revision(experimentDefinition)) {
		t.Fatalf("dashboard = %#v", dashboard)
	}
	installed := len(fake.calls)
	if err := grafana.EnsureExperiment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.calls[installed:]; len(got) != 1 || got[0] != "GET /api/dashboards/uid/"+ExperimentUID {
		t.Fatalf("an up-to-date dashboard was written again: %v", got)
	}
}

func TestEnsureExperimentUpdatesAnOlderRevisionInAnExistingFolder(t *testing.T) {
	fake := &fakeGrafana{folder: true, stored: []string{"kubephos", revisionTag + "000000000000"}}
	server := httptest.NewServer(fake)
	defer server.Close()
	if err := (Grafana{BaseURL: server.URL, Username: "admin", Password: "secret"}).EnsureExperiment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fake.calls, "POST /api/dashboards/db") || !slices.Contains(fake.stored, revision(experimentDefinition)) {
		t.Fatalf("older dashboard was not updated: %v %v", fake.calls, fake.stored)
	}
}

func TestEnsureExperimentKeepsADashboardProvisionedFromFiles(t *testing.T) {
	fake := &fakeGrafana{stored: []string{"kubephos"}, fromFiles: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	if err := (Grafana{BaseURL: server.URL, Username: "admin", Password: "secret"}).EnsureExperiment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("a read-only provisioned dashboard was written: %v", fake.calls)
	}
}

func TestEnsureExperimentReportsRejectedCredentials(t *testing.T) {
	server := httptest.NewServer(&fakeGrafana{authFails: true})
	defer server.Close()
	err := (Grafana{BaseURL: server.URL, Username: "admin", Password: "secret"}).EnsureExperiment(context.Background())
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("err = %v", err)
	}
}

func TestExperimentURLSelectsNamespaceAndWindow(t *testing.T) {
	from := time.UnixMilli(1_763_290_000_000)
	address, err := url.Parse(ExperimentURL("http://192.0.2.10:32000/", "run-42", from))
	if err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	if address.Path != "/d/"+ExperimentUID || query.Get("var-namespace") != "run-42" || query.Get("from") != "1763290000000" || query.Get("refresh") != "10s" {
		t.Fatalf("url = %s", address)
	}
	if fallback, _ := url.Parse(ExperimentURL("http://192.0.2.10:32000", "", time.Time{})); fallback.Query().Get("from") != "now-30m" || fallback.Query().Has("var-namespace") {
		t.Fatalf("fallback url = %s", fallback)
	}
}

func TestExperimentDefinitionIsScopedToTheExperimentNamespace(t *testing.T) {
	var dashboard struct {
		UID        string `json:"uid"`
		Templating struct {
			List []struct {
				Name string `json:"name"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(ExperimentDefinition(), &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard.UID != ExperimentUID {
		t.Fatalf("uid = %s", dashboard.UID)
	}
	queries := 0
	for _, panel := range dashboard.Panels {
		for _, target := range panel.Targets {
			queries++
			// Node utilization and network latency describe the whole cluster.
			if !strings.Contains(target.Expr, "$namespace") && !strings.HasPrefix(target.Expr, "100 * (1 - ") && !strings.Contains(target.Expr, "node_latency") {
				t.Fatalf("query is not scoped to the experiment namespace: %s", target.Expr)
			}
		}
	}
	if queries == 0 || len(dashboard.Templating.List) == 0 {
		t.Fatal("dashboard has no queries or variables")
	}
}
