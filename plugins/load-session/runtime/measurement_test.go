package loadsession

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func finiteStep(t *testing.T, replicas, warmup int, drain *int) domain.PlanStep {
	t.Helper()
	spec := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: "start", Replicas: replicas, MaxUsers: 20, SpawnRate: 2, Steps: []LoadStep{{Type: "constant", DurationSeconds: 1, RPS: 10}}, WarmupSeconds: warmup, DrainSeconds: drain}
	raw, _ := json.Marshal(spec)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	return step
}

// fastSharedStart shortens the start lead and replaces the drain sleep so a full
// finite session runs in a few seconds; it returns the recorded drain requests.
func fastSharedStart(t *testing.T, onDrain func()) *[]time.Duration {
	t.Helper()
	lead, drain := sharedStartLead, drainWait
	drained := []time.Duration{}
	sharedStartLead = func(int, int) time.Duration { return 2 * time.Second }
	drainWait = func(_ context.Context, duration time.Duration) error {
		drained = append(drained, duration)
		if onDrain != nil {
			onDrain()
		}
		return nil
	}
	t.Cleanup(func() { sharedStartLead, drainWait = lead, drain })
	return &drained
}

func workerSummaryJSON(measuredFrom time.Time, requests int, times map[string]int, lagCount int, lagSum float64, late int, maxLag float64) string {
	encodedTimes, _ := json.Marshal(times)
	entry := fmt.Sprintf(`{"method":"GET","name":"/cart","numRequests":%d,"numNoneRequests":0,"numFailures":1,"totalResponseTime":%d,"minResponseTime":4.2,"maxResponseTime":512.5,"responseTimes":%s}`, requests, requests*20, encodedTimes)
	total := strings.Replace(strings.Replace(entry, `"method":"GET"`, `"method":""`, 1), `"name":"/cart"`, `"name":"Aggregated"`, 1)
	return fmt.Sprintf(`{"phase":2,"measuredFrom":%d,"entries":[%s],"total":%s,"errors":[{"method":"GET","name":"/cart","error":"HTTPError('503 Server Error')","occurrences":1}],"cpu":{"maxPercent":93.5,"warnings":2},"schedule":{"count":%d,"lagSum":%g,"late":%d,"maxLag":%g}}`, measuredFrom.Unix(), entry, total, lagCount, lagSum, late, maxLag)
}

