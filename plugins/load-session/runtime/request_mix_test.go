package loadsession

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
)

// taskMixFakes reproduces how Locust turns tasks (dict, list, @task) into a list
// of repeated callables (locust.user.task.get_tasks_from_base_classes), plus the
// TaskSet interrupt and reschedule exceptions.
const taskMixFakes = `
import fractions, functools, io, contextlib, random
class RescheduleTaskImmediately(Exception):
    pass
class InterruptTaskSet(Exception):
    def __init__(self, reschedule=True):
        self.reschedule = reschedule
def task(weight=1):
    def decorate(function):
        function.locust_task_weight = weight
        return function
    if callable(weight):
        function, weight = weight, 1
        return decorate(function)
    return decorate
def locust_tasks(bases, class_dict):
    new_tasks = []
    for base in bases:
        if hasattr(base, "tasks") and base.tasks:
            new_tasks += base.tasks
    if "tasks" in class_dict and class_dict["tasks"] is not None:
        tasks = class_dict["tasks"]
        if isinstance(tasks, dict):
            tasks = tasks.items()
        for item in tasks:
            if isinstance(item, tuple):
                item, count = item
                new_tasks += [item] * count
            else:
                new_tasks.append(item)
    for item in class_dict.values():
        if "locust_task_weight" in dir(item):
            new_tasks += [item] * item.locust_task_weight
    return new_tasks
class TaskMeta(type):
    def __new__(mcs, name, bases, class_dict):
        class_dict["tasks"] = locust_tasks(bases, class_dict)
        return type.__new__(mcs, name, bases, class_dict)
class TaskSet(metaclass=TaskMeta):
    def __init__(self, parent):
        self.parent = parent
        self.client = parent.client
    def interrupt(self, reschedule=True):
        raise InterruptTaskSet(reschedule)
class User(metaclass=TaskMeta):
    abstract = False
    weight = 1
    def __init__(self, client):
        self.client = client
if not hasattr(gevent, "getcurrent"):
    gevent.getcurrent = staticmethod(lambda: "greenlet")
`

// taskHarness runs the production request-mix code with a fake Locust and a
// simulated clock; simulate() mimics Locust's TaskSet scheduling for one user.
func taskHarness(t *testing.T) string {
	t.Helper()
	return `import json, math, os, tempfile
clock = [0.0]
sleeps = []
class time:
    @staticmethod
    def monotonic():
        return clock[0]
class gevent:
    @staticmethod
    def sleep(seconds):
        sleeps.append(seconds)
warmup_seconds = 0.0
profile_tasks = []
profile_seconds_per_task = 0.0
profile_rps = 0.0
load_workers = 1
max_users = 1000
steps = [{"type": "constant", "durationSeconds": 100, "rps": 40}]
def profile_elapsed():
    return clock[0]
` + taskMixFakes + wrapperSection(t, "# Request mix", "profile_call_chain = ") +
		wrapperSection(t, "def duration(items):", "def choose_weighted(") + `
task_plan_file = os.path.join(tempfile.mkdtemp(), "plan.json")
class Client:
    def __init__(self, latency=0.0):
        self.requests, self.latency = [], latency
    def request(self, method, path):
        check_task_mode()
        self.requests.append((current_tasks.get(id(gevent.getcurrent())), method, path))
        clock[0] += self.latency
    def get(self, path, **kwargs):
        self.request("GET", path)
    def post(self, path, **kwargs):
        self.request("POST", path)
class Simulation:
    # Locust scheduling: the user's DefaultTaskSet picks from User.tasks, a nested
    # TaskSet keeps picking from its own tasks until it is interrupted.
    def __init__(self, user_class, client, seed=1):
        self.user = user_class(client)
        self.stack, self.random, self.executed = [], random.Random(seed), []
    def step(self):
        holder = self.stack[-1] if self.stack else None
        tasks = type(holder).tasks if holder is not None else type(self.user).tasks
        chosen = self.random.choice(tasks)
        if is_task_set(chosen):
            self.stack.append(chosen(holder if holder is not None else self.user))
            return
        if chosen is not kubephos_idle:
            self.executed.append(task_name(chosen))
        try:
            chosen(holder if holder is not None else self.user)
        except InterruptTaskSet:
            self.stack.pop()
        except RescheduleTaskImmediately:
            self.executed.append("aborted")
    def run(self, count):
        for _ in range(count):
            self.step()
def online_boutique():
    # Upstream microservices-demo loadgenerator v0.10.5 shape.
    def index(l):
        l.client.get("/")
    def setCurrency(l):
        l.client.post("/setCurrency")
    def browseProduct(l):
        l.client.get("/product/OLJCESPC7Z")
    def viewCart(l):
        l.client.get("/cart")
    def addToCart(l):
        l.client.get("/product/OLJCESPC7Z")
        l.client.post("/cart")
    def empty_cart(l):
        l.client.post("/cart/empty")
    def checkout(l):
        addToCart(l)
        l.client.post("/cart/checkout")
    def logout(l):
        l.client.get("/logout")
    class UserBehavior(TaskSet):
        def on_start(self):
            index(self)
        tasks = {index: 1, setCurrency: 2, browseProduct: 10, addToCart: 2, viewCart: 3, checkout: 1}
    class WebsiteUser(User):
        tasks = [UserBehavior]
    return WebsiteUser, UserBehavior
def reset_mix():
    task_classes.clear()
    del task_class_order[:]
    task_executions.clear()
    current_tasks.clear()
    task_mode["target"] = None
def configure(roots, overrides):
    reset_mix()
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        plan = configure_task_mix(roots, overrides)
    return plan, output.getvalue()
def counts(cls):
    result = {}
    for item in cls.tasks:
        result[task_name(item)] = result.get(task_name(item), 0) + 1
    return result
`
}

