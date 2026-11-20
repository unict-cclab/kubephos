package metricscollector

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

var testNow = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

func executeVerified(t *testing.T, runner *fakeRunner, step domain.PlanStep) timeSeriesDataset {
	t.Helper()
	plugin := Plugin{Runner: runner, Clock: func() time.Time { return testNow }}
	raw, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), step, raw, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value.Dataset
}

func editArtifact(t *testing.T, step domain.PlanStep, name string, edit func(map[string]any)) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(step.ResolvedInputs[name].Value, &value); err != nil {
		t.Fatal(err)
	}
	edit(value)
	raw, _ := json.Marshal(value)
	step.ResolvedInputs[name] = domain.ResolvedArtifact{Value: raw}
}

func editSpec(t *testing.T, step *domain.PlanStep, edit func(*Spec)) {
	t.Helper()
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		t.Fatal(err)
	}
	edit(&spec)
	step.Input, _ = json.Marshal(spec)
}

func vectorResponse(samples ...map[string]any) string {
	result := []map[string]any{}
	for _, sample := range samples {
		labels := map[string]string{}
		value := "1"
		for key, item := range sample {
			if key == "__value" {
				value = item.(string)
				continue
			}
			labels[key] = item.(string)
		}
		result = append(result, map[string]any{"metric": labels, "value": []any{testNow.Unix(), value}})
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
	return string(raw)
}

func matrixResponse(values ...string) string {
	points := [][]any{}
	for index, value := range values {
		points = append(points, []any{testNow.Add(-time.Minute).Add(time.Duration(index) * 15 * time.Second).Unix(), value})
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []map[string]any{{"metric": map[string]string{}, "values": points}}}})
	return string(raw)
}

const emptyMatrix = `{"status":"success","data":{"resultType":"matrix","result":[]}}`

func TestCatalogDescribesEverySeriesMetric(t *testing.T) {
	step := testStep(t)
	editArtifact(t, step, "application-deployment", func(value map[string]any) {
		value["spec"].(map[string]any)["controlMetrics"] = map[string]any{"startedAt": testNow.Add(-2 * time.Minute), "schedulerName": "default-scheduler"}
	})
	dataset := executeVerified(t, &fakeRunner{now: testNow}, step)
	entries := map[string]int{}
	byMetric := map[string]metricCatalogEntry{}
	for _, entry := range dataset.Spec.Metrics {
		entries[entry.Metric]++
		byMetric[entry.Metric] = entry
		if entry.Source == "" || entry.Vantage == "" || entry.Collector == "" || entry.Description == "" {
			t.Fatalf("incomplete catalog entry %+v", entry)
		}
	}
	present := map[string]bool{}
	for _, series := range dataset.Spec.Series {
		present[series.Metric] = true
		if entries[series.Metric] != 1 {
			t.Fatalf("%s has %d catalog entries", series.Metric, entries[series.Metric])
		}
	}
	if len(present) != len(entries) || !present["scheduler.cycle_duration"] || !present["load.slo_violation_percentage"] {
		t.Fatalf("catalog and series disagree: %v %v", present, entries)
	}
	for metric, want := range map[string][2]string{
		"load.p95_response_time":        {sourceLocust, vantageClient},
		"application.p95_response_time": {sourceIstio, vantageServer},
		"cpu.cores":                     {sourceCadvisor, vantageResource},
		"pod.restarts":                  {sourceKubeStateMetrics, vantageResource},
		"network.rtt":                   {sourceMentat, vantageNetwork},
		"load.configured_rps":           {sourceKubePhos, vantageConfiguration},
		"load.slo_violation_percentage": {sourceKubePhos, vantageClient},
		"scheduler.cycle_duration":      {sourceKubeScheduler, vantageControlPlane},
	} {
		if entry := byMetric[metric]; entry.Source != want[0] || entry.Vantage != want[1] {
			t.Fatalf("%s provenance %+v", metric, entry)
		}
	}
	p95 := byMetric["load.p95_response_time"]
	if p95.Query != `histogram_quantile(0.95, sum by (le) (rate(kubephos_load_duration_milliseconds_bucket{namespace="kubephos-app-one"}[1m])))` || p95.RateWindowSeconds != 60 || p95.Collector != collectorPrometheus {
		t.Fatalf("catalog does not record the query sent: %+v", p95)
	}
	if byMetric["memory.working_set"].RateWindowSeconds != 0 || byMetric["load.configured_rps"].Query != "" {
		t.Fatal("instant and configured metrics must not claim a rate window or query")
	}
	if dataset.Spec.RateWindowSeconds != 60 || dataset.Spec.ScrapeIntervalSeconds != 0 {
		t.Fatalf("unexpected window %d / scrape %d", dataset.Spec.RateWindowSeconds, dataset.Spec.ScrapeIntervalSeconds)
	}
}