func TestSharedStartRecordsExactWindowDrainsAndMergesSummary(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	drainedAt := -1
	drained := fastSharedStart(t, func() { drainedAt = len(runner.commands) })
	now := time.Date(2030, 1, 2, 3, 4, 5, 400_000_000, time.UTC)
	warmupStart := time.Date(2030, 1, 2, 3, 4, 7, 0, time.UTC)
	started := warmupStart.Add(time.Second)
	runner.summaries = map[string]string{
		fakePodName(0): workerSummaryJSON(started, 4, map[string]int{"10": 2, "20": 1, "300": 1}, 10, 1.0, 2, 0.5),
		fakePodName(1): workerSummaryJSON(started, 6, map[string]int{"10": 1, "30": 4, "1000": 1}, 30, 1.0, 1, 0.8),
	}
	plugin := Plugin{Runner: runner, Clock: func() time.Time { return now }}
	drain := 7
	step := finiteStep(t, 2, 1, &drain)
	if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	raw, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	session := value.LoadSession.Spec
	// startedAt/completedAt are the profile's t=0 and t=end, not when kubectl returned.
	if session.StartedAt == nil || !session.StartedAt.Equal(started) || session.CompletedAt == nil || !session.CompletedAt.Equal(started.Add(time.Second)) {
		t.Fatalf("measured window %v - %v, want %v + 1s", session.StartedAt, session.CompletedAt, started)
	}
	if session.WarmupStartedAt == nil || !session.WarmupStartedAt.Equal(warmupStart) || session.WarmupSeconds == nil || *session.WarmupSeconds != 1 || session.DrainSeconds == nil || *session.DrainSeconds != 7 || session.Clock != "shared-start" {
		t.Fatalf("warmup/drain/clock fields: %s", raw)
	}
	if session.Workers != 2 {
		t.Fatalf("workers %d, want the 2 Locust processes that ran the profile", session.Workers)
	}
	if len(*drained) != 1 || (*drained)[0] != 7*time.Second {
		t.Fatalf("drain waits %v", *drained)
	}
	armed, summaryAt, scaledDown := 0, -1, -1
	for index, command := range runner.commands {
		if strings.Contains(command, "-c locust -- /bin/sh -ec") {
			armed++
			if !strings.Contains(command, "test $(("+strconv.FormatInt(warmupStart.Unix(), 10)+" - now)) -lt 1") || !strings.Contains(command, "printf '%s' "+strconv.FormatInt(started.Unix(), 10)+" > /tmp/kubephos-start") {
				t.Fatalf("worker received a different start: %s", command)
			}
		}
		if strings.Contains(command, "-c locust -- python3 -c") {
			summaryAt = index
		}
		if strings.HasSuffix(command, "--replicas 0") {
			scaledDown = index
		}
	}
	if armed != 2 || drainedAt < 0 || summaryAt < drainedAt || scaledDown < summaryAt {
		t.Fatalf("armed %d workers; drain at %d, summary at %d, scale-down at %d", armed, drainedAt, summaryAt, scaledDown)
	}
	summary := session.LocustSummary
	if summary == nil || summary.Workers != 2 || !summary.MeasuredFrom.Equal(started) || !summary.MeasuredTo.Equal(*session.CompletedAt) || summary.Requests != 10 || summary.Failures != 2 || len(summary.Endpoints) != 1 || summary.Endpoints[0].P50Ms != 30 {
		t.Fatalf("merged summary %+v", summary)
	}
	if summary.Schedule.LateRequests != 3 || summary.Schedule.MaxLagSeconds != 0.8 || summary.Schedule.MeanLagSeconds != 0.05 || len(summary.WorkerCPU) != 2 || summary.WorkerCPU[0].Worker != fakePodName(0) || summary.Errors[0].Occurrences != 2 {
		t.Fatalf("merged schedule/cpu/errors %+v", summary)
	}
	if health, err := plugin.Verify(context.Background(), step, raw, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify rejected the shared-start artifact %#v %v", health, err)
	}
	// The window is exact: a shifted completion no longer verifies.
	shifted := *session.CompletedAt
	shifted = shifted.Add(time.Second)
	value.LoadSession.Spec.CompletedAt = &shifted
	tampered, _ := json.Marshal(value)
	if health, _ := plugin.Verify(context.Background(), step, tampered, discardLog); health.Status == domain.HealthHealthy {
		t.Fatal("verify accepted a window longer than the profile")
	}
}