func TestRequestMixRebuildsLocustWeights(t *testing.T) {
	runPython(t, taskHarness(t)+`
# Online Boutique: dict weights in a nested TaskSet, reached through a list.
WebsiteUser, UserBehavior = online_boutique()
plan, log = configure([WebsiteUser], {})
assert log == "", log
assert [e["task"] for e in plan["taskMix"]] == ["index", "setCurrency", "browseProduct", "addToCart", "viewCart", "checkout"], plan
shares = {e["task"]: e["share"] for e in plan["taskMix"]}
assert abs(shares["browseProduct"] - 10 / 19.0) < 1e-12 and abs(sum(shares.values()) - 1) < 1e-12
assert {e["task"]: e["weight"] for e in plan["taskMix"]}["viewCart"] == 3
assert WebsiteUser.tasks == [UserBehavior] and len(UserBehavior.tasks) == 19
assert counts(UserBehavior) == {"index": 1, "setCurrency": 2, "browseProduct": 10, "addToCart": 2, "viewCart": 3, "checkout": 1}
assert plan["profileTasks"] == [e["task"] for e in plan["taskMix"]]
assert set(task_executions) == {"index", "setCurrency", "browseProduct", "addToCart", "viewCart", "checkout"}

# Overrides: zero, fractional percentages, unknown names (logged, ignored).
WebsiteUser, UserBehavior = online_boutique()
plan, log = configure([WebsiteUser], {"browseProduct": 0, "index": 33.3, "setCurrency": 66.7, "addToCart": 0, "viewCart": 0, "checkout": 0, "nosuchtask": 5})
assert "nosuchtask" in log, log
assert counts(UserBehavior) == {"index": 333, "setCurrency": 667}, counts(UserBehavior)
shares = {e["task"]: e["share"] for e in plan["taskMix"]}
assert set(shares) == {"index", "setCurrency"} and abs(shares["index"] - 0.333) < 1e-12, shares
assert plan["profileTasks"] == ["index", "setCurrency"]
# A TaskSet entry is weighted by its class name.
WebsiteUser, UserBehavior = online_boutique()
plan, _ = configure([WebsiteUser], {"UserBehavior": 4})
assert WebsiteUser.tasks == [UserBehavior] * 4

# Fractions keep exact ratios; awkward ones are rounded within 10000 entries.
assert integer_weights([0.5, 0.25]) == [2, 1]
assert integer_weights([33.3, 66.7, 0]) == [333, 667, 0]
assert integer_weights([3, 1]) == [3, 1]
values = integer_weights([math.pi, math.e, 1e-6])
assert sum(values) <= max_task_entries and values[2] >= 1 and abs(values[0] / values[1] - math.pi / math.e) < 1e-3, values
assert integer_weights([0, 0]) == [0, 0]

# Train Ticket style: @task(n) methods on the User, helper methods untouched.
class TicketUser(User):
    @task(3)
    def high_speed_search(self):
        self.search("/api/v1/travelservice/trips/left")
    @task(2)
    def regular_search(self):
        self.search("/api/v1/travel2service/trips/left")
    def search(self, path):
        self.client.post(path)
plan, _ = configure([TicketUser], {"regular_search": 6})
assert counts(TicketUser) == {"high_speed_search": 3, "regular_search": 6}
assert abs({e["task"]: e["share"] for e in plan["taskMix"]}["regular_search"] - 2 / 3.0) < 1e-12
client = Client()
simulation = Simulation(TicketUser, client)
simulation.run(300)
assert task_executions["regular_search"] + task_executions["high_speed_search"] == 300
assert task_executions["regular_search"] == simulation.executed.count("regular_search")
assert all(name == ("regular_search" if "travel2" in path else "high_speed_search") for name, _, path in client.requests)

# Bookinfo style: a single @task; zeroing it leaves no task, which stops the worker.
class BookUser(User):
    @task
    def browse_product(self):
        self.client.get("/productpage")
plan, _ = configure([BookUser], {})
assert counts(BookUser) == {"browse_product": 1}
try:
    configure([BookUser], {"browse_product": 0})
    raise AssertionError("an empty mix must fail")
except RuntimeError as error:
    assert "without tasks" in str(error)

# List style and several User classes (by their weight).
def a(l):
    l.client.get("/a")
def b(l):
    l.client.get("/b")
def c(l):
    l.client.get("/c")
class ListUser(User):
    weight = 3
    tasks = [a, a, b]
class OtherUser(User):
    tasks = {c: 1}
plan, _ = configure([ListUser, OtherUser], {"b": 2})
assert counts(ListUser) == {"a": 2, "b": 2}
shares = {e["task"]: e["share"] for e in plan["taskMix"]}
assert abs(shares["a"] - 0.375) < 1e-12 and abs(shares["b"] - 0.375) < 1e-12 and abs(shares["c"] - 0.25) < 1e-12, shares

# Counters through the nested TaskSet: only real task functions are counted.
WebsiteUser, UserBehavior = online_boutique()
configure([WebsiteUser], {"index": 1, "browseProduct": 0, "setCurrency": 0, "addToCart": 0, "viewCart": 0, "checkout": 0})
client = Client()
simulation = Simulation(WebsiteUser, client)
simulation.run(200)
assert task_executions["index"] == 199 and task_executions["browseProduct"] == 0, task_executions
assert {path for _, _, path in client.requests} == {"/"}
`)
}

