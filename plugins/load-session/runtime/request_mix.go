package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultProfileSecondsPerTask = 60
	minProfileSecondsPerTask     = 30
	maxProfileSecondsPerTask     = 600
	maxProfileRPS                = 100000
	maxTaskWeights               = 200
	maxTaskWeight                = 1e6
	// taskPlanFile is written by the wrapper at import: the effective task mix and
	// the tasks it will profile, in order. KubePhos reads it before publishing the
	// shared start so the profiling windows are part of the known timeline.
	taskPlanFile = "/tmp/kubephos-task-plan.json"
)

var taskNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// taskPlanTimeout bounds the wait for the wrapper's task plan; taskPlanPoll is the
// retry interval while Locust is still importing the scenario (tests shorten it).
var (
	taskPlanTimeout = 2 * time.Minute
	taskPlanPoll    = time.Second
)

// taskMixEntry is one task function of the effective mix: its configured weight
// (in its containing class, before the integer conversion) and its share of task
// executions over all User classes and nested TaskSets (shares sum to 1).
type taskMixEntry struct {
	Task   string  `json:"task"`
	Weight float64 `json:"weight"`
	Share  float64 `json:"share"`
}

type callChainProfile struct {
	RPS            float64           `json:"rps"`
	SecondsPerTask int               `json:"secondsPerTask"`
	Windows        []callChainWindow `json:"windows"`
}

type callChainWindow struct {
	Task string    `json:"task"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type taskPlan struct {
	TaskMix      []taskMixEntry `json:"taskMix"`
	ProfileTasks []string       `json:"profileTasks"`
}

// validateRequestMix checks the shape of the request-mix inputs. Task names can
// only be matched against the locustfile at runtime: unknown names are logged and
// ignored by the wrapper, and a mix without any positive weight stops it.
func validateRequestMix(spec Spec) (string, error) {
	if len(spec.TaskWeights) > maxTaskWeights {
		return "taskWeights", fmt.Errorf("at most %d task weights are supported", maxTaskWeights)
	}
	for name, weight := range spec.TaskWeights {
		if !taskNamePattern.MatchString(name) {
			return "taskWeights", fmt.Errorf("task name %q must be a Python function, method or TaskSet class name", name)
		}
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > maxTaskWeight {
			return "taskWeights", fmt.Errorf("task %s weight must be a finite number between 0 and %g", name, float64(maxTaskWeight))
		}
	}
	if spec.ProfileSecondsPerTask != 0 && (spec.ProfileSecondsPerTask < minProfileSecondsPerTask || spec.ProfileSecondsPerTask > maxProfileSecondsPerTask) {
		return "profileSecondsPerTask", fmt.Errorf("profiling seconds per task must be between %d and %d", minProfileSecondsPerTask, maxProfileSecondsPerTask)
	}
	if spec.ProfileRPS != 0 && (math.IsNaN(spec.ProfileRPS) || math.IsInf(spec.ProfileRPS, 0) || spec.ProfileRPS < 0.01 || spec.ProfileRPS > maxProfileRPS) {
		return "profileRps", errors.New("profiling requests per second must be between 0.01 and 100000")
	}
	if spec.ProfileCallChain && spec.Interactive {
		return "profileCallChain", errors.New("call-chain profiling requires a finite start load profile")
	}
	return "", nil
}

// defaultProfileRPS is the lower of 10 and the profile's initial (t=0) rate.
func defaultProfileRPS(steps []LoadStep) float64 {
	initial := 0.0
	if len(steps) > 0 {
		step := steps[0]
		switch step.Type {
		case "constant":
			initial = step.RPS
		case "sinusoidal":
			baseline, amplitude := step.BaselineRPS, step.AmplitudeRPS
			if step.MinRPS != nil && step.MaxRPS != nil {
				baseline, amplitude = (*step.MinRPS+*step.MaxRPS)/2, (*step.MaxRPS-*step.MinRPS)/2
			}
			period := math.Max(float64(step.PeriodSeconds), 1)
			initial = baseline + amplitude*math.Sin(2*math.Pi*float64(step.PhaseSeconds)/period)
		case "exponential":
			initial = step.StartRPS
		}
	}
	if !(initial > 0) {
		return 10
	}
	return math.Min(10, initial)
}

func requestMixEnvironment(spec Spec) []any {
	environment := []any{}
	if len(spec.TaskWeights) > 0 {
		value, _ := json.Marshal(spec.TaskWeights)
		environment = append(environment, map[string]any{"name": "KUBEPHOS_TASK_WEIGHTS", "value": string(value)})
	}
	if spec.ProfileCallChain {
		environment = append(environment,
			map[string]any{"name": "KUBEPHOS_PROFILE_CALL_CHAIN", "value": "true"},
			map[string]any{"name": "KUBEPHOS_PROFILE_SECONDS_PER_TASK", "value": strconv.Itoa(spec.ProfileSecondsPerTask)},
			map[string]any{"name": "KUBEPHOS_PROFILE_RPS", "value": strconv.FormatFloat(spec.ProfileRPS, 'g', -1, 64)},
		)
	}
	return environment
}

// readTaskPlan reads the task plan of every worker; they run the same script with
// the same weights, so their profiling orders must be identical.
func readTaskPlan(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int) (taskPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, taskPlanTimeout)
	defer cancel()
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "json")
	if err != nil {
		return taskPlan{}, fmt.Errorf("list load workers: %w", err)
	}
	var pods struct {
		Items []timelinePod `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &pods) != nil || len(pods.Items) != replicas {
		return taskPlan{}, errors.New("the load worker set is incomplete")
	}
	var mu sync.Mutex
	plans := map[string]taskPlan{}
	err = parallelTimelinePods(ctx, pods.Items, func(ctx context.Context, pod timelinePod) error {
		for {
			value, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", namespace, pod.Metadata.Name, "-c", "locust", "--", "cat", taskPlanFile)
			if err == nil {
				var plan taskPlan
				if json.Unmarshal([]byte(value), &plan) != nil {
					return fmt.Errorf("worker %s returned an invalid task plan", pod.Metadata.Name)
				}
				mu.Lock()
				plans[pod.Metadata.Name] = plan
				mu.Unlock()
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("worker %s did not publish its task plan: %w", pod.Metadata.Name, err)
			case <-time.After(taskPlanPoll):
			}
		}
	})
	if err != nil {
		return taskPlan{}, err
	}
	names := make([]string, 0, len(plans))
	for name := range plans {
		names = append(names, name)
	}
	sort.Strings(names)
	first := plans[names[0]]
	for _, name := range names[1:] {
		if !reflect.DeepEqual(plans[name].ProfileTasks, first.ProfileTasks) {
			return taskPlan{}, errors.New("load workers disagree on the tasks to profile")
		}
	}
	for _, task := range first.ProfileTasks {
		if strings.TrimSpace(task) == "" {
			return taskPlan{}, errors.New("task plan contains an unnamed task")
		}
	}
	return first, nil
}

