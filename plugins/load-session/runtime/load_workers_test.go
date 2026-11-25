package loadsession

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func wrapperSection(t *testing.T, from, to string) string {
	t.Helper()
	start := strings.Index(locustWrapper, from)
	if start < 0 {
		t.Fatalf("wrapper section %q missing", from)
	}
	if to == "" {
		return locustWrapper[start:]
	}
	end := strings.Index(locustWrapper[start:], to)
	if end < 0 {
		t.Fatalf("wrapper section end %q missing", to)
	}
	return locustWrapper[start : start+end]
}

// locustHarness runs the production pacing, rate and shape code against a simulated
// clock so offered load can be measured without Locust or gevent.
func locustHarness(t *testing.T) string {
	t.Helper()
	return `import math
clock = [0.0]
class time:
    @staticmethod
    def monotonic():
        return clock[0]
class gevent:
    @staticmethod
    def sleep(seconds):
        clock[0] += seconds
class NoLock:
    def __enter__(self):
        return self
    def __exit__(self, *args):
        return False
class LoadTestShape:
    def get_run_time(self):
        return clock[0]
request_lock = NoLock()
next_request_at = 0.0
max_users = 1000
spawn_rate = 10.0
warmup_seconds = 0.0
profile_tasks = []
profile_seconds_per_task = 0.0
profile_rps = 0.0
steps = []
def profile_elapsed():
    return clock[0]
` + wrapperSection(t, "def configured_load_workers(", "load_workers = configured_load_workers(") +
		"load_workers = 1\n" +
		wrapperSection(t, "def duration(items):", "def choose_weighted(") +
		wrapperSection(t, "def wait_for_request_slot(", "original_http_request =") +
		wrapperSection(t, "class KubePhosLoadShape(", "") + `
def paced_requests(seconds, at=0.0):
    global next_request_at
    clock[0] = at
    next_request_at = at
    count = 0
    while True:
        wait_for_request_slot()
        if clock[0] >= at + seconds:
            return count
        count += 1
def shape_users(elapsed):
    clock[0] = elapsed
    return KubePhosLoadShape().tick()[0]
`
}

func runPython(t *testing.T, script string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	if output, err := exec.Command(python, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("python: %v\n%s", err, output)
	}
}

func TestLocustWorkersShareConfiguredTotalLoad(t *testing.T) {
	runPython(t, locustHarness(t)+`
assert configured_load_workers("4") == 4
for invalid in (None, "", "0", "-3", "many"):
    assert configured_load_workers(invalid) == 1, invalid

steps = [{"type":"constant","durationSeconds":600,"rps":100}]
load_workers = configured_load_workers("1")
assert worker_rps_at(5) == 100
assert shape_users(5) == 100
count = paced_requests(10)
assert abs(count - 1000) <= 1, count

load_workers = configured_load_workers("4")
assert rps_at(5) == 100
assert worker_rps_at(5) == 25
assert shape_users(5) == 25
count = paced_requests(10)
# Four independent workers together offer the configured 100 RPS.
assert abs(count - 250) <= 1, count
assert abs(4 * count - 1000) <= 4, count

# A share below one request per second still keeps one paced user per worker.
steps = [{"type":"constant","durationSeconds":600,"rps":2}]
assert worker_rps_at(5) == 0.5
assert shape_users(5) == 1
count = paced_requests(20)
assert abs(count - 10) <= 1, count

# The per-worker user cap still applies to each worker's share.
steps = [{"type":"constant","durationSeconds":600,"rps":10000}]
assert shape_users(5) == 1000
load_workers = 1
assert shape_users(5) == 1000

# Time-varying profiles are divided at every instant.
steps = [{"type":"exponential","durationSeconds":100,"startRps":40,"endRps":440}]
load_workers = 4
assert abs(worker_rps_at(50) - 60) < 1e-9
assert shape_users(50) == 60
assert shape_users(101) == 0
`)
}

func TestLocustZeroValuedStepFieldsFromGoEncoding(t *testing.T) {
	steps := []LoadStep{
		{Type: "constant", DurationSeconds: 30, RPS: 0},
		{Type: "exponential", DurationSeconds: 100, StartRPS: 0, EndRPS: 100},
		{Type: "sinusoidal", DurationSeconds: 100, BaselineRPS: 0, AmplitudeRPS: 40, PeriodSeconds: 100},
		{Type: "exponential", DurationSeconds: 100, StartRPS: 0, EndRPS: 100, Curve: 3},
	}
	if _, err := validateLoadTimeline(steps); err != nil {
		t.Fatalf("zero-valued fields must remain valid: %v", err)
	}
	encoded, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	// The premise of the regression: omitempty drops these zero values from KUBEPHOS_STEPS.
	for _, dropped := range []string{`"rps"`, `"startRps"`, `"baselineRps"`, `"curve":0`} {
		if strings.Contains(string(encoded), dropped) {
			t.Fatalf("expected %s to be omitted from %s", dropped, encoded)
		}
	}
	runPython(t, "import json\n"+locustHarness(t)+"steps = json.loads("+strconv.Quote(string(encoded))+")\n"+`
# Constant 0 RPS phase: no KeyError, no traffic, no users.
assert rps_at(0) == 0
assert rps_at(29.9) == 0
assert shape_users(10) == 0
# Exponential with startRps 0 and curve omitted is linear (curve 0), like the UI and collector.
assert rps_at(30) == 0
assert abs(rps_at(80) - 50) < 1e-9
assert abs(rps_at(105) - 75) < 1e-9
# Legacy sinusoidal with baseline 0 and phase omitted.
assert abs(rps_at(130 + 25) - 40) < 1e-9
assert rps_at(130 + 75) == 0
# An explicit curve is still honoured.
expected = 100 * (math.exp(1.5) - 1) / (math.exp(3) - 1)
assert abs(rps_at(230 + 50) - expected) < 1e-9
load_workers = 4
assert abs(worker_rps_at(80) - 12.5) < 1e-9
`)
}