func TestCallChainProfilingIsolatesEachTaskInOrder(t *testing.T) {
	runPython(t, taskHarness(t)+`
WebsiteUser, UserBehavior = online_boutique()
def other(l):
    l.client.get("/other")
class OtherUser(User):
    tasks = [other]
plan, _ = configure([WebsiteUser, OtherUser], {"viewCart": 0})
profile_tasks = list(plan["profileTasks"])
assert profile_tasks == ["index", "setCurrency", "browseProduct", "addToCart", "checkout", "other"], profile_tasks
warmup_seconds, profile_seconds_per_task, profile_rps, load_workers = 5.0, 10.0, 8.0, 2
start = -warmup_seconds - 60.0
assert load_phase(start - 0.1) is None and profiling_task_at(start - 0.1) is None
assert load_phase(start) == 3 and worker_rps_at(start) == 4.0
assert load_phase(-5.0) == 0 and worker_rps_at(-5.0) == 20.0 and profiling_task_at(-5.0) is None
paths = {"index": {"/"}, "setCurrency": {"/setCurrency"}, "browseProduct": {"/product/OLJCESPC7Z"}, "addToCart": {"/product/OLJCESPC7Z", "/cart"}, "checkout": {"/product/OLJCESPC7Z", "/cart", "/cart/checkout"}, "other": {"/other"}}
users = [Simulation(WebsiteUser, Client(), seed=3), Simulation(OtherUser, Client(), seed=4)]
for index, name in enumerate(profile_tasks):
    clock[0] = start + index * profile_seconds_per_task + 0.5
    assert profiling_task_at(clock[0]) == name and sync_task_mode(clock[0]) == name
    before = dict(task_executions)
    for user in users:
        del user.executed[:]
        del user.user.client.requests[:]
        user.run(80)
    executed = {task for user in users for task in user.executed}
    assert executed == {name}, (name, executed)
    requested = {path for user in users for _, _, path in user.user.client.requests}
    assert requested == paths[name], (name, requested)
    assert all(task == name for user in users for task, _, _ in user.user.client.requests)
    grown = {task for task in task_executions if task_executions[task] != before.get(task, 0)}
    assert grown == {name}, grown
    if name != "other":
        assert counts(UserBehavior) == {name: 1} and OtherUser.tasks == [kubephos_idle]
# The warmup restores the configured mix.
clock[0] = -warmup_seconds
sync_task_mode(clock[0])
assert counts(UserBehavior) == {"index": 1, "setCurrency": 2, "browseProduct": 10, "addToCart": 2, "checkout": 1}
assert OtherUser.tasks[0].kubephos_task == "other" and WebsiteUser.tasks == [UserBehavior]

# A task still running when its window ends stops before sending into the next one.
clock[0] = start + 4 * profile_seconds_per_task - 0.05
sync_task_mode(clock[0])
assert task_mode["target"] == "addToCart"
client = Client(latency=0.1)
simulation = Simulation(WebsiteUser, client, seed=5)
simulation.step()
simulation.step()
assert simulation.executed == ["addToCart", "aborted"], simulation.executed
assert [path for _, _, path in client.requests] == ["/product/OLJCESPC7Z"], client.requests
assert task_mode["target"] == "checkout"
simulation.step()
assert simulation.executed[-1] == "checkout"

# The plan file tells KubePhos the tasks to profile before the start is published.
write_task_plan(task_plan_file)
with open(task_plan_file) as source:
    written = json.load(source)
assert written["profileTasks"] == profile_tasks and [e["task"] for e in written["taskMix"]] == [e["task"] for e in plan["taskMix"]]
`)
}