func TestSelectRateWindow(t *testing.T) {
	for _, item := range []struct {
		config             string
		resolution, scrape time.Duration
		want               time.Duration
	}{
		{"auto", 15 * time.Second, 15 * time.Second, time.Minute},
		{"", 15 * time.Second, 5 * time.Second, 20 * time.Second},
		{"auto", time.Minute, 5 * time.Second, time.Minute},
		{"auto", 5 * time.Second, 2500 * time.Millisecond, 10 * time.Second},
		{"auto", 5 * time.Second, 1700 * time.Millisecond, 7 * time.Second},
		{"auto", 15 * time.Second, 0, time.Minute},
		{"2m", 15 * time.Second, 5 * time.Second, 2 * time.Minute},
		{"30s", time.Minute, 0, 30 * time.Second},
		// An explicit window shorter than two scrapes would leave required series empty.
		{"30s", 15 * time.Second, 30 * time.Second, time.Minute},
		{"30s", 15 * time.Second, 15 * time.Second, 30 * time.Second},
	} {
		if got := selectRateWindow(item.config, item.resolution, item.scrape); got != item.want {
			t.Fatalf("%+v: got %s", item, got)
		}
	}
	if formatPromDuration(20*time.Second) != "20s" || formatPromDuration(2*time.Minute) != "2m" {
		t.Fatal("unexpected PromQL duration format")
	}
	report := (Plugin{}).Validate(context.Background(), Invocation{Input: json.RawMessage(`{"clusterConnectionRef":"art_a","applicationDeploymentRef":"art_b","observabilityCapabilityRef":"art_c","loadSessionRef":"art_d","window":"5m","resolution":"15s","rateWindow":"7m"}`)})
	if report.Valid {
		t.Fatal("unsupported rate window accepted")
	}
}

type recordedQueries struct {
	mu      sync.Mutex
	queries []string
}

func (r *recordedQueries) add(command, query string) {
	if strings.Contains(command, "/api/v1/query_range?") {
		r.mu.Lock()
		r.queries = append(r.queries, query)
		r.mu.Unlock()
	}
}