func TestSharedStartWithoutWarmupAndSummaryFailureStillSucceeds(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	drained := fastSharedStart(t, nil)
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	plugin := Plugin{Runner: runner, Clock: func() time.Time { return now }}
	step := finiteStep(t, 1, 0, nil)
	warnings := []string{}
	log := func(level, message string) error {
		if level == "warning" {
			warnings = append(warnings, message)
		}
		return nil
	}
	raw, err := plugin.Execute(context.Background(), step, log)
	if err != nil {
		t.Fatalf("summary failure must not fail the session: %v", err)
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	session := value.LoadSession.Spec
	if session.LocustSummary != nil || strings.Contains(string(raw), "locustSummary") || len(warnings) != 1 || !strings.Contains(warnings[0], "Locust statistics summary is unavailable") {
		t.Fatalf("summary should be omitted with a warning: %s %v", raw, warnings)
	}
	if session.WarmupStartedAt != nil || session.WarmupSeconds == nil || *session.WarmupSeconds != 0 || !session.StartedAt.Equal(now.Add(2*time.Second)) || session.DrainSeconds == nil || *session.DrainSeconds != defaultDrainSeconds {
		t.Fatalf("defaults: %s", raw)
	}
	if len(*drained) != 1 || (*drained)[0] != 30*time.Second || runner.state.Spec.Replicas != 0 {
		t.Fatalf("default drain %v replicas %d", *drained, runner.state.Spec.Replicas)
	}
	if health, err := plugin.Verify(context.Background(), step, raw, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify %#v %v", health, err)
	}
}

func TestLegacyLoadSessionArtifactStillVerifies(t *testing.T) {
	spec, cluster, deployment, _, profile, _, err := resolve(finiteStep(t, 1, 0, nil))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	completed := started.Add(1500 * time.Millisecond)
	session := loadSession{APIVersion: artifactAPI, Kind: "LoadSession", Metadata: loadSessionMetadata{Name: deployment.Spec.Namespace + "/" + profile.ID, Version: "v1alpha1"}, Spec: loadSessionSpec{ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace, ScenarioID: profile.ID, Action: "start", State: "completed", Steps: spec.Steps, DurationSeconds: 1, StartedAt: &started, CompletedAt: &completed, Workload: profile.Workload}}
	if err := validateResult(session, spec, cluster, deployment, profile, "completed", 0); err != nil {
		t.Fatalf("legacy artifact without a clock rejected: %v", err)
	}
}

func TestWarmupAndDrainValidation(t *testing.T) {
	base := func() Spec {
		return Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: "start", Replicas: 1, Steps: []LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 10}}}
	}
	negative, tooLong, zero, maximum := -1, 601, 0, 600
	for _, tc := range []struct {
		name  string
		edit  func(*Spec)
		path  string
		valid bool
	}{
		{"defaults", func(*Spec) {}, "", true},
		{"warmup max", func(spec *Spec) { spec.WarmupSeconds = 3600 }, "", true},
		{"warmup too long", func(spec *Spec) { spec.WarmupSeconds = 3601 }, "warmupSeconds", false},
		{"warmup negative", func(spec *Spec) { spec.WarmupSeconds = -1 }, "warmupSeconds", false},
		{"drain zero", func(spec *Spec) { spec.DrainSeconds = &zero }, "", true},
		{"drain max", func(spec *Spec) { spec.DrainSeconds = &maximum }, "", true},
		{"drain negative", func(spec *Spec) { spec.DrainSeconds = &negative }, "drainSeconds", false},
		{"drain too long", func(spec *Spec) { spec.DrainSeconds = &tooLong }, "drainSeconds", false},
	} {
		spec := base()
		tc.edit(&spec)
		raw, _ := json.Marshal(spec)
		report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw})
		if report.Valid != tc.valid || (!tc.valid && report.Issues[len(report.Issues)-1].Path != tc.path) {
			t.Fatalf("%s: %#v", tc.name, report)
		}
		if _, err := validateSpecTimeline(withDefaults(spec)); (err == nil) != tc.valid {
			t.Fatalf("%s: precheck timeline validation %v", tc.name, err)
		}
	}
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{"action":"start","replicas":1}`))
	if err != nil || !strings.Contains(string(plan.Steps[0].Input), `"drainSeconds":30`) || strings.Contains(string(plan.Steps[0].Input), "warmupSeconds") {
		t.Fatalf("plan defaults: %s %v", plan.Steps[0].Input, err)
	}
	for _, schema := range []string{string((Plugin{}).Manifest().Schema)} {
		if !strings.Contains(schema, `"warmupSeconds":{"type":"integer"`) || !strings.Contains(schema, `"drainSeconds":{"type":"integer"`) {
			t.Fatal("manifest schema lacks warmupSeconds/drainSeconds")
		}
	}
}

func TestManifestSelectsStartClockAndWarmup(t *testing.T) {
	profile := loadProfile{ID: "journey", Engine: "locust", RuntimeImage: "example.test/locust:1.0.0", Script: "from locust import HttpUser\n", Workload: scenarioWorkload("journey")}
	finite, err := loadManifest(profile, "owner", Spec{Replicas: 2, MaxUsers: 25, SpawnRate: 5, WarmupSeconds: 45, Steps: []LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 20}}}, []loadTarget{{URL: "http://node-proxy:80/", Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`name: KUBEPHOS_START_CLOCK\s+value: shared\n`, `name: KUBEPHOS_WARMUP_SECONDS\s+value: "45"`} {
		if !regexp.MustCompile(expected).Match(finite) {
			t.Fatalf("finite manifest missing %q:\n%s", expected, finite)
		}
	}
	interactive, err := loadManifest(profile, "owner", Spec{Interactive: true, Replicas: 1, MaxUsers: 25, SpawnRate: 5}, []loadTarget{{URL: "http://node-proxy:80/", Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`name: KUBEPHOS_START_CLOCK\s+value: local\n`).Match(interactive) {
		t.Fatalf("interactive sessions must not wait for a shared start:\n%s", interactive)
	}
}

func TestResumeAndRestoreUseTheLocalClock(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	plugin := Plugin{Runner: runner}
	for _, step := range []domain.PlanStep{testStep(t, "start", 2), testStep(t, "stop", 1), testStep(t, "resume", 3)} {
		if _, err := plugin.Execute(context.Background(), step, discardLog); err != nil {
			t.Fatal(err)
		}
	}
	if runner.startClock != "local" || runner.workers != 3 {
		t.Fatalf("resumed pods would wait for a start nobody publishes: clock %q workers %d", runner.startClock, runner.workers)
	}
	stop := testStep(t, "stop", 1)
	if _, err := plugin.Execute(context.Background(), stop, discardLog); err != nil {
		t.Fatal(err)
	}
	runner.startClock = ""
	stop.Cleanup = true
	if err := plugin.Cleanup(context.Background(), stop, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if runner.startClock != "local" || runner.state.Spec.Replicas != 3 {
		t.Fatalf("restore clock %q replicas %d", runner.startClock, runner.state.Spec.Replicas)
	}
}

func mergeTestWorker(measuredFrom time.Time, entries string, schedule string, cpu string) workerSummary {
	var value workerSummary
	raw := fmt.Sprintf(`{"measuredFrom":%f,"entries":[%s],"total":%s,"errors":[],"cpu":%s,"schedule":%s}`, float64(measuredFrom.UnixNano())/1e9, entries, aggregatedOf(entries), cpu, schedule)
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		panic(err)
	}
	return value
}

// aggregatedOf builds the worker's Locust "Aggregated" entry from its endpoints.
func aggregatedOf(entries string) string {
	var values []workerStatsEntry
	_ = json.Unmarshal([]byte("["+entries+"]"), &values)
	total := mergedEntry{name: "Aggregated"}
	times := map[string]int64{}
	var minimum *float64
	for _, value := range values {
		_ = total.add(value)
		for key, count := range value.ResponseTimes {
			times[key] += count
		}
		if value.MinResponseTime != nil && (minimum == nil || *value.MinResponseTime < *minimum) {
			minimum = value.MinResponseTime
		}
	}
	encoded, _ := json.Marshal(workerStatsEntry{Name: "Aggregated", NumRequests: total.requests, NumNoneRequests: total.none, NumFailures: total.failures, TotalResponseTime: total.totalTime, MinResponseTime: minimum, MaxResponseTime: total.maxTime, ResponseTimes: times})
	return string(encoded)
}

func TestMergeLocustSummariesIsExact(t *testing.T) {
	from := time.Date(2030, 1, 1, 0, 0, 10, 0, time.UTC)
	to := from.Add(time.Minute)
	one := mergeTestWorker(from,
		`{"method":"GET","name":"/cart","numRequests":4,"numNoneRequests":0,"numFailures":1,"totalResponseTime":341,"minResponseTime":9.6,"maxResponseTime":301,"responseTimes":{"10":2,"20":1,"300":1}},
		 {"method":"GET","name":"/idle","numRequests":0,"numNoneRequests":0,"numFailures":0,"totalResponseTime":0,"minResponseTime":null,"maxResponseTime":0,"responseTimes":{}}`,
		`{"count":10,"lagSum":1.0,"late":2,"maxLag":0.5}`, `{"maxPercent":40,"warnings":0}`)
	two := mergeTestWorker(from,
		`{"method":"GET","name":"/cart","numRequests":6,"numNoneRequests":1,"numFailures":0,"totalResponseTime":1131,"minResponseTime":8.9,"maxResponseTime":1004,"responseTimes":{"10":1,"30":3,"1000":1}},
		 {"method":"POST","name":"/pay","numRequests":2,"numNoneRequests":0,"numFailures":2,"totalResponseTime":100,"minResponseTime":50,"maxResponseTime":50,"responseTimes":{"50":2}}`,
		`{"count":30,"lagSum":1.0,"late":1,"maxLag":0.8}`, `{"maxPercent":95.5,"warnings":3}`)
	two.Errors = []workerStatsError{{Method: "POST", Name: "/pay", Error: "HTTPError('503')", Occurrences: 2}}
	one.Errors = []workerStatsError{{Method: "POST", Name: "/pay", Error: "HTTPError('503')", Occurrences: 1}, {Method: "GET", Name: "/cart", Error: "ConnectionError", Occurrences: 1}}
	summary, err := mergeLocustSummaries([]namedWorkerSummary{{worker: "worker-b", summary: two}, {worker: "worker-a", summary: one}}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Workers != 2 || summary.Requests != 12 || summary.Failures != 3 || len(summary.Endpoints) != 2 {
		t.Fatalf("counts %+v", summary)
	}
	cart, pay := summary.Endpoints[0], summary.Endpoints[1]
	if cart.Name != "/cart" || cart.Requests != 10 || cart.Failures != 1 || cart.MinMs != 8.9 || cart.MaxMs != 1004 || cart.MeanMs != 1472.0/9 || pay.Name != "/pay" || pay.Method != "POST" || pay.P99Ms != 50 {
		t.Fatalf("endpoints %+v", summary.Endpoints)
	}
	if summary.Total.Name != "Aggregated" || summary.Total.Method != "" || summary.Total.Requests != 12 {
		t.Fatalf("total %+v", summary.Total)
	}
	if len(summary.Errors) != 2 || summary.Errors[0].Occurrences != 3 || summary.Errors[0].Name != "/pay" {
		t.Fatalf("errors %+v", summary.Errors)
	}
	if summary.WorkerCPU[0].Worker != "worker-a" || summary.WorkerCPU[1].MaxPercent != 95.5 || summary.WorkerCPU[1].Warnings != 3 || summary.Schedule.LateRequests != 3 || summary.Schedule.MaxLagSeconds != 0.8 || summary.Schedule.MeanLagSeconds != 0.05 {
		t.Fatalf("cpu/schedule %+v %+v", summary.WorkerCPU, summary.Schedule)
	}
	// Percentiles of the merged histogram must equal Locust's own computation on the
	// merged response_times dict (verbatim from locust.stats).
	encoded, _ := json.Marshal(map[string]any{"cart": cart, "total": summary.Total})
	runPython(t, `import json
def calculate_response_time_percentile(response_times, num_requests, percent):
    num_of_request = int(num_requests * percent)
    processed_count = 0
    for response_time in sorted(response_times.keys(), reverse=True):
        processed_count += response_times[response_time]
        if num_requests - processed_count <= num_of_request:
            return response_time
    return 0
go = json.loads(`+strconv.Quote(string(encoded))+`)
cart = {10: 3, 20: 1, 30: 3, 300: 1, 1000: 1}
total = dict(cart)
total[50] = 2
for key, times, requests in (("cart", cart, 10), ("total", total, 12)):
    for field, percent in (("p50Ms", 0.5), ("p90Ms", 0.9), ("p95Ms", 0.95), ("p99Ms", 0.99), ("p999Ms", 0.999)):
        assert go[key][field] == calculate_response_time_percentile(times, requests, percent), (key, field, go[key][field])
`)
	shifted := mergeTestWorker(from.Add(5*time.Second), `{"method":"GET","name":"/cart","numRequests":1,"numNoneRequests":0,"numFailures":0,"totalResponseTime":1,"minResponseTime":1,"maxResponseTime":1,"responseTimes":{"1":1}}`, `{}`, `{}`)
	if _, err := mergeLocustSummaries([]namedWorkerSummary{{worker: "a", summary: one}, {worker: "b", summary: shifted}}, from, to); err == nil {
		t.Fatal("a worker that reset its statistics at another instant was merged")
	}
	if _, err := mergeLocustSummaries(nil, from, to); err == nil {
		t.Fatal("empty summary accepted")
	}
}