func TestLoadManifestPassesWorkerCount(t *testing.T) {
	profile := loadProfile{ID: "journey", Engine: "locust", RuntimeImage: "example.test/locust:1.0.0", Script: "from locust import HttpUser\n", Workload: scenarioWorkload("journey")}
	for _, tc := range []struct{ replicas, workers int }{{0, 1}, {1, 1}, {4, 4}} {
		manifest, err := loadManifest(profile, "owner", Spec{Replicas: tc.replicas, MaxUsers: 25, SpawnRate: 5, Steps: []LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 20}}}, []loadTarget{{URL: "http://node-proxy:80/", Weight: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if got := manifestWorkers(string(manifest)); got != tc.workers {
			t.Fatalf("replicas %d: %s=%d, want %d", tc.replicas, loadWorkersEnv, got, tc.workers)
		}
	}
	for _, expected := range []string{"os.environ.get(\"KUBEPHOS_LOAD_WORKERS\", \"1\")", "target_rps = worker_rps_at(elapsed)", "return worker_users_at(elapsed), spawn_rate"} {
		if !strings.Contains(locustWrapper, expected) {
			t.Fatalf("wrapper missing %q", expected)
		}
	}
}

var manifestWorkersPattern = regexp.MustCompile(`name: KUBEPHOS_LOAD_WORKERS\s+value: "(\d+)"`)

func manifestWorkers(manifest string) int {
	match := manifestWorkersPattern.FindStringSubmatch(manifest)
	if match == nil {
		return -1
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func TestLoadSessionWorkerCountFollowsReplicas(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	plugin := Plugin{Runner: runner}
	steps := []struct {
		action   string
		replicas int
		workers  int
	}{{"start", 2, 2}, {"stop", 1, 2}, {"resume", 3, 3}}
	for _, tc := range steps {
		step := testStep(t, tc.action, tc.replicas)
		if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("%s precheck failed %#v %v", tc.action, health, err)
		}
		if _, err := plugin.Execute(context.Background(), step, discardLog); err != nil {
			t.Fatal(err)
		}
		if runner.workers != tc.workers {
			t.Fatalf("%s: workers %d, want %d", tc.action, runner.workers, tc.workers)
		}
	}

	// Compensating a stop restores both the replicas and the matching worker share.
	stop := testStep(t, "stop", 1)
	if _, err := plugin.Execute(context.Background(), stop, discardLog); err != nil {
		t.Fatal(err)
	}
	runner.workers = 0
	stop.Cleanup = true
	if err := plugin.Cleanup(context.Background(), stop, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if runner.state.Spec.Replicas != 3 || runner.workers != 3 {
		t.Fatalf("stop cleanup restored replicas %d workers %d", runner.state.Spec.Replicas, runner.workers)
	}
}

// Users follow the observed latency (Little's law) so the generator keeps an
// open-loop schedule when the system under test slows down.
func TestLocustUsersFollowObservedLatency(t *testing.T) {
	runPython(t, locustHarness(t)+`
steps = [{"type":"constant","durationSeconds":600,"rps":10}]
# Before any response: ceil(rate) users.
assert shape_users(5) == 10, shape_users(5)
# Fast responses (below 1/headroom s) keep the floor.
for _ in range(50):
    observe_latency(response_time=200.0)
assert shape_users(7) == 10, shape_users(7)
# Two-second responses need 10 x 2 x 1.5 = 30 users, from the next update on.
for _ in range(50):
    observe_latency(response_time=2000.0)
assert shape_users(9) == 30, shape_users(9)
# Updates happen at most once per second.
for _ in range(50):
    observe_latency(response_time=8000.0)
assert shape_users(9.5) == 30, shape_users(9.5)
assert shape_users(11) == 120, shape_users(11)
# Recovery decays slowly instead of dropping the users at once.
for _ in range(50):
    observe_latency(response_time=100.0)
users = shape_users(13)
assert 30 < users < 120, users
# Failures without a duration and invalid values are ignored.
observe_latency(response_time=None)
observe_latency(response_time=float("nan"))
observe_latency(response_time=-1.0)
# The per-worker cap still applies.
max_users = 50
assert shape_users(15) == 50, shape_users(15)
# The share of each worker is sized, not the total.
max_users = 1000
latency_window["estimate"] = 2.0
load_workers = 4
steps = [{"type":"constant","durationSeconds":600,"rps":100}]
latency_window["updated_at"] = clock[0] = 20.0
assert worker_users_at(20.5) == 75, worker_users_at(20.5)
`)
}
