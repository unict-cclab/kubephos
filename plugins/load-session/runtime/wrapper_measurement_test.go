package loadsession

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// measurementHarness runs the production clock, phase, pacing, measurement and
// metrics code of the wrapper with a simulated monotonic and wall clock.
func measurementHarness(t *testing.T) string {
	t.Helper()
	return `import json, math, os, re, tempfile
clock = [0.0]
wall = [0.0]
class time:
    @staticmethod
    def monotonic():
        return clock[0]
    @staticmethod
    def time():
        return wall[0]
class gevent:
    @staticmethod
    def sleep(seconds):
        clock[0] += seconds
        wall[0] += seconds
class NoLock:
    def __enter__(self):
        return self
    def __exit__(self, *args):
        return False
class LoadTestShape:
    pass
class events:
    pass
request_lock = NoLock()
next_request_at = None
max_users = 1000
spawn_rate = 10.0
steps = []
started_at = 0.0
load_workers = 1
warmup_seconds = 0.0
start_clock = "shared"
start_file = os.path.join(tempfile.mkdtemp(), "start")
shared_start = None
` + taskMixFakes + wrapperSection(t, "# Request mix", "profile_call_chain = ") + `
profile_tasks = []
profile_seconds_per_task = 0.0
profile_rps = 0.0
task_plan_file = os.path.join(tempfile.mkdtemp(), "plan.json")
` + wrapperSection(t, "def configured_load_workers(", "load_workers = configured_load_workers(") +
		wrapperSection(t, "def profile_elapsed(", "request_lock = Semaphore()") +
		wrapperSection(t, "def duration(items):", "def choose_weighted(") +
		wrapperSection(t, "def wait_for_request_slot(", "original_http_request =") +
		wrapperSection(t, "# Measurement window", "@events.init.add_listener") +
		wrapperSection(t, "buckets = (", "def serve_stats(") +
		wrapperSection(t, "class KubePhosLoadShape(", "") + `
class Entry:
    def __init__(self, method, name, times, failures=0, minimum=None):
        self.method, self.name, self.response_times = method, name, dict(times)
        self.num_requests = sum(times.values())
        self.num_none_requests = 0
        self.num_failures = failures
        self.total_response_time = sum(key * count for key, count in times.items())
        self.min_response_time = minimum
        self.max_response_time = max(times) if times else 0
class StatsError:
    def __init__(self, method, name, error, occurrences):
        self.method, self.name, self.error, self.occurrences = method, name, error, occurrences
    @classmethod
    def parse_error(cls, error):
        return "parsed " + repr(error)
class Stats:
    def __init__(self):
        self.resets = 0
        self.reset_all()
    def reset_all(self):
        self.resets += 1
        self.entries = {}
        self.total = Entry(None, "Aggregated", {})
        self.errors = {}
class Runner:
    def __init__(self):
        self.stats = Stats()
        self.user_count = 3
        self.current_cpu_usage = 12.0
        self.cpu_warning_emitted = False
class Environment:
    def __init__(self):
        self.runner = Runner()
def publish_start(value):
    with open(start_file, "w") as target:
        target.write(value)
`
}

func outputPython(t *testing.T, script string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	output, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, output)
	}
	return string(output)
}