func TestProfilingGaugeCountersAndPhaseTransitions(t *testing.T) {
	runPython(t, measurementHarness(t)+`
def index(l):
    pass
def browse(l):
    pass
class Behavior(TaskSet):
    tasks = {index: 1, browse: 3}
class Visitor(User):
    tasks = [Behavior]
configure_task_mix([Visitor], {})
profile_tasks = ["index", "browse"]
profile_seconds_per_task, profile_rps, warmup_seconds = 30.0, 6.0, 10.0
steps = [{"type":"constant","durationSeconds":60,"rps":30}]
environment = Environment()
measurement["environment"] = environment
Behavior.tasks[0](None)
Behavior.tasks[1](None)
publish_start("1000")
def series(text, name):
    return {line.split("{")[1].split("}")[0]: float(line.split(" ")[-1]) for line in text.splitlines() if line.startswith(name + "{")}
def plain(text, name):
    return [float(line.split(" ")[-1]) for line in text.splitlines() if line.startswith(name + " ")]
for at, phase, active, target in ((929.0, None, None, 0.0), (930.0, 3, "index", 6.0), (959.9, 3, "index", 6.0), (960.0, 3, "browse", 6.0), (990.0, 0, None, 30.0), (1000.0, 1, None, 30.0)):
    wall[0] = at
    text = prometheus_exposition()
    gauge = series(text, "kubephos_load_profiling_task")
    assert gauge == {'task="index"': 1.0 if active == "index" else 0.0, 'task="browse"': 1.0 if active == "browse" else 0.0}, (at, gauge)
    assert plain(text, "kubephos_load_phase") == ([] if phase is None else [phase]), (at, text)
    assert plain(text, "kubephos_load_target_rps") == [target], (at, text)
    assert update_phase(profile_elapsed()) == phase
    assert task_mode["target"] == active, (at, task_mode)
assert "# TYPE kubephos_load_task_executions_total counter" in text
assert series(text, "kubephos_load_task_executions_total") == {'task="browse"': 1.0, 'task="index"': 1.0}
# Statistics reset at t=0 only, so profiling and warmup are not measured.
assert environment.runner.stats.resets == 2 and measurement["measured_from"] == 1000.0
summary = locust_summary()
assert [e["task"] for e in summary["taskMix"]] == ["index", "browse"] and summary["taskExecutions"] == {"index": 1, "browse": 1}
# The phase watcher wakes at every window boundary.
assert abs(next_phase_delay(-70.5) - 0.5) < 1e-9 and abs(next_phase_delay(-40.2) - 0.2) < 1e-9 and abs(next_phase_delay(-10.3) - 0.3) < 1e-9
assert next_phase_delay(-100.0) == 1.0 and next_phase_delay(None) == 0.2
`)
}