// callChainWindows places one window per task, back to back, from the first
// profiling instant (cluster clock) up to the warmup start.
func callChainWindows(tasks []string, profilingStart time.Time, secondsPerTask int) []callChainWindow {
	windows := make([]callChainWindow, 0, len(tasks))
	step := time.Duration(secondsPerTask) * time.Second
	for index, task := range tasks {
		from := profilingStart.Add(time.Duration(index) * step)
		windows = append(windows, callChainWindow{Task: task, From: from, To: from.Add(step)})
	}
	return windows
}

func validateRequestMixResult(session loadSession, spec Spec) error {
	total := 0.0
	for _, entry := range session.Spec.TaskMix {
		if entry.Task == "" || math.IsNaN(entry.Weight) || math.IsInf(entry.Weight, 0) || entry.Weight < 0 || math.IsNaN(entry.Share) || entry.Share < 0 || entry.Share > 1 {
			return errors.New("load session task mix is invalid")
		}
		total += entry.Share
	}
	if len(session.Spec.TaskMix) > 0 && math.Abs(total-1) > 1e-6 {
		return errors.New("load session task mix shares do not sum to 1")
	}
	profile := session.Spec.CallChainProfile
	if profile == nil {
		return nil
	}
	if !spec.ProfileCallChain || session.Spec.StartedAt == nil || profile.RPS != spec.ProfileRPS || profile.SecondsPerTask != spec.ProfileSecondsPerTask || len(profile.Windows) == 0 {
		return errors.New("load session call-chain profile does not match the configuration")
	}
	step := time.Duration(spec.ProfileSecondsPerTask) * time.Second
	for index, window := range profile.Windows {
		if window.Task == "" || window.To.Sub(window.From) != step || (index > 0 && !window.From.Equal(profile.Windows[index-1].To)) {
			return errors.New("load session call-chain windows are not contiguous")
		}
	}
	warmupStart := session.Spec.StartedAt.Add(-time.Duration(spec.WarmupSeconds) * time.Second)
	if !profile.Windows[len(profile.Windows)-1].To.Equal(warmupStart) {
		return errors.New("load session call-chain profile does not end at the warmup start")
	}
	return nil
}