func TestRateWindowFollowsDetectedScrapeInterval(t *testing.T) {
	targets := `{"status":"success","data":{"activeTargets":[{"scrapePool":"kubernetes-nodes","labels":{"job":"kubelet"},"scrapeInterval":"30s"},{"scrapePool":"istio-mesh","scrapeUrl":"http://10.0.0.4:9646/stats/prometheus","labels":{"job":"istio-mesh","namespace":"kubephos-app-one","pod":"kubephos-load-journey-1"},"scrapeInterval":"10s"}]}}`
	recorded := &recordedQueries{}
	runner := &fakeRunner{now: testNow, override: func(command, query string) (string, bool, error) {
		recorded.add(command, query)
		if strings.Contains(command, "/api/v1/targets") {
			return targets, true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, testStep(t))
	if dataset.Spec.RateWindowSeconds != 40 || dataset.Spec.ScrapeIntervalSeconds != 10 || dataset.Spec.RunContext == nil || dataset.Spec.RunContext.Prometheus == nil || dataset.Spec.RunContext.Prometheus.ScrapeIntervalSeconds != 10 {
		t.Fatalf("auto window ignored the 10s scrape interval: %d %d %+v", dataset.Spec.RateWindowSeconds, dataset.Spec.ScrapeIntervalSeconds, dataset.Spec.RunContext)
	}
	windowed := 0
	for _, query := range recorded.queries {
		if strings.Contains(query, rateWindowToken) || strings.Contains(query, "[1m]") {
			t.Fatalf("query kept the legacy window: %s", query)
		}
		if strings.Contains(query, "[40s]") {
			windowed++
		}
	}
	if windowed < 15 {
		t.Fatalf("expected the detected window in the rate queries, got %d", windowed)
	}
	for _, entry := range dataset.Spec.Metrics {
		if strings.Contains(entry.Query, "rate(") && entry.RateWindowSeconds != 40 {
			t.Fatalf("catalog window mismatch %+v", entry)
		}
	}

	// An explicit window wins over the detected interval.
	step := testStep(t)
	editSpec(t, &step, func(spec *Spec) { spec.RateWindow = "5m" })
	dataset = executeVerified(t, &fakeRunner{now: testNow, override: func(command, _ string) (string, bool, error) {
		if strings.Contains(command, "/api/v1/targets") {
			return targets, true, nil
		}
		return "", false, nil
	}}, step)
	if dataset.Spec.RateWindowSeconds != 300 || dataset.Spec.ScrapeIntervalSeconds != 10 {
		t.Fatalf("explicit window not honoured: %d", dataset.Spec.RateWindowSeconds)
	}
}

func TestScrapeIntervalFallsBackToPrometheusConfig(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]string{"yaml": "global:\n  scrape_interval: 5s\nscrape_configs:\n- job_name: istio-mesh\n  scrape_interval: 30s\n- job_name: other\n"}})
	runner := &fakeRunner{now: testNow, override: func(command, _ string) (string, bool, error) {
		if strings.Contains(command, "/api/v1/status/config") {
			return string(config), true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, testStep(t))
	if dataset.Spec.ScrapeIntervalSeconds != 30 || dataset.Spec.RateWindowSeconds != 120 {
		t.Fatalf("config fallback: scrape %d window %d", dataset.Spec.ScrapeIntervalSeconds, dataset.Spec.RateWindowSeconds)
	}
	if interval, job := scrapeIntervalFromConfig(string(config), "missing"); interval != 5*time.Second || job != "global" {
		t.Fatalf("global interval: %s %s", interval, job)
	}
	if interval, _ := scrapeIntervalFromConfig(`{"status":"error"}`, "istio-mesh"); interval != 0 {
		t.Fatal("failed config must be unknown")
	}
}

func optionalQueries(t *testing.T, step domain.PlanStep) (map[string]string, map[string]string) {
	t.Helper()
	_, _, deployment, _, _, err := resolve(step)
	if err != nil {
		t.Fatal(err)
	}
	optional, required := map[string]string{}, map[string]string{}
	for _, definition := range applyRateWindow(deploymentMetricDefinitions(deployment), time.Minute) {
		if definition.Optional {
			optional[definition.Query] = definition.ID
		} else {
			required[definition.Query] = definition.ID
		}
	}
	return optional, required
}

func TestOptionalMetricsMayBeMissing(t *testing.T) {
	step := testStep(t)
	optional, required := optionalQueries(t, step)
	for _, id := range []string{"load.p50_response_time", "load.p90_response_time", "load.p99_response_time", "load.mean_response_time", "application.p50_response_time", "application.p90_response_time", "application.p99_response_time", "application.mean_response_time", "load.offered_rps", "load.achieved_rps", "load.schedule_lag", "load.users", "load.failure_percentage", "statefulset.ready_replicas", "daemonset.ready_replicas"} {
		found := false
		for _, candidate := range optional {
			found = found || candidate == id
		}
		if !found {
			t.Fatalf("%s must be optional", id)
		}
	}
	runner := &fakeRunner{now: testNow, override: func(command, query string) (string, bool, error) {
		if _, ok := optional[query]; ok && strings.Contains(command, "query_range") {
			if strings.Contains(query, "kubephos_load_users") {
				return "", true, context.DeadlineExceeded
			}
			return emptyMatrix, true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, step)
	for _, series := range dataset.Spec.Series {
		for _, id := range optional {
			if series.Metric == id {
				t.Fatalf("unexpected optional series %s", id)
			}
		}
	}
	for _, entry := range dataset.Spec.Metrics {
		for _, id := range optional {
			if entry.Metric == id {
				t.Fatalf("catalog describes absent metric %s", id)
			}
		}
	}

	var p95 string
	for query, id := range required {
		if id == "load.p95_response_time" {
			p95 = query
		}
	}
	failing := &fakeRunner{now: testNow, override: func(_, query string) (string, bool, error) {
		if query == p95 {
			return emptyMatrix, true, nil
		}
		return "", false, nil
	}}
	_, err := (Plugin{Runner: failing, Clock: func() time.Time { return testNow }}).Execute(context.Background(), step, discardLog)
	if err == nil || !strings.Contains(err.Error(), "load.p95_response_time") {
		t.Fatalf("a missing required metric must still fail the run: %v", err)
	}
}

func TestFailurePercentageIsUnavailableWithoutTraffic(t *testing.T) {
	var failure metricDefinition
	for _, definition := range locustMetricDefinitions("run") {
		if definition.ID == "load.failure_percentage" {
			failure = definition
		}
	}
	if strings.Contains(failure.Query, "clamp_min") || !failure.Optional || failure.Query != `100 * (sum(rate(kubephos_load_requests_total{namespace="run",outcome="failure"}`+rateWindowRange+`)) or 0 * sum(rate(kubephos_load_requests_total{namespace="run"}`+rateWindowRange+`))) / sum(rate(kubephos_load_requests_total{namespace="run"}`+rateWindowRange+`))` {
		t.Fatalf("failure percentage must be undefined without traffic: %s", failure.Query)
	}
	step := testStep(t)
	optional, _ := optionalQueries(t, step)
	idle := map[string]string{}
	for query, id := range optional {
		switch id {
		case "load.failure_percentage":
			idle[query] = matrixResponse("NaN", "NaN", "NaN")
		case "load.mean_response_time":
			idle[query] = matrixResponse("NaN", "12.5", "NaN")
		}
	}
	runner := &fakeRunner{now: testNow, override: func(_, query string) (string, bool, error) {
		if response, ok := idle[query]; ok {
			return response, true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, step)
	for _, series := range dataset.Spec.Series {
		if series.Metric == "load.failure_percentage" {
			t.Fatal("an idle run must have no failure percentage sample")
		}
		if series.Metric == "load.mean_response_time" && (len(series.Points) != 1 || series.Points[0].Value != 12.5) {
			t.Fatalf("idle windows must be skipped, not zero: %+v", series.Points)
		}
	}
}

func TestLocustSummaryAndLoadSessionContextPassThrough(t *testing.T) {
	step := testStep(t)
	summary := map[string]any{"workers": 4.0, "requests": 12000.0, "failures": 3.0, "total": map[string]any{"method": "", "name": "Aggregated", "p95Ms": 30.0}, "endpoints": []any{map[string]any{"method": "GET", "name": "/cart", "p99Ms": 80.0}}, "schedule": map[string]any{"lateRequests": 12.0}}
	warmupStarted := testNow.Add(-2 * time.Minute)
	editArtifact(t, step, "load-session", func(value map[string]any) {
		spec := value["spec"].(map[string]any)
		spec["locustSummary"] = summary
		spec["warmupSeconds"] = 60
		spec["warmupStartedAt"] = warmupStarted
		spec["drainSeconds"] = 30
		spec["clock"] = "shared-start"
		spec["replicas"] = 2
	})
	// Every run context probe fails with the default fake runner.
	dataset := executeVerified(t, &fakeRunner{now: testNow}, step)
	var copied map[string]any
	if err := json.Unmarshal(dataset.Spec.LocustSummary, &copied); err != nil || !reflect.DeepEqual(copied, summary) {
		t.Fatalf("locust summary not copied verbatim: %s %v", dataset.Spec.LocustSummary, err)
	}
	run := dataset.Spec.RunContext
	if run == nil || run.LoadSession == nil || *run.LoadSession.WarmupSeconds != 60 || !run.LoadSession.WarmupStartedAt.Equal(warmupStarted) || *run.LoadSession.DrainSeconds != 30 || run.LoadSession.Clock != "shared-start" || run.LoadSession.Workers != 4 {
		t.Fatalf("unexpected load session context %+v", run)
	}
	if run.Prometheus != nil || run.Kubernetes != nil || run.Istio != nil || run.Nodes != nil || run.ConcurrentLoad != nil {
		t.Fatalf("failed probes must leave fields absent: %+v", run)
	}

	// Legacy load sessions have neither field.
	legacy := executeVerified(t, &fakeRunner{now: testNow}, testStep(t))
	if legacy.Spec.LocustSummary != nil || legacy.Spec.RunContext == nil || legacy.Spec.RunContext.LoadSession != nil {
		t.Fatalf("legacy load session produced context %+v", legacy.Spec.RunContext)
	}
}

func TestRunContextProbesAndWorkloadLabels(t *testing.T) {
	nodes := `{"items":[{"metadata":{"name":"n1","labels":{"topology.kubernetes.io/zone":"eu-1a","topology.kubernetes.io/region":"eu-1","node.kubernetes.io/instance-type":"m5.large"}},"status":{"nodeInfo":{"kubeletVersion":"v1.31.2","containerRuntimeVersion":"containerd://1.7","kernelVersion":"6.1"}}},{"metadata":{"name":"n2","labels":{"failure-domain.beta.kubernetes.io/zone":"eu-1b"}},"status":{"nodeInfo":{}}}]}`
	recorded := &recordedQueries{}
	runner := &fakeRunner{now: testNow, override: func(command, query string) (string, bool, error) {
		recorded.add(command, query)
		switch {
		case strings.HasSuffix(command, "/api/v1/status/buildinfo"):
			return `{"status":"success","data":{"version":"2.53.0"}}`, true, nil
		case command == "get --raw=/version":
			return `{"gitVersion":"v1.31.2"}`, true, nil
		case command == "get nodes -o json":
			return nodes, true, nil
		case strings.Contains(query, "istio_build"):
			return vectorResponse(map[string]any{"component": "proxy", "tag": "1.24.0", "__value": "3"}, map[string]any{"component": "pilot", "tag": "1.24.1"}), true, nil
		case strings.Contains(query, `pod=~"kubephos-load-.*"`):
			if !strings.Contains(query, `namespace!="kubephos-app-one"`) {
				t.Errorf("concurrent load must exclude the run's namespace: %s", query)
			}
			return vectorResponse(map[string]any{"namespace": "other-ns", "__value": "2"}), true, nil
		case strings.Contains(query, "kube_pod_owner") && strings.Contains(command, "/api/v1/query?"):
			return vectorResponse(map[string]any{"pod": "one", "owner_kind": "ReplicaSet", "owner_name": "frontend-7d9f", "owner_is_controller": "true"}), true, nil
		case strings.Contains(query, "kube_replicaset_owner") && strings.Contains(command, "/api/v1/query?"):
			return vectorResponse(map[string]any{"replicaset": "frontend-7d9f", "owner_kind": "Deployment", "owner_name": "frontend", "owner_is_controller": "true"}), true, nil
		case strings.Contains(query, "kube_pod_info") && strings.Contains(command, "/api/v1/query?"):
			return vectorResponse(map[string]any{"pod": "one", "node": "n1"}), true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, testStep(t))
	run := dataset.Spec.RunContext
	if run == nil || run.Prometheus == nil || run.Prometheus.Version != "2.53.0" || run.Kubernetes == nil || run.Kubernetes.ServerVersion != "v1.31.2" || run.Istio == nil || run.Istio.Version != "1.24.1" {
		t.Fatalf("unexpected versions %+v", run)
	}
	if len(run.Nodes) != 2 || run.Nodes[0] != (runContextNode{Name: "n1", Zone: "eu-1a", Region: "eu-1", InstanceType: "m5.large", Kubelet: "v1.31.2", ContainerRuntime: "containerd://1.7", Kernel: "6.1"}) || run.Nodes[1].Zone != "eu-1b" {
		t.Fatalf("unexpected nodes %+v", run.Nodes)
	}
	if run.ConcurrentLoad == nil || !reflect.DeepEqual(*run.ConcurrentLoad, []concurrentLoad{{Namespace: "other-ns", MaxPods: 2}}) {
		t.Fatalf("unexpected concurrent load %+v", run.ConcurrentLoad)
	}
	labelled := 0
	for _, series := range dataset.Spec.Series {
		if podScopedMetrics[series.Metric] {
			if series.Labels["workload"] != "frontend" || series.Labels["workload_kind"] != "Deployment" || series.Labels["node"] != "n1" || series.Labels["zone"] != "eu-1a" {
				t.Fatalf("pod series not attributed: %s %v", series.Metric, series.Labels)
			}
			labelled++
		}
	}
	if labelled != 5 {
		t.Fatalf("expected five labelled pod-scoped series, got %d", labelled)
	}
}

func TestRunContextWithoutLoadGeneratorsIsEmptyList(t *testing.T) {
	runner := &fakeRunner{now: testNow, override: func(_, query string) (string, bool, error) {
		if strings.Contains(query, `pod=~"kubephos-load-.*"`) {
			return vectorResponse(), true, nil
		}
		return "", false, nil
	}}
	dataset := executeVerified(t, runner, testStep(t))
	raw, _ := json.Marshal(dataset.Spec.RunContext)
	if !strings.Contains(string(raw), `"concurrentLoad":[]`) {
		t.Fatalf("no concurrent load must be an empty list: %s", raw)
	}
}

func TestPodWorkloadResolution(t *testing.T) {
	owner := func(pod, kind, name string) vectorSample {
		controller := "true"
		if kind == "<none>" {
			controller = "<none>"
		}
		return vectorSample{Labels: map[string]string{"pod": pod, "owner_kind": kind, "owner_name": name, "owner_is_controller": controller}, Value: 1}
	}
	owners := []vectorSample{
		owner("cart-7d9f-abc", "ReplicaSet", "cart-7d9f"),
		owner("cart-db-0", "StatefulSet", "cart-db"),
		owner("fluent-x2k", "DaemonSet", "fluent"),
		owner("migrate-q8", "Job", "migrate"),
		owner("debug", "<none>", "<none>"),
		owner("orphan-5c-x", "ReplicaSet", "orphan-5c"),
		owner("standalone-rs-1", "ReplicaSet", "standalone-rs"),
		owner("reused", "StatefulSet", "a"),
		owner("reused", "StatefulSet", "b"),
	}
	replicaSets := []vectorSample{
		{Labels: map[string]string{"replicaset": "cart-7d9f", "owner_kind": "Deployment", "owner_name": "cart", "owner_is_controller": "true"}, Value: 1},
		{Labels: map[string]string{"replicaset": "standalone-rs", "owner_kind": "<none>", "owner_name": "<none>", "owner_is_controller": "<none>"}, Value: 1},
	}
	nodes := []vectorSample{
		{Labels: map[string]string{"pod": "cart-7d9f-abc", "node": "n1"}, Value: 1},
		{Labels: map[string]string{"pod": "cart-db-0", "node": "n1"}, Value: 1},
		{Labels: map[string]string{"pod": "cart-db-0", "node": "n2"}, Value: 1},
		{Labels: map[string]string{"pod": "fluent-x2k", "node": "n2"}, Value: 1},
	}
	pods := podWorkloads(owners, replicaSets, nodes, map[string]string{"n1": "eu-1a"})
	want := map[string]podWorkload{
		"cart-7d9f-abc":   {Workload: "cart", Kind: "Deployment", Node: "n1", Zone: "eu-1a"},
		"cart-db-0":       {Workload: "cart-db", Kind: "StatefulSet"},
		"fluent-x2k":      {Workload: "fluent", Kind: "DaemonSet", Node: "n2"},
		"migrate-q8":      {Workload: "migrate", Kind: "Job"},
		"debug":           {Workload: "debug", Kind: "Pod"},
		"standalone-rs-1": {Workload: "standalone-rs", Kind: "ReplicaSet"},
	}
	if !reflect.DeepEqual(pods, want) {
		t.Fatalf("unexpected resolution\n got: %+v\nwant: %+v", pods, want)
	}
	series := []metricSeries{
		{Metric: "cpu.cores", Labels: map[string]string{"pod": "cart-db-0"}},
		{Metric: "memory.working_set", Labels: map[string]string{"pod": "cart-7d9f-abc"}},
		{Metric: "pod.restarts", Labels: map[string]string{"pod": "orphan-5c-x"}},
		{Metric: "deployment.ready_replicas", Labels: map[string]string{"deployment": "cart"}},
		{Metric: "deployment.ready_replicas", Labels: map[string]string{"statefulset": "redis-primary-1"}},
		{Metric: "daemonset.ready_replicas", Labels: map[string]string{"daemonset": "fluent"}},
		{Metric: "network.rtt", Labels: map[string]string{"pod": "cart-db-0"}},
	}
	labelWorkloads(series, pods)
	if series[0].Labels["workload"] != "cart-db" || series[0].Labels["workload_kind"] != "StatefulSet" || series[0].Labels["node"] != "" {
		t.Fatalf("StatefulSet pod grouped under the wrong workload: %v", series[0].Labels)
	}
	if series[1].Labels["workload"] != "cart" || series[1].Labels["zone"] != "eu-1a" {
		t.Fatalf("Deployment pod not resolved through its ReplicaSet: %v", series[1].Labels)
	}
	if _, ok := series[2].Labels["workload"]; ok {
		t.Fatal("unresolved pods must keep no workload label")
	}
	if series[3].Labels["workload_kind"] != "Deployment" || series[4].Labels["workload_kind"] != "StatefulSet" || series[5].Labels["workload"] != "fluent" || series[5].Labels["workload_kind"] != "DaemonSet" {
		t.Fatalf("ready replica series not grouped: %v %v %v", series[3].Labels, series[4].Labels, series[5].Labels)
	}
	if _, ok := series[6].Labels["workload"]; ok {
		t.Fatal("non pod-scoped metrics must not be relabelled")
	}
}

func TestReusedNamespaceScopingCoversNewQueries(t *testing.T) {
	created := time.Unix(1234567890, 0)
	for _, endpoints := range [][]struct {
		Protocol string `json:"protocol"`
	}{nil, {{Protocol: "tcp"}}} {
		var deployment applicationDeployment
		deployment.Spec.Namespace = "benchmark"
		deployment.Spec.NamespaceCreatedAt = &created
		deployment.Spec.Endpoints = endpoints
		seen := map[string]bool{}
		unscoped := deploymentMetricDefinitions(deployment)
		for index, definition := range applyRateWindow(scopeReusableNamespaceMetrics(deploymentMetricDefinitions(deployment), deployment), 40*time.Second) {
			seen[definition.ID] = true
			if strings.Contains(definition.Query, rateWindowToken) {
				t.Fatalf("unresolved window in %s", definition.Query)
			}
			if !strings.HasPrefix(definition.ID, "load.") && !strings.HasPrefix(definition.ID, "application.") {
				continue
			}
			original := unscoped[index].Query
			selectors := strings.Count(original, `{namespace="benchmark"`) + strings.Count(original, `destination_workload_namespace="benchmark"`)
			if scoped := strings.Count(definition.Query, `kube_pod_created{namespace="benchmark"} >= 1234567890`); scoped != selectors || scoped == 0 {
				t.Fatalf("%s: %d of %d selectors scoped: %s", definition.ID, scoped, selectors, definition.Query)
			}
		}
		for _, id := range []string{"load.p50_response_time", "load.p99_response_time", "load.mean_response_time", "load.offered_rps", "load.achieved_rps", "load.schedule_lag", "load.users", "load.failure_percentage"} {
			if !seen[id] {
				t.Fatalf("missing %s", id)
			}
		}
		if endpoints == nil && !seen["application.mean_response_time"] {
			t.Fatal("missing application mean")
		}
	}
}