func requestMixSpec() Spec {
	return Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: "start", Replicas: 1, Steps: []LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 30}}}
}

func TestRequestMixValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*Spec)
		path  string
		valid bool
	}{
		{"none", func(*Spec) {}, "", true},
		{"weights", func(spec *Spec) { spec.TaskWeights = map[string]float64{"index": 33.3, "browseProduct": 0} }, "", true},
		{"all zero is a runtime question", func(spec *Spec) { spec.TaskWeights = map[string]float64{"browseProduct": 0} }, "", true},
		{"negative", func(spec *Spec) { spec.TaskWeights = map[string]float64{"index": -1} }, "taskWeights", false},
		{"too large", func(spec *Spec) { spec.TaskWeights = map[string]float64{"index": 2e6} }, "taskWeights", false},
		{"bad name", func(spec *Spec) { spec.TaskWeights = map[string]float64{"not a name": 1} }, "taskWeights", false},
		{"profiling defaults", func(spec *Spec) { spec.ProfileCallChain = true }, "", true},
		{"profiling bounds", func(spec *Spec) { spec.ProfileCallChain, spec.ProfileSecondsPerTask, spec.ProfileRPS = true, 600, 0.5 }, "", true},
		{"profiling too short", func(spec *Spec) { spec.ProfileCallChain, spec.ProfileSecondsPerTask = true, 29 }, "profileSecondsPerTask", false},
		{"profiling too long", func(spec *Spec) { spec.ProfileSecondsPerTask = 601 }, "profileSecondsPerTask", false},
		{"profiling rate", func(spec *Spec) { spec.ProfileCallChain, spec.ProfileRPS = true, -2 }, "profileRps", false},
		{"interactive profiling", func(spec *Spec) { spec.ProfileCallChain, spec.Interactive, spec.Steps = true, true, nil }, "profileCallChain", false},
	} {
		spec := requestMixSpec()
		tc.edit(&spec)
		raw, _ := json.Marshal(spec)
		report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw})
		if report.Valid != tc.valid || (!tc.valid && report.Issues[len(report.Issues)-1].Path != tc.path) {
			t.Fatalf("%s: %#v", tc.name, report)
		}
		if path, err := validateRequestMix(withDefaults(spec)); (err == nil) != tc.valid || path != tc.path {
			t.Fatalf("%s: precheck validation %q %v", tc.name, path, err)
		}
	}
	// NaN cannot travel through JSON, but a programmatic spec is still rejected.
	spec := requestMixSpec()
	spec.TaskWeights = map[string]float64{"index": nan()}
	if _, err := validateRequestMix(spec); err == nil {
		t.Fatal("NaN weight accepted")
	}
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}