func TestLocustSharedStartIdlesThenWarmsUpAndRunsTheProfile(t *testing.T) {
	runPython(t, measurementHarness(t)+`
warmup_seconds = configured_seconds("5")
assert configured_seconds("x") == 0 and configured_seconds("-3") == 0 and configured_seconds("nan") == 0
load_workers = 2
steps = [{"type":"exponential","durationSeconds":100,"startRps":40,"endRps":440}]
shape = KubePhosLoadShape()
# Ready workers idle with zero users until KubePhos publishes the shared start.
wall[0] = 990.0
assert profile_elapsed() is None
assert shape.tick() == (0, spawn_rate)
assert load_phase(profile_elapsed()) is None
assert worker_rps_at(None) == 0
publish_start("1000")
assert profile_elapsed() == -10.0
assert shape.tick()[0] == 0
assert load_phase(-10.0) is None
# Warmup: the profile's initial rate, split across the workers.
wall[0] = 996.0
assert load_phase(profile_elapsed()) == 0
assert worker_rps_at(profile_elapsed()) == 20.0
assert shape.tick()[0] == 20
# Measured profile from the shared t=0.
wall[0] = 1000.0
assert load_phase(profile_elapsed()) == 1
wall[0] = 1050.0
assert worker_rps_at(profile_elapsed()) == 120.0
assert shape.tick()[0] == 120
# Drain: no users and no target after the profile.
wall[0] = 1100.5
assert load_phase(profile_elapsed()) == 2
assert worker_rps_at(profile_elapsed()) == 0
assert shape.tick() == (0, spawn_rate)
# The published instant is read once.
os.remove(start_file)
wall[0] = 1050.0
assert profile_elapsed() == 50.0
# The pacer offers the warmup rate during the warmup.
wall[0] = 995.0
clock[0] = 0.0
next_request_at = None
count = 0
while True:
    wait_for_request_slot()
    if wall[0] >= 999.0:
        break
    count += 1
assert abs(count - 80) <= 1, count
# Resumed pods use their own process clock (no shared start is published to them).
start_clock = "local"
clock[0] = started_at + 2.0
assert profile_elapsed() == -3.0
assert shape.tick()[0] == 20
`)
}

func TestLocustMeasurementResetsAtProfileStartAndSummarizes(t *testing.T) {
	runPython(t, measurementHarness(t)+`
warmup_seconds = 5.0
steps = [{"type":"constant","durationSeconds":10,"rps":10}]
environment = Environment()
measurement["environment"] = environment
stats = environment.runner.stats
publish_start("1000")
wall[0] = 996.0
assert update_phase(profile_elapsed()) == 0
assert stats.resets == 1 and measurement["measured_from"] is None
record_schedule_lag(0.4, 10.0)
stats.entries[("/cart", "GET")] = Entry("GET", "/cart", {10: 5})
# t=0: statistics and the measured schedule restart; Prometheus counters keep counting.
wall[0] = 1000.0
assert update_phase(profile_elapsed()) == 1
assert stats.resets == 2 and stats.entries == {} and measurement["measured_from"] == 1000.0
assert measured_schedule["count"] == 0 and schedule_stats["count"] == 1
wall[0] = 1005.0
assert update_phase(profile_elapsed()) == 1 and stats.resets == 2
record_schedule_lag(0.0, 10.0)
record_schedule_lag(0.2, 10.0)
record_schedule_lag(-0.1, 10.0)
cpu_warning_event = object()
record_cpu_warning(cpu_usage=97.0)
stats.entries[("/cart", "GET")] = Entry("GET", "/cart", {10: 3, 120: 1}, failures=1, minimum=9.7)
stats.total = Entry(None, "Aggregated", {10: 3, 120: 1}, failures=1, minimum=9.7)
stats.errors["x"] = StatsError("GET", "/cart", ValueError("boom"), 1)
wall[0] = 1010.5
assert update_phase(profile_elapsed()) == 2
record_cpu_warning(cpu_usage=99.0)
summary = json.loads(json.dumps(locust_summary()))
assert set(summary) == {"phase", "measuredFrom", "entries", "total", "errors", "cpu", "schedule", "taskMix", "taskExecutions"}, summary
assert summary["phase"] == 2 and summary["measuredFrom"] == 1000.0
assert summary["schedule"] == {"count": 3, "lagSum": 0.2, "late": 1, "maxLag": 0.2}, summary["schedule"]
assert summary["cpu"] == {"maxPercent": 97.0, "warnings": 1}, summary["cpu"]
entry = summary["entries"][0]
assert entry == {"method": "GET", "name": "/cart", "numRequests": 4, "numNoneRequests": 0, "numFailures": 1, "totalResponseTime": 150.0, "minResponseTime": 9.7, "maxResponseTime": 120.0, "responseTimes": {"10": 3, "120": 1}}, entry
assert summary["total"]["method"] == "" and summary["total"]["name"] == "Aggregated"
assert summary["errors"] == [{"method": "GET", "name": "/cart", "error": "parsed ValueError('boom')", "occurrences": 1}], summary["errors"]
assert schedule_stats["count"] == 4 and schedule_stats["late"] == 2
# Without Locust's cpu_warning event the one-shot warning flag is counted once.
cpu_warning_event = None
measurement["measured_from"] = None
wall[0] = 1001.0
update_phase(profile_elapsed())
environment.runner.cpu_warning_emitted = True
environment.runner.current_cpu_usage = 93.0
update_phase(profile_elapsed())
update_phase(profile_elapsed())
assert measurement["cpu_warnings"] == 1 and measurement["cpu_max"] == 93.0, measurement
`)
}

