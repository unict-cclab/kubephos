package metricscollector

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

// counterSeries is one canned counter group: value(t) = base + perSecond * (t - start).
type counterSeries struct {
	labels    map[string]string
	base      float64
	perSecond float64
	// bornAfter drops the points before start + bornAfter (series born mid-window).
	bornAfter time.Duration
}

func counterMatrix(command string, series ...counterSeries) string {
	parsed, _ := url.Parse(strings.TrimPrefix(strings.TrimPrefix(command, "get "), "--raw="))
	start, _ := strconv.ParseInt(parsed.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(parsed.Query().Get("end"), 10, 64)
	step, _ := strconv.ParseInt(parsed.Query().Get("step"), 10, 64)
	result := []map[string]any{}
	for _, item := range series {
		values := [][]any{}
		for at := start; at <= end; at += step {
			if time.Duration(at-start)*time.Second < item.bornAfter {
				continue
			}
			values = append(values, []any{at, strconv.FormatFloat(item.base+item.perSecond*float64(at-start), 'f', -1, 64)})
		}
		result = append(result, map[string]any{"metric": item.labels, "values": values})
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": result}})
	return string(raw)
}

var (
	browseFrom   = testNow.Add(-10 * time.Minute)
	checkoutFrom = testNow.Add(-8 * time.Minute)
)

func profileStep(t *testing.T) domain.PlanStep {
	t.Helper()
	step := testStep(t)
	editArtifact(t, step, "load-session", func(value map[string]any) {
		spec := value["spec"].(map[string]any)
		spec["taskMix"] = []any{map[string]any{"task": "browseProduct", "weight": 3.0, "share": 0.75}, map[string]any{"task": "checkout", "weight": 1.0, "share": 0.25}}
		spec["callChainProfile"] = map[string]any{"rps": 10.0, "secondsPerTask": 120.0, "windows": []any{
			map[string]any{"task": "browseProduct", "from": browseFrom, "to": browseFrom.Add(2 * time.Minute)},
			map[string]any{"task": "checkout", "from": checkoutFrom, "to": checkoutFrom.Add(2 * time.Minute)},
		}}
	})
	return step
}

type callModelFake struct {
	mu          sync.Mutex
	queries     []string
	executions  bool
	failIstio   bool
	scrapeEvery string
}

func (fake *callModelFake) runner() *fakeRunner {
	return &fakeRunner{now: testNow, override: func(command, query string) (string, bool, error) {
		if fake.scrapeEvery != "" && strings.Contains(command, "/api/v1/targets") {
			return `{"status":"success","data":{"activeTargets":[{"scrapePool":"istio-mesh","scrapeInterval":"` + fake.scrapeEvery + `","labels":{"job":"istio-mesh","namespace":"kubephos-app-one","pod":"kubephos-load-journey-1"}}]}}`, true, nil
		}
		if !strings.Contains(query, "sum by (task, pod)") && !(strings.Contains(query, "sum by (pod)") && strings.Contains(query, "kubephos_load_requests_total")) && !strings.Contains(query, "sum by (source_workload, destination_workload, pod)") {
			return "", false, nil
		}
		fake.mu.Lock()
		fake.queries = append(fake.queries, query)
		fake.mu.Unlock()
		browse := strings.Contains(command, "start="+strconv.FormatInt(browseFrom.Add(15*time.Second).Unix(), 10)) || strings.Contains(command, "start="+strconv.FormatInt(browseFrom.Add(30*time.Second).Unix(), 10))
		switch {
		case strings.Contains(query, "kubephos_load_task_executions_total"):
			if !fake.executions {
				return counterMatrix(command), true, nil
			}
			// Two Locust workers; the other task's counter stays flat.
			return counterMatrix(command,
				counterSeries{labels: map[string]string{"task": "browseProduct", "pod": "w1"}, base: 100, perSecond: map[bool]float64{true: 2, false: 0}[browse]},
				counterSeries{labels: map[string]string{"task": "browseProduct", "pod": "w2"}, base: 100, perSecond: map[bool]float64{true: 3, false: 0}[browse]},
				counterSeries{labels: map[string]string{"task": "checkout", "pod": "w1"}, base: 10, perSecond: map[bool]float64{true: 0, false: 5}[browse]},
			), true, nil
		case strings.Contains(query, "kubephos_load_requests_total"):
			// browseProduct sends 2 HTTP requests per execution, checkout 1.
			return counterMatrix(command, counterSeries{labels: map[string]string{"pod": "w1"}, base: 1000, perSecond: map[bool]float64{true: 10, false: 5}[browse]}), true, nil
		default:
			if fake.failIstio {
				return "", true, errors.New("prometheus unavailable")
			}
			if browse {
				return counterMatrix(command,
					counterSeries{labels: map[string]string{"source_workload": "kubephos-load-journey", "destination_workload": "node-proxy", "pod": "np"}, base: 50, perSecond: 10},
					counterSeries{labels: map[string]string{"source_workload": "node-proxy", "destination_workload": "frontend", "pod": "f1"}, base: 50, perSecond: 10},
					counterSeries{labels: map[string]string{"source_workload": "frontend", "destination_workload": "currencyservice", "pod": "c1"}, base: 0, perSecond: 30},
					// A new edge appearing mid-window is counted from zero.
					counterSeries{labels: map[string]string{"source_workload": "frontend", "destination_workload": "productcatalogservice", "pod": "p1"}, base: 0, perSecond: 10, bornAfter: 50 * time.Second},
				), true, nil
			}
			return counterMatrix(command,
				counterSeries{labels: map[string]string{"source_workload": "node-proxy", "destination_workload": "frontend", "pod": "f1"}, base: 900, perSecond: 5},
				counterSeries{labels: map[string]string{"source_workload": "frontend", "destination_workload": "checkoutservice", "pod": "k1"}, base: 0, perSecond: 5},
			), true, nil
		}
	}}
}

func TestCallModelFromProfilingWindows(t *testing.T) {
	fake := &callModelFake{executions: true}
	dataset := executeVerified(t, fake.runner(), profileStep(t))
	model := dataset.Spec.CallModel
	if model == nil {
		t.Fatal("call model missing")
	}
	if model.Source != "istio" || model.ApplicationRef != "app:dev.example.app" || model.ScenarioID != "journey" || model.WindowTrimSeconds != 15 || model.StepSeconds != 5 || model.RPS != 10 || model.SecondsPerTask != 120 || !model.MeasuredAt.Equal(checkoutFrom.Add(2*time.Minute)) {
		t.Fatalf("unexpected model header %+v", model)
	}
	if !reflect.DeepEqual(model.Proxies, []string{"node-proxy"}) || len(model.Tasks) != 2 {
		t.Fatalf("unexpected proxies/tasks %+v", model)
	}
	browse := model.Tasks[0]
	// 105 s counted (120 s window - 15 s trim): 5 executions/s, 10 requests/s.
	if browse.Task != "browseProduct" || browse.Executions == nil || *browse.Executions != 525 || browse.EntryRequests != 1050 || !browse.From.Equal(browseFrom.Add(15*time.Second)) {
		t.Fatalf("unexpected browse counts %+v", browse)
	}
	// productcatalogservice is born 50 s into the interval (first sample 500, last 1050): counted from zero.
	wantServices := map[string]float64{"node-proxy": 2, "frontend": 2, "currencyservice": 6, "productcatalogservice": 2}
	if !reflect.DeepEqual(browse.Services, wantServices) {
		t.Fatalf("unexpected per-execution services %v", browse.Services)
	}
	if browse.ServicesPerRequest["currencyservice"] != 3 {
		t.Fatalf("unexpected per-request services %v", browse.ServicesPerRequest)
	}
	edges := map[string]float64{}
	for _, edge := range browse.Edges {
		if strings.HasPrefix(edge.Source, "kubephos-load-") || edge.PerExecution == nil || edge.PerRequest == nil {
			t.Fatalf("unexpected edge %+v", edge)
		}
		edges[edge.Source+">"+edge.Destination] = *edge.PerExecution
	}
	if !reflect.DeepEqual(edges, map[string]float64{"node-proxy>frontend": 2, "frontend>currencyservice": 6, "frontend>productcatalogservice": 2}) {
		t.Fatalf("unexpected edges %v", edges)
	}
	checkout := model.Tasks[1]
	if checkout.Task != "checkout" || *checkout.Executions != 525 || !reflect.DeepEqual(checkout.Services, map[string]float64{"frontend": 1, "checkoutservice": 1}) {
		t.Fatalf("unexpected checkout %+v", checkout)
	}
	for _, query := range fake.queries {
		if strings.Contains(query, "istio_requests_total") && (!strings.Contains(query, `reporter=~"destination|waypoint"`) || !strings.Contains(query, `destination_workload!~"kubephos-load-.*|kubephos-pod-scheduling.*"`)) {
			t.Fatalf("istio query not filtered: %s", query)
		}
	}
	var mix []map[string]any
	if err := json.Unmarshal(dataset.Spec.TaskMix, &mix); err != nil || len(mix) != 2 || mix[0]["task"] != "browseProduct" || mix[1]["share"] != 0.25 {
		t.Fatalf("task mix not copied: %s", dataset.Spec.TaskMix)
	}
	raw, _ := json.Marshal(dataset.Spec)
	for _, field := range []string{`"callModel":{"source":"istio"`, `"perExecution":6`, `"windowTrimSeconds":15`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("missing %s in %s", field, raw)
		}
	}
}

func TestCallModelTrimFollowsScrapeInterval(t *testing.T) {
	fake := &callModelFake{executions: true, scrapeEvery: "30s"}
	dataset := executeVerified(t, fake.runner(), profileStep(t))
	if dataset.Spec.CallModel == nil || dataset.Spec.CallModel.WindowTrimSeconds != 30 || dataset.Spec.CallModel.ScrapeIntervalSeconds != 30 || !dataset.Spec.CallModel.Tasks[0].From.Equal(browseFrom.Add(30*time.Second)) {
		t.Fatalf("unexpected trim %+v", dataset.Spec.CallModel)
	}
	if callModelTrim(0) != 15*time.Second || callModelTrim(7500*time.Millisecond) != 15*time.Second {
		t.Fatal("trim must be at least 15 s")
	}
}

func TestCallModelWithoutExecutionsCounterUsesEntryRequests(t *testing.T) {
	fake := &callModelFake{}
	dataset := executeVerified(t, fake.runner(), profileStep(t))
	model := dataset.Spec.CallModel
	if model == nil || len(model.Tasks) != 2 {
		t.Fatalf("call model missing %+v", model)
	}
	browse := model.Tasks[0]
	if browse.Executions != nil || browse.Services != nil || browse.EntryRequests != 1050 || browse.ServicesPerRequest["frontend"] != 1 || browse.ServicesPerRequest["currencyservice"] != 3 {
		t.Fatalf("unexpected fallback %+v", browse)
	}
	for _, edge := range browse.Edges {
		if edge.PerExecution != nil || edge.PerRequest == nil {
			t.Fatalf("unexpected fallback edge %+v", edge)
		}
	}
}

func TestCallModelFailureKeepsRunSuccessful(t *testing.T) {
	fake := &callModelFake{executions: true, failIstio: true}
	dataset := executeVerified(t, fake.runner(), profileStep(t))
	if dataset.Spec.CallModel != nil || dataset.Spec.TaskMix == nil {
		t.Fatalf("failed call model must be omitted, task mix kept: %+v", dataset.Spec.CallModel)
	}
	// A malformed profile never fails the run either.
	step := profileStep(t)
	editArtifact(t, step, "load-session", func(value map[string]any) {
		value["spec"].(map[string]any)["callChainProfile"] = map[string]any{"windows": "nope"}
	})
	if dataset := executeVerified(t, (&callModelFake{executions: true}).runner(), step); dataset.Spec.CallModel != nil {
		t.Fatal("malformed profile must be ignored")
	}
	// Legacy sessions have neither field.
	legacy := executeVerified(t, (&callModelFake{executions: true}).runner(), testStep(t))
	if legacy.Spec.CallModel != nil || legacy.Spec.TaskMix != nil {
		t.Fatal("legacy session produced a call model")
	}
}

func TestCallModelQueriesHonourNamespaceReuse(t *testing.T) {
	created := time.Unix(1234567890, 0)
	var deployment applicationDeployment
	deployment.Spec.Namespace = "benchmark"
	deployment.Spec.NamespaceCreatedAt = &created
	executions, requests, istio := callModelQueries(deployment)
	for _, query := range []string{executions, requests, istio} {
		if strings.Count(query, `and on (pod) (max by (pod) (kube_pod_created{namespace="benchmark"} >= 1234567890))`) != 1 {
			t.Fatalf("query not scoped: %s", query)
		}
	}
	step := profileStep(t)
	editArtifact(t, step, "application-deployment", func(value map[string]any) {
		value["spec"].(map[string]any)["namespaceCreatedAt"] = created
	})
	fake := &callModelFake{executions: true}
	executeVerified(t, fake.runner(), step)
	if len(fake.queries) != 6 {
		t.Fatalf("expected 3 queries per window, got %d", len(fake.queries))
	}
	for _, query := range fake.queries {
		if !strings.Contains(query, `kube_pod_created{namespace="kubephos-app-one"} >= 1234567890`) {
			t.Fatalf("executed query not scoped: %s", query)
		}
	}
}

func TestCounterIncreaseHandlesResetsAndBirths(t *testing.T) {
	start := testNow
	at := func(seconds int, value float64) metricPoint {
		return metricPoint{Timestamp: start.Add(time.Duration(seconds) * time.Second), Value: value}
	}
	if got := counterIncrease([]metricPoint{at(0, 10), at(5, 15), at(10, 3), at(15, 8)}, start); got != 13 {
		t.Fatalf("reset: %v", got)
	}
	if got := counterIncrease([]metricPoint{at(10, 4), at(15, 6)}, start); got != 6 {
		t.Fatalf("birth: %v", got)
	}
	if got := counterIncrease([]metricPoint{at(0, 4), at(5, 6), at(30, 2)}, start); got != 4 {
		t.Fatalf("gap: %v", got)
	}
}