func TestRequestMixDefaultsAndManifest(t *testing.T) {
	min, max := 2.0, 6.0
	for _, tc := range []struct {
		steps []LoadStep
		want  float64
	}{
		{[]LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 30}}, 10},
		{[]LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 4}}, 4},
		{[]LoadStep{{Type: "exponential", DurationSeconds: 60, StartRPS: 0, EndRPS: 100}}, 10},
		{[]LoadStep{{Type: "sinusoidal", DurationSeconds: 60, MinRPS: &min, MaxRPS: &max, PeriodSeconds: 60}}, 4},
	} {
		spec := withDefaults(Spec{ProfileCallChain: true, Steps: tc.steps})
		if spec.ProfileRPS != tc.want || spec.ProfileSecondsPerTask != 60 {
			t.Fatalf("%+v: profileRps %g secondsPerTask %d", tc.steps, spec.ProfileRPS, spec.ProfileSecondsPerTask)
		}
	}
	if spec := withDefaults(Spec{Steps: []LoadStep{{Type: "constant", DurationSeconds: 60, RPS: 4}}}); spec.ProfileRPS != 0 || spec.ProfileSecondsPerTask != 0 {
		t.Fatalf("profiling defaults without profiling: %+v", spec)
	}
	raw, _ := json.Marshal(requestMixSpec())
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil || strings.Contains(string(plan.Steps[0].Input), "profile") || strings.Contains(string(plan.Steps[0].Input), "taskWeights") {
		t.Fatalf("legacy plan input changed: %s %v", plan.Steps[0].Input, err)
	}
	profile := loadProfile{ID: "journey", Engine: "locust", RuntimeImage: "example.test/locust:1.0.0", Script: "from locust import HttpUser\n", Workload: scenarioWorkload("journey")}
	targets := []loadTarget{{URL: "http://node-proxy:80/", Weight: 1}}
	plain, err := loadManifest(profile, "owner", withDefaults(requestMixSpec()), targets)
	if err != nil || strings.Contains(string(plain), "name: KUBEPHOS_TASK_WEIGHTS") || strings.Contains(string(plain), "name: KUBEPHOS_PROFILE") {
		t.Fatalf("manifest without a request mix: %v\n%s", err, plain)
	}
	spec := requestMixSpec()
	spec.TaskWeights = map[string]float64{"index": 1, "browseProduct": 0}
	spec.ProfileCallChain, spec.ProfileRPS = true, 2.5
	manifest, err := loadManifest(profile, "owner", withDefaults(spec), targets)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`name: KUBEPHOS_TASK_WEIGHTS\s+value: .\{"browseProduct":0,"index":1\}.\n`, `name: KUBEPHOS_PROFILE_CALL_CHAIN\s+value: "true"`, `name: KUBEPHOS_PROFILE_SECONDS_PER_TASK\s+value: "60"`, `name: KUBEPHOS_PROFILE_RPS\s+value: "2.5"`} {
		if !regexp.MustCompile(expected).Match(manifest) {
			t.Fatalf("manifest missing %s:\n%s", expected, manifest)
		}
	}
	for _, expected := range []string{`os.environ.get("KUBEPHOS_TASK_WEIGHTS", "")`, `os.environ.get("KUBEPHOS_PROFILE_CALL_CHAIN") == "true"`, `os.environ.get("KUBEPHOS_PROFILE_SECONDS_PER_TASK", "60")`, `os.environ.get("KUBEPHOS_PROFILE_RPS", "0")`, `task_plan_file = "` + taskPlanFile + `"`} {
		if !strings.Contains(locustWrapper, expected) {
			t.Fatalf("wrapper does not read %s", expected)
		}
	}
}