func TestLocustPrometheusExpositionLabelsCapEscapingAndNewSeries(t *testing.T) {
	runPython(t, measurementHarness(t)+`
steps = [{"type":"constant","durationSeconds":60,"rps":30}]
load_workers = 3
environment = Environment()
measurement["environment"] = environment
seed_metrics(None)
weird = 'we"ird\\path\nx'
record_request("GET", weird, 3.0, ValueError("boom"))
record_request("redis", "SET", 1.0, None)
for index in range(60):
    record_request("GET", "/item/%d" % index, 12.0, None)
record_request("GET", "/item/0", float("nan"), None)
record_schedule_lag(0.3, 10.0)

def parse(text):
    assert text.endswith("\n")
    types, family, samples = {}, None, []
    sample = re.compile(r'^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})? (\S+)$')
    label = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\\n]|\\[\\"n])*)"(?:,|$)')
    for line in text.split("\n")[:-1]:
        if line.startswith("# TYPE "):
            _, _, name, kind = line.split(" ")
            assert name not in types, "duplicate family " + name
            types[name], family = kind, name
            continue
        match = sample.match(line)
        assert match, line
        name, raw, value = match.groups()
        float(value)
        suffixes = ("_bucket", "_count", "_sum") if types[family] == "histogram" else ("_count", "_sum") if types[family] == "summary" else ()
        assert name == family or any(name == family + suffix for suffix in suffixes), ("family not contiguous", line)
        labels, position = {}, 0
        while raw and position < len(raw):
            found = label.match(raw, position)
            assert found, raw[position:]
            labels[found.group(1)] = re.sub(r'\\(.)', lambda m: "\n" if m.group(1) == "n" else m.group(1), found.group(2))
            position = found.end()
        samples.append((name, labels, float(value)))
    return types, samples

wall[0] = 990.0
types, samples = parse(prometheus_exposition())
assert not [s for s in samples if s[0] == "kubephos_load_phase"], "idle workers expose no phase"
assert [s[2] for s in samples if s[0] == "kubephos_load_target_rps"] == [0.0]
requests = [s for s in samples if s[0] == "kubephos_load_requests_total"]
names = {s[1]["name"] for s in requests}
assert weird in names and "SET" in names and "other" in names and "" in names
assert len(names - {"", "other"}) == max_metric_names, len(names)
other = sum(s[2] for s in requests if s[1]["name"] == "other" and s[1]["outcome"] == "success")
assert other == 12, other
assert sum(s[2] for s in requests if s[1]["outcome"] == "success") == 62
assert sum(s[2] for s in requests if s[1]["outcome"] == "failure") == 1
assert {s[1]["operation"] for s in requests if s[1]["name"] == "SET"} == {"SET"}
assert all(set(s[1]) == {"operation", "name", "outcome"} for s in requests)
infinite = sum(s[2] for s in samples if s[0] == "kubephos_load_duration_milliseconds_bucket" and s[1]["le"] == "+Inf")
count = sum(s[2] for s in samples if s[0] == "kubephos_load_duration_milliseconds_count")
assert infinite == count == 62, (infinite, count)
assert types["kubephos_load_schedule_lag_seconds"] == "summary" and types["kubephos_load_late_requests_total"] == "counter"
assert types["kubephos_load_target_rps"] == "gauge" and types["kubephos_load_users"] == "gauge" and types["kubephos_load_phase"] == "gauge"
values = {s[0]: s[2] for s in samples if not s[1]}
assert values["kubephos_load_users"] == 3
assert values["kubephos_load_schedule_lag_seconds_sum"] == 0.3 and values["kubephos_load_schedule_lag_seconds_count"] == 1 and values["kubephos_load_late_requests_total"] == 1
publish_start("1000")
for at, phase, target in ((1000.0, 1, 10.0), (1061.0, 2, 0.0)):
    wall[0] = at
    values = {s[0]: s[2] for s in parse(prometheus_exposition())[1] if not s[1]}
    assert values["kubephos_load_phase"] == phase and values["kubephos_load_target_rps"] == target, values
warmup_seconds = 5.0
wall[0] = 997.0
values = {s[0]: s[2] for s in parse(prometheus_exposition())[1] if not s[1]}
assert values["kubephos_load_phase"] == 0 and values["kubephos_load_target_rps"] == 10.0, values
`)
}