// The manifest schema duplicated in Go must stay identical to plugin.yaml.
func TestManifestSchemaMatchesPluginYAML(t *testing.T) {
	raw, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Spec struct {
			ConfigurationSchema any `yaml:"configurationSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document.Spec.ConfigurationSchema)
	var fromYAML, fromGo any
	_ = json.Unmarshal(encoded, &fromYAML)
	if err := json.Unmarshal((Plugin{}).Manifest().Schema, &fromGo); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromYAML, fromGo) {
		t.Fatalf("plugin.yaml and Manifest schema differ:\n%s\n%s", encoded, (Plugin{}).Manifest().Schema)
	}
}

func TestCallChainProfilingPrecedesWarmupInTheSharedTimeline(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker", planMisses: 2}
	fastSharedStart(t, nil)
	poll := taskPlanPoll
	taskPlanPoll = time.Millisecond
	t.Cleanup(func() { taskPlanPoll = poll })
	runner.taskPlan = `{"taskMix":[{"task":"index","weight":1,"share":0.25},{"task":"browseProduct","weight":3,"share":0.75}],"profileTasks":["index","browseProduct"]}`
	now := time.Date(2030, 1, 2, 3, 4, 5, 400_000_000, time.UTC)
	calls := 0
	// The host clock reaches the end of profiling + warmup by the time Execute
	// waits for the profile, so the test does not sleep through the profiling.
	clock := func() time.Time {
		calls++
		if calls > 2 {
			return now.Add(62 * time.Second)
		}
		return now
	}
	plugin := Plugin{Runner: runner, Clock: clock}
	profilingStart := time.Date(2030, 1, 2, 3, 4, 7, 0, time.UTC)
	warmupStart := profilingStart.Add(60 * time.Second)
	started := warmupStart.Add(time.Second)
	reported := `,"taskMix":[{"task":"index","weight":1,"share":0.5},{"task":"browseProduct","weight":1,"share":0.5}]}`
	runner.summaries = map[string]string{fakePodName(0): strings.TrimSuffix(workerSummaryJSON(started, 4, map[string]int{"10": 4}, 1, 0, 0, 0), "}") + reported}
	spec := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: "start", Replicas: 1, MaxUsers: 20, SpawnRate: 2, Steps: []LoadStep{{Type: "constant", DurationSeconds: 1, RPS: 30}}, WarmupSeconds: 1, TaskWeights: map[string]float64{"index": 1, "browseProduct": 1}, ProfileCallChain: true, ProfileSecondsPerTask: 30}
	rawSpec, _ := json.Marshal(spec)
	plan, err := plugin.Plan(context.Background(), rawSpec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck %#v %v", health, err)
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
	if !session.StartedAt.Equal(started) || !session.WarmupStartedAt.Equal(warmupStart) || !session.CompletedAt.Equal(started.Add(time.Second)) {
		t.Fatalf("timeline: warmup %v start %v end %v", session.WarmupStartedAt, session.StartedAt, session.CompletedAt)
	}
	want := &callChainProfile{RPS: 10, SecondsPerTask: 30, Windows: []callChainWindow{{Task: "index", From: profilingStart, To: profilingStart.Add(30 * time.Second)}, {Task: "browseProduct", From: profilingStart.Add(30 * time.Second), To: warmupStart}}}
	if !reflect.DeepEqual(session.CallChainProfile, want) {
		t.Fatalf("call-chain profile %s", raw)
	}
	// The mix reported by the workers wins over the plan read before the start.
	if len(session.TaskMix) != 2 || session.TaskMix[0].Share != 0.5 || session.LocustSummary == nil {
		t.Fatalf("task mix %s", raw)
	}
	armed := 0
	for _, command := range runner.commands {
		if strings.Contains(command, "-c locust -- /bin/sh -ec") {
			armed++
			// Workers must hold the start before the first profiling window; they
			// receive only t=0 of the measured profile and derive the rest.
			if !strings.Contains(command, "test $(("+itoa(profilingStart.Unix())+" - now)) -lt 1") || !strings.Contains(command, "printf '%s' "+itoa(started.Unix())+" > /tmp/kubephos-start") {
				t.Fatalf("worker start command %s", command)
			}
		}
	}
	if armed != 1 {
		t.Fatalf("armed %d workers", armed)
	}
	if health, err := plugin.Verify(context.Background(), step, raw, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify %#v %v", health, err)
	}
	for name, tamper := range map[string]func(*loadSessionSpec){
		"gap": func(s *loadSessionSpec) {
			s.CallChainProfile.Windows[1].From = s.CallChainProfile.Windows[1].From.Add(time.Second)
		},
		"rate":       func(s *loadSessionSpec) { s.CallChainProfile.RPS = 3 },
		"not ending": func(s *loadSessionSpec) { s.CallChainProfile.Windows = s.CallChainProfile.Windows[:1] },
		"shares":     func(s *loadSessionSpec) { s.TaskMix[0].Share = 0.9 },
	} {
		var copy result
		_ = json.Unmarshal(raw, &copy)
		tamper(&copy.LoadSession.Spec)
		tampered, _ := json.Marshal(copy)
		if health, _ := plugin.Verify(context.Background(), step, tampered, discardLog); health.Status == domain.HealthHealthy {
			t.Fatalf("verify accepted a tampered artifact (%s)", name)
		}
	}
}

func TestCallChainProfilingFailsWithoutTaskPlan(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	fastSharedStart(t, nil)
	timeout, poll := taskPlanTimeout, taskPlanPoll
	taskPlanTimeout, taskPlanPoll = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { taskPlanTimeout, taskPlanPoll = timeout, poll })
	spec := requestMixSpec()
	spec.ProfileCallChain = true
	rawSpec, _ := json.Marshal(spec)
	plan, _ := (Plugin{}).Plan(context.Background(), rawSpec)
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	if _, err := (Plugin{Runner: runner}).Execute(context.Background(), step, discardLog); err == nil || !strings.Contains(err.Error(), "task plan") {
		t.Fatalf("missing task plan: %v", err)
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "/tmp/kubephos-start") {
			t.Fatal("start published without a known profiling timeline")
		}
	}
}

func itoa(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