func TestWrapperSummaryJSONMergesInGo(t *testing.T) {
	output := outputPython(t, measurementHarness(t)+`
import sys
measurement["environment"] = Environment()
steps = [{"type":"constant","durationSeconds":10,"rps":10}]
publish_start("1000")
wall[0] = 1000.0
update_phase(profile_elapsed())
stats = measurement["environment"].runner.stats
stats.entries[("/cart", "GET")] = Entry("GET", "/cart", {10: 3, 120: 1}, failures=1, minimum=9.7)
stats.total = Entry(None, "Aggregated", {10: 3, 120: 1}, failures=1, minimum=9.7)
record_schedule_lag(0.2, 10.0)
sys.stdout.write(json.dumps(locust_summary()))
`)
	var worker workerSummary
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &worker); err != nil {
		t.Fatalf("wrapper summary does not decode: %v\n%s", err, output)
	}
	from := time.Unix(1000, 0).UTC()
	summary, err := mergeLocustSummaries([]namedWorkerSummary{{worker: "a", summary: worker}, {worker: "b", summary: worker}}, from, from.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 8 || summary.Failures != 2 || summary.Endpoints[0].P50Ms != 10 || summary.Endpoints[0].MaxMs != 120 || summary.Endpoints[0].MinMs != 9.7 || summary.Schedule.LateRequests != 2 {
		t.Fatalf("merged wrapper summary %+v", summary)
	}
}

// Users reserve pacing slots up to a short horizon ahead, so near t=end some hold
// slots that fall in the drain. Those requests must not be sent: the measured
// window ends at completedAt and the drain carries no traffic.
func TestPacerSendsNothingAfterTheProfileEnds(t *testing.T) {
	runPython(t, measurementHarness(t)+`
class Done(Exception):
    pass
advance = gevent.sleep
def bounded_sleep(seconds):
    advance(seconds)
    if wall[0] > 1015.0:
        raise Done()
gevent.sleep = bounded_sleep
steps = [{"type":"constant","durationSeconds":10,"rps":10}]
publish_start("1000")
# A queue of busy users already reserved the slots up to 0.5 s ahead.
wall[0] = 1009.8
clock[0] = 0.0
next_request_at = clock[0] + 0.5
issued = []
try:
    while True:
        wait_for_request_slot()
        issued.append(profile_elapsed())
except Done:
    pass
assert issued == [], issued
# Slots inside the profile are still honoured up to its last instant.
wall[0] = 1009.0
clock[0] = 0.0
next_request_at = None
issued = []
try:
    while True:
        wait_for_request_slot()
        issued.append(profile_elapsed())
except Done:
    pass
assert issued and max(issued) <= 10.0 and len(issued) >= 10, issued
`)
}
