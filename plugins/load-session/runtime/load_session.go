package loadsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"math"
	"net"
	"net/url"

	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/externaldestination"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID                = "io.kubephos.load.kubernetes.session"
	artifactAPI             = "artifacts.kubephos.dev/v1alpha1"
	ownershipKey            = "kubephos.dev/ownership-marker"
	applicationOwnershipKey = "kubephos.dev/application-marker"
	loadProfileKey          = "kubephos.dev/load-scenario"
	previousReplicasKey     = "kubephos.dev/load-previous-replicas"
	maxWarmupSeconds        = 3600
	maxDrainSeconds         = 600
	defaultDrainSeconds     = 30
	// startClockEnv selects how a Locust replica finds the profile's t=0:
	// "shared" waits for the instant KubePhos publishes to every worker, "local"
	// (resume and restore) starts the profile when the process starts.
	startClockEnv = "KUBEPHOS_START_CLOCK"
	// loadWorkersEnv tells every Locust replica how many independent workers share
	// the configured (total) offered load, so each paces rps_at(t)/workers.
	loadWorkersEnv = "KUBEPHOS_LOAD_WORKERS"
)

type Plugin struct {
	Runner commandRunner
	Clock  func() time.Time
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	NetworkTimeline          bool             `json:"networkTimeline,omitempty"`
	ClusterName              string           `json:"clusterName,omitempty"`
	Peers                    []ClusterPeer    `json:"peers,omitempty"`
	ClusterConnectionRef     string           `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string           `json:"applicationDeploymentRef"`
	LoadScenarioSetRef       string           `json:"loadScenarioSetRef"`
	ScenarioID               string           `json:"scenarioId"`
	Action                   string           `json:"action"`
	Interactive              bool             `json:"interactive,omitempty"`
	Replicas                 int              `json:"replicas"`
	MaxUsers                 int              `json:"maxUsers,omitempty"`
	SpawnRate                float64          `json:"spawnRate,omitempty"`
	Steps                    []LoadStep       `json:"steps,omitempty"`
	GeographicSteps          []GeographicStep `json:"geographicSteps,omitempty"`
	// WarmupSeconds of traffic at the profile's initial rate precede the measured
	// profile; DrainSeconds keep idle workers serving metrics after it ends.
	WarmupSeconds int  `json:"warmupSeconds,omitempty"`
	DrainSeconds  *int `json:"drainSeconds,omitempty"`
	// TaskWeights overrides the locustfile weight of the named tasks (function or
	// method __name__, or TaskSet class name); fractional weights keep their ratios.
	TaskWeights map[string]float64 `json:"taskWeights,omitempty"`
	// ProfileCallChain runs every task alone for ProfileSecondsPerTask at
	// ProfileRPS (total) before the warmup, to measure its call chain.
	ProfileCallChain      bool    `json:"profileCallChain,omitempty"`
	ProfileSecondsPerTask int     `json:"profileSecondsPerTask,omitempty"`
	ProfileRPS            float64 `json:"profileRps,omitempty"`
}

type LoadStep struct {
	MinRPS          *float64 `json:"minRps,omitempty"`
	MaxRPS          *float64 `json:"maxRps,omitempty"`
	Type            string   `json:"type"`
	DurationSeconds int      `json:"durationSeconds"`
	RPS             float64  `json:"rps,omitempty"`
	BaselineRPS     float64  `json:"baselineRps,omitempty"`
	AmplitudeRPS    float64  `json:"amplitudeRps,omitempty"`
	PeriodSeconds   int      `json:"periodSeconds,omitempty"`
	PhaseSeconds    int      `json:"phaseSeconds,omitempty"`
	StartRPS        float64  `json:"startRps,omitempty"`
	EndRPS          float64  `json:"endRps,omitempty"`
	Curve           float64  `json:"curve,omitempty"`
}

type GeographicStep struct {
	Type            string             `json:"type"`
	DurationSeconds int                `json:"durationSeconds"`
	Weights         map[string]float64 `json:"weights,omitempty"`
	StartWeights    map[string]float64 `json:"startWeights,omitempty"`
	EndWeights      map[string]float64 `json:"endWeights,omitempty"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Networking   string `json:"networking,omitempty"`
		Distribution string `json:"distribution"`
		Server       string `json:"server"`
		Kubeconfig   string `json:"kubeconfig"`
	} `json:"spec"`
}

type applicationDeployment struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string `json:"name"`
		Version         string `json:"version"`
		OwnershipMarker string `json:"ownershipMarker"`
	} `json:"metadata"`
	Spec struct {
		ApplicationRef string            `json:"applicationRef"`
		ClusterServer  string            `json:"clusterServer"`
		Namespace      string            `json:"namespace"`
		ManifestDigest string            `json:"manifestDigest"`
		Endpoints      []serviceEndpoint `json:"endpoints"`
	} `json:"spec"`
}

type serviceEndpoint struct {
	Targets   []externaldestination.Endpoint `json:"targets,omitempty"`
	ID        string                         `json:"id"`
	Component string                         `json:"component"`
	Service   string                         `json:"service"`
	Port      int                            `json:"port"`
	Protocol  string                         `json:"protocol"`
	Path      string                         `json:"path,omitempty"`
	URL       string                         `json:"url,omitempty"`
	External  bool                           `json:"external,omitempty"`
}

type loadProfileSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ApplicationRef string `json:"applicationRef"`
		Digest         string `json:"digest"`
	} `json:"metadata"`
	Spec struct {
		Scenarios []loadProfile `json:"scenarios"`
	} `json:"spec"`
}

type loadProfile struct {
	ID             string            `json:"id"`
	Engine         string            `json:"engine"`
	TargetEndpoint string            `json:"targetEndpoint"`
	RuntimeImage   string            `json:"runtimeImage"`
	Script         string            `json:"script"`
	ScriptDigest   string            `json:"scriptDigest"`
	Parameters     map[string]string `json:"parameters,omitempty"`
	Workload       workloadTarget    `json:"-"`
}

type workloadTarget struct {
	ID         string            `json:"id"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Selector   map[string]string `json:"selector"`
	Traits     []string          `json:"traits"`
}

type loadSession struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   loadSessionMetadata `json:"metadata"`
	Spec       loadSessionSpec     `json:"spec"`
}

type loadSessionMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type loadSessionSpec struct {
	ApplicationRef   string           `json:"applicationRef"`
	ClusterServer    string           `json:"clusterServer"`
	Namespace        string           `json:"namespace"`
	ScenarioID       string           `json:"scenarioId"`
	Action           string           `json:"action"`
	State            string           `json:"state"`
	Replicas         int              `json:"replicas"`
	PreviousReplicas int              `json:"previousReplicas"`
	Steps            []LoadStep       `json:"steps"`
	GeographicSteps  []GeographicStep `json:"geographicSteps,omitempty"`
	DurationSeconds  int              `json:"durationSeconds"`
	// StartedAt and CompletedAt delimit the measured profile (t=0 and t=end).
	StartedAt       *time.Time     `json:"startedAt,omitempty"`
	CompletedAt     *time.Time     `json:"completedAt,omitempty"`
	Workload        workloadTarget `json:"workload"`
	WarmupSeconds   *int           `json:"warmupSeconds,omitempty"`
	WarmupStartedAt *time.Time     `json:"warmupStartedAt,omitempty"`
	DrainSeconds    *int           `json:"drainSeconds,omitempty"`
	Clock           string         `json:"clock,omitempty"`
	// Workers is the number of Locust processes that ran the measured profile;
	// Replicas is already zero once a finite session completes.
	Workers       int            `json:"workers,omitempty"`
	LocustSummary *locustSummary `json:"locustSummary,omitempty"`
	// TaskMix is the effective request mix the workers applied; CallChainProfile
	// the per-task profiling windows (cluster clock) that preceded the warmup.
	TaskMix          []taskMixEntry    `json:"taskMix,omitempty"`
	CallChainProfile *callChainProfile `json:"callChainProfile,omitempty"`
}

type loadTarget struct {
	URL    string  `json:"url"`
	Zone   string  `json:"zone,omitempty"`
	Weight float64 `json:"weight"`
}

type serviceState struct {
	Spec struct {
		Ports []struct {
			Port     int `json:"port"`
			NodePort int `json:"nodePort"`
		} `json:"ports"`
	} `json:"spec"`
}

type nodeStateList struct {
	Items []struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
		} `json:"status"`
	} `json:"items"`
}

type result struct {
	LoadSession loadSession `json:"loadSession"`
}

type workloadState struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Replicas int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int `json:"readyReplicas"`
	} `json:"status"`
}

type podListState struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			ContainerStatuses []struct {
				Name         string `json:"name"`
				Ready        bool   `json:"ready"`
				RestartCount int    `json:"restartCount"`
				State        struct {
					Running    json.RawMessage `json:"running"`
					Waiting    json.RawMessage `json:"waiting"`
					Terminated json.RawMessage `json:"terminated"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Interactive Kubernetes load session", Version: "0.11.0",
		Description:     "Runs an ordered RPS timeline using a verified Locust journey against a catalog application or external HTTP destination.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","loadScenarioSetRef","scenarioId","action","replicas","maxUsers","spawnRate"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"loadScenarioSetRef":{"type":"string","title":"Load scenarios","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadScenarioSet","x-kubephos-artifact-version":"v1alpha1"},"scenarioId":{"type":"string","title":"Load scenario","description":"User behavior implemented by the Locust file supplied by the selected application.","minLength":1,"maxLength":63,"default":"storefront-journey"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["start","stop","resume"],"default":"start"},"interactive":{"type":"boolean","title":"Interactive session","default":false},"replicas":{"type":"integer","title":"Load workers","minimum":1,"maximum":100,"default":1},"maxUsers":{"type":"integer","title":"Maximum concurrent users per worker","minimum":1,"maximum":100000,"default":1000},"spawnRate":{"type":"number","title":"Users started per second","minimum":0.1,"maximum":100000,"default":10},"steps":{"type":"array","title":"Temporal workload timeline","maxItems":100,"items":{"type":"object"}},"geographicSteps":{"type":"array","title":"Geographic traffic timeline","maxItems":100,"items":{"type":"object"}},"clusterName":{"type":"string","x-kubephos-hidden":true},"peers":{"type":"array","minItems":1,"maxItems":5,"x-kubephos-hidden":true,"items":{"type":"object","required":["name","clusterConnectionRef","applicationDeploymentRef"],"additionalProperties":false,"properties":{"name":{"type":"string"},"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"}}}},"networkTimeline":{"type":"boolean","x-kubephos-hidden":true},"warmupSeconds":{"type":"integer","title":"Warmup seconds","description":"Traffic at the profile's initial rate sent before the measured profile starts; excluded from the measurement window.","minimum":0,"maximum":3600,"default":0},"drainSeconds":{"type":"integer","title":"Drain seconds","description":"Idle time after the profile before the load workers stop, so the final metrics are scraped.","minimum":0,"maximum":600,"default":30},"taskWeights":{"type":"object","title":"Request mix","description":"Relative weight of each Locust task (function or method name, or TaskSet class name); tasks not listed keep the locustfile weight.","x-kubephos-hidden":true},"profileCallChain":{"type":"boolean","title":"Measure call chain","description":"Before the warmup, run every task alone to measure how many requests each service receives per task execution.","default":false},"profileSecondsPerTask":{"type":"integer","title":"Profiling seconds per task","minimum":30,"maximum":600,"default":60},"profileRps":{"type":"number","title":"Profiling requests per second","description":"Total rate while profiling; defaults to the lower of 10 and the profile's initial rate.","minimum":0.01,"maximum":100000}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "LoadScenarioSet", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "LoadSession", Version: "v1alpha1"}},
		Capabilities:    []string{"load.session.control", "load.session.preflight", "load.session.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"cluster.admin"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	refs := []struct{ path, value string }{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"applicationDeploymentRef", spec.ApplicationDeploymentRef}, {"loadScenarioSetRef", spec.LoadScenarioSetRef}}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.value, "art_") {
			return invalid(report, ref.path, "Select a verified artifact.")
		}
		if seen[ref.value] {
			return invalid(report, ref.path, "Each input must reference its own typed artifact.")
		}
		seen[ref.value] = true
	}
	if spec.ScenarioID == "" || len(spec.ScenarioID) > 63 || strings.ContainsAny(spec.ScenarioID, "\n\r") {
		return invalid(report, "scenarioId", "Select a valid declared load scenario.")
	}
	if spec.Action != "start" && spec.Action != "stop" && spec.Action != "resume" {
		return invalid(report, "action", "Action must be start, stop or resume.")
	}
	if spec.Replicas < 1 || spec.Replicas > 100 {
		return invalid(report, "replicas", "Load workers must be between 1 and 100.")
	}
	if spec.MaxUsers != 0 && (spec.MaxUsers < 1 || spec.MaxUsers > 100000) {
		return invalid(report, "maxUsers", "Maximum concurrent users must be between 1 and 100000.")
	}
	if spec.SpawnRate != 0 && (spec.SpawnRate < 0.1 || spec.SpawnRate > 100000) {
		return invalid(report, "spawnRate", "Spawn rate must be between 0.1 and 100000.")
	}
	if spec.WarmupSeconds < 0 || spec.WarmupSeconds > maxWarmupSeconds {
		return invalid(report, "warmupSeconds", "Warmup must be between 0 and 3600 seconds.")
	}
	if spec.DrainSeconds != nil && (*spec.DrainSeconds < 0 || *spec.DrainSeconds > maxDrainSeconds) {
		return invalid(report, "drainSeconds", "Drain must be between 0 and 600 seconds.")
	}
	if path, err := validateRequestMix(spec); err != nil {
		return invalid(report, path, err.Error())
	}
	duration, err := validateSpecTimeline(spec)
	if err != nil {
		return invalid(report, "steps", err.Error())
	}
	if err := validateGeographicTimeline(spec.GeographicSteps, duration); err != nil {
		return invalid(report, "geographicSteps", err.Error())
	}
	if err := checkSessionBudget(spec, duration, 0); err != nil {
		return invalid(report, "steps", err.Error())
	}
	if spec.ProfileCallChain {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("Call-chain profiling adds %ds per task with a positive weight before the warmup.", withDefaults(spec).ProfileSecondsPerTask)})
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("The experiment duration is %s, calculated from %d ordered workload steps.", (time.Duration(duration) * time.Second).String(), len(spec.Steps))})
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will revalidate the cluster, application ownership, load profile, geographic coverage, current session state and target endpoint before changing load."})
	return report
}

func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	if spec.Action != "start" && spec.Action != "stop" && spec.Action != "resume" {
		return domain.Plan{}, errors.New("action must be start, stop or resume")
	}
	spec = withDefaults(spec)
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	plan := domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "control-load-session", Name: strings.ToUpper(spec.Action[:1]) + spec.Action[1:] + " declared load profile", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", ArtifactID: spec.LoadScenarioSetRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "load-session", Type: "LoadSession", Version: "v1alpha1", MediaType: "application/json", Source: "/loadSession"}},
	}}}
	plan.Steps[0].ArtifactInputs = append(plan.Steps[0].ArtifactInputs, clusterPeerInputs(spec.Peers)...)
	return plan, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, deployment, profiles, profile, endpoint, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	duration, err := validateSpecTimeline(spec)
	if err != nil {
		return unhealthy(err.Error(), "loadTimeline", "invalid"), nil
	}
	if _, err := validateRequestMix(spec); err != nil {
		return unhealthy(err.Error(), "requestMix", "invalid"), nil
	}
	if err := validateGeographicTimeline(spec.GeographicSteps, duration); err != nil {
		return unhealthy(err.Error(), "geographicTimeline", "invalid"), nil
	}
	if err := validateArtifacts(cluster, deployment, profiles, profile, endpoint); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := log("info", "Validating cluster, namespace ownership, load profile, target endpoint and current session state"); err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--namespace", deployment.Spec.Namespace)
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("Kubernetes authorization check failed", "authorization", "denied"), nil
	}
	if err := verifyNamespace(ctx, runner, cluster.Spec.Kubeconfig, deployment); err != nil {
		return unhealthy(err.Error(), "namespace", "invalid"), nil
	}
	state, exists, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile.Workload)
	if err != nil {
		return unhealthy(err.Error(), "loadSession", "unknown"), nil
	}
	owned := exists && state.Metadata.Annotations[applicationOwnershipKey] == deployment.Metadata.OwnershipMarker && state.Metadata.Annotations[loadProfileKey] == profile.ID
	if step.Cleanup {
		if !exists && spec.Action == "start" {
			return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The load workload is already absent", Checks: map[string]string{"loadSession": "absent", "ownership": "verified"}}, nil
		}
		if !owned {
			return unhealthy("Only the load workload owned by this application can be reset", "loadSession", "invalid"), nil
		}
		if spec.Action != "start" {
			if _, err := storedPreviousReplicas(state); err != nil {
				return unhealthy(err.Error(), "loadSession", "invalid"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The load session is safe to reset", Checks: map[string]string{"loadSession": "owned", "ownership": "verified"}}, nil
	}
	switch spec.Action {
	case "start":
		if exists {
			return unhealthy("The declared load workload already exists; use resume only after a KubePhos stop", "loadSession", "conflict"), nil
		}
		targets, err := resolveRuntimeLoadTargets(ctx, runner, step, cluster, deployment, endpoint, spec)
		if err != nil {
			return unhealthy(err.Error(), "geographicDistribution", "invalid"), nil
		}
		manifest, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, targets, deployment.Spec.Namespace)
		if err != nil {
			return unhealthy(err.Error(), "manifest", "invalid"), nil
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=server", "--namespace", deployment.Spec.Namespace, "-f", "-"); err != nil {
			return unhealthy("Load manifest admission dry-run failed: "+err.Error(), "admission", "rejected"), nil
		}
	case "stop":
		if !owned || state.Spec.Replicas == 0 {
			return unhealthy("Only a running load workload owned by this application can be stopped", "loadSession", "invalid-transition"), nil
		}
	case "resume":
		if !owned || state.Spec.Replicas != 0 {
			return unhealthy("Only a stopped load workload owned by this application can be resumed", "loadSession", "invalid-transition"), nil
		}
	}
	if spec.Action != "stop" && !endpoint.External {
		addresses, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "endpoints", endpoint.Service, "-n", deployment.Spec.Namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
		if err != nil || strings.TrimSpace(addresses) == "" {
			return unhealthy("The declared load target has no ready backend", "targetEndpoint", "unhealthy"), nil
		}
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Load session transition is valid and ready for confirmation", Checks: map[string]string{"api": "ready", "namespace": "owned", "profile": "verified", "transition": spec.Action, "targetEndpoint": "ready"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, deployment, _, profile, endpoint, err := resolve(step)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	state, exists, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile.Workload)
	if err != nil {
		return nil, err
	}
	previous := 0
	durationSeconds, _ := validateSpecTimeline(spec)
	var startedAt, completedAt, warmupStartedAt *time.Time
	var summary *locustSummary
	var taskMix []taskMixEntry
	var profiled *callChainProfile
	if exists {
		previous = state.Spec.Replicas
	}
	resource := resourceName(profile.Workload)
	if err := log("info", fmt.Sprintf("Applying load session action %s to profile %s", spec.Action, profile.ID)); err != nil {
		return nil, err
	}
	switch spec.Action {
	case "start":
		if exists {
			return nil, errors.New("load workload appeared after precheck")
		}
		targets, err := resolveRuntimeLoadTargets(ctx, runner, step, cluster, deployment, endpoint, spec)
		if err != nil {
			return nil, err
		}
		manifest, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, targets, deployment.Spec.Namespace)
		if err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--namespace", deployment.Spec.Namespace, "-f", "-"); err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(spec.Replicas)); err != nil {
			return nil, err
		}
		if durationSeconds > 0 {
			if err := waitForReady(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas, log); err != nil {
				return nil, err
			}
			checks := []func() error{}
			if spec.NetworkTimeline {
				if err := log("info", "Synchronizing network phases with the load profile"); err != nil {
					return nil, err
				}
				checks = append(checks, func() error {
					_, err := networkShapers(ctx, runner, cluster.Spec.Kubeconfig, deployment.Metadata.OwnershipMarker, durationSeconds)
					return err
				})
			}
			// Call-chain profiling precedes the warmup; its length (tasks x seconds)
			// comes from the workers' task plan and is part of the shared timeline.
			profilingSeconds := 0
			var plan taskPlan
			if spec.ProfileCallChain {
				plan, err = readTaskPlan(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas)
				if err != nil {
					return nil, fmt.Errorf("read the task plan for call-chain profiling: %w", err)
				}
				taskMix = plan.TaskMix
				profilingSeconds = len(plan.ProfileTasks) * spec.ProfileSecondsPerTask
				// The task count is known only now; fail before sending traffic
				// rather than being killed by the runtime timeout mid-profile.
				if err := checkSessionBudget(spec, durationSeconds, profilingSeconds); err != nil {
					return nil, err
				}
				if len(plan.ProfileTasks) == 0 {
					if err := log("warning", "Call-chain profiling skipped: the scenario has no task with a positive weight"); err != nil {
						return nil, err
					}
				}
			}
			// Every worker (and network shaper) receives the same absolute start, so
			// the recorded window is exactly the profile the processes execute.
			clock, err := armSharedStart(ctx, runner, cluster.Spec.Kubeconfig, deployment, profile, spec.Replicas, profilingSeconds, spec.WarmupSeconds, durationSeconds, spec.NetworkTimeline, plugin.now)
			if err != nil {
				return nil, err
			}
			if profilingSeconds > 0 {
				profiled = &callChainProfile{RPS: spec.ProfileRPS, SecondsPerTask: spec.ProfileSecondsPerTask, Windows: callChainWindows(plan.ProfileTasks, clock.profilingStart, spec.ProfileSecondsPerTask)}
				if err := log("info", fmt.Sprintf("Call-chain profiling: %d tasks x %ds at %g RPS from %s", len(plan.ProfileTasks), spec.ProfileSecondsPerTask, spec.ProfileRPS, clock.profilingStart.Format(time.RFC3339))); err != nil {
					return nil, err
				}
			}
			started := clock.profileStart
			completed := started.Add(time.Duration(durationSeconds) * time.Second)
			startedAt, completedAt = &started, &completed
			if spec.WarmupSeconds > 0 {
				warmup := clock.warmupStart
				warmupStartedAt = &warmup
			}
			if err := log("info", fmt.Sprintf("Shared load start armed: warmup %ds from %s, measured profile %s to %s", spec.WarmupSeconds, clock.warmupStart.Format(time.RFC3339), started.Format(time.RFC3339), completed.Format(time.RFC3339))); err != nil {
				return nil, err
			}
			if clock.clockOffset != 0 {
				if err := log("warning", fmt.Sprintf("Cluster clock differs from the KubePhos host clock by %s; the measured window uses the cluster clock", clock.clockOffset)); err != nil {
					return nil, err
				}
			}
			// completed is a cluster-clock instant; convert the host clock before waiting.
			if err := waitForLoadDuration(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas, completed.Sub(plugin.now().Add(clock.clockOffset)), log, checks...); err != nil {
				return nil, err
			}
			// Idle workers keep serving their final counters until Prometheus scrapes them.
			if drain := drainSeconds(spec); drain > 0 {
				if err := log("info", fmt.Sprintf("Load profile completed; draining %ds so the final metrics are scraped", drain)); err != nil {
					return nil, err
				}
				if err := drainWait(ctx, time.Duration(drain)*time.Second); err != nil {
					return nil, err
				}
			}
			collected, reportedMix, err := collectLocustSummary(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas, started, completed)
			if len(reportedMix) > 0 {
				taskMix = reportedMix
			}
			if err != nil {
				if logErr := log("warning", "Locust statistics summary is unavailable: "+err.Error()); logErr != nil {
					return nil, logErr
				}
			} else {
				summary = collected
			}
			if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", "0"); err != nil {
				return nil, err
			}
		}
	case "stop", "resume":
		if !ownedBy(state, deployment, profile) {
			return nil, errors.New("load workload ownership changed after precheck")
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "annotate", resource, "-n", deployment.Spec.Namespace, previousReplicasKey+"="+fmt.Sprint(previous), "--overwrite"); err != nil {
			return nil, err
		}
		replicas := 0
		if spec.Action == "resume" {
			replicas = spec.Replicas
			// Pods divide the total profile by the worker count baked into the template;
			// keep it equal to the replica count the session resumes with.
			if err := setLoadWorkers(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, resource, replicas); err != nil {
				return nil, err
			}
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(replicas)); err != nil {
			return nil, err
		}
	}
	replicas := spec.Replicas
	stateValue := "running"
	if spec.Action == "start" && durationSeconds > 0 {
		replicas = 0
		stateValue = "completed"
	}
	if spec.Action == "stop" {
		replicas = 0
		stateValue = "stopped"
	}
	value := result{LoadSession: loadSession{
		APIVersion: artifactAPI, Kind: "LoadSession", Metadata: loadSessionMetadata{Name: deployment.Spec.Namespace + "/" + profile.ID, Version: "v1alpha1"},
		Spec: loadSessionSpec{ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace, ScenarioID: profile.ID, Action: spec.Action, State: stateValue, Replicas: replicas, PreviousReplicas: previous, Steps: spec.Steps, GeographicSteps: spec.GeographicSteps, DurationSeconds: durationSeconds, StartedAt: startedAt, CompletedAt: completedAt, Workload: profile.Workload},
	}}
	if startedAt != nil {
		warmup, drain := spec.WarmupSeconds, drainSeconds(spec)
		value.LoadSession.Spec.WarmupSeconds = &warmup
		value.LoadSession.Spec.Workers = spec.Replicas
		value.LoadSession.Spec.WarmupStartedAt = warmupStartedAt
		value.LoadSession.Spec.DrainSeconds = &drain
		value.LoadSession.Spec.Clock = sharedStartClock
		value.LoadSession.Spec.LocustSummary = summary
		value.LoadSession.Spec.TaskMix = taskMix
		value.LoadSession.Spec.CallChainProfile = profiled
	}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, deployment, _, profile, endpoint, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		state, exists, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile.Workload)
		if err != nil {
			return unhealthy(err.Error(), "loadSession", "unknown"), nil
		}
		if spec.Action == "start" {
			if exists {
				return unhealthy("The load workload still exists after reset", "loadSession", "present"), nil
			}
			return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The started load workload was removed", Checks: map[string]string{"loadSession": "absent"}}, nil
		}
		if !exists || !ownedBy(state, deployment, profile) {
			return unhealthy("The load workload is unavailable after reset", "loadSession", "invalid"), nil
		}
		previous, err := storedPreviousReplicas(state)
		if err != nil || state.Spec.Replicas != previous {
			return unhealthy("The previous load intensity was not restored", "loadSession", "invalid"), nil
		}
		if previous == 0 {
			err = waitForStopped(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, log)
		} else {
			err = waitForReady(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, previous, log)
		}
		if err != nil {
			return unhealthy(err.Error(), "loadSession", "unhealthy"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The previous load intensity was restored", Checks: map[string]string{"loadSession": "restored", "replicas": fmt.Sprint(previous)}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	expectedReplicas := spec.Replicas
	expectedState := "running"
	durationSeconds, _ := validateSpecTimeline(spec)
	if spec.Action == "start" && durationSeconds > 0 {
		expectedReplicas = 0
		expectedState = "completed"
	}
	if spec.Action == "stop" {
		expectedReplicas = 0
		expectedState = "stopped"
	}
	if err := validateResult(value.LoadSession, spec, cluster, deployment, profile, expectedState, expectedReplicas); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	state, exists, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile.Workload)
	if err != nil || !exists || !ownedBy(state, deployment, profile) || state.Spec.Replicas != expectedReplicas {
		return unhealthy("Load workload state or ownership does not match the validated transition", "loadSession", "invalid"), nil
	}
	if expectedState == "running" {
		if err := waitForReady(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, expectedReplicas, log); err != nil {
			return unhealthy(err.Error(), "loadSession", "unhealthy"), nil
		}
		if !endpoint.External {
			addresses, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "endpoints", endpoint.Service, "-n", deployment.Spec.Namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
			if err != nil || strings.TrimSpace(addresses) == "" {
				return unhealthy("Load target became unavailable", "targetEndpoint", "unhealthy"), nil
			}
		}
	} else if err := waitForStopped(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, log); err != nil {
		return unhealthy(err.Error(), "loadSession", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: fmt.Sprintf("Load profile %s is %s", profile.ID, expectedState), Checks: map[string]string{"namespace": "owned", "profile": "verified", "state": expectedState, "replicas": fmt.Sprint(expectedReplicas)}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) error {
	spec, cluster, deployment, _, profile, _, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	state, exists, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile.Workload)
	if err != nil || !exists {
		return err
	}
	if !ownedBy(state, deployment, profile) {
		return errors.New("refusing to compensate a load workload without verified ownership")
	}
	resource := resourceName(profile.Workload)
	if spec.Action == "start" {
		if err := log("warning", "Removing the load workload created by the failed start operation"); err != nil {
			return err
		}
		args := []string{"delete", resource, "configmap/" + profile.Workload.Name}
		if spec.NetworkTimeline {
			name := networkClockName(deployment.Metadata.OwnershipMarker)
			args = append(args, "configmap/"+name, "serviceaccount/"+name, "role/"+name, "rolebinding/"+name)
		}
		args = append(args, "-n", deployment.Spec.Namespace, "--ignore-not-found", "--wait=true", "--timeout=5m")
		_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, args...)
		return err
	}
	previous, err := storedPreviousReplicas(state)
	if err != nil {
		return err
	}
	if err := log("warning", fmt.Sprintf("Restoring the previous load intensity %d", previous)); err != nil {
		return err
	}
	if previous > 0 {
		if err := setLoadWorkers(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, resource, previous); err != nil {
			return err
		}
	}
	_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(previous))
	return err
}

// loadWorkers is the number of Locust replicas that share the configured load.
func loadWorkers(replicas int) int {
	if replicas < 1 {
		return 1
	}
	return replicas
}

// setLoadWorkers prepares the template for pods started outside a shared start
// (resume, restore): they split the load across the new replica count and run the
// profile from their own process start, as no instant will be published to them.
func setLoadWorkers(ctx context.Context, runner commandRunner, kubeconfig, namespace, resource string, replicas int) error {
	_, err := runner.Run(ctx, kubeconfig, nil, "set", "env", resource, "-n", namespace, "-c", "locust", fmt.Sprintf("%s=%d", loadWorkersEnv, loadWorkers(replicas)), startClockEnv+"=local")
	return err
}

func storedPreviousReplicas(state workloadState) (int, error) {
	value, ok := state.Metadata.Annotations[previousReplicasKey]
	if !ok {
		return 0, errors.New("previous load intensity is unavailable for compensation")
	}
	previous, err := strconv.Atoi(value)
	if err != nil || previous < 0 || previous > 100 {
		return 0, errors.New("previous load intensity is invalid")
	}
	return previous, nil
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, loadProfileSet, loadProfile, serviceEndpoint, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, err
	}
	spec = withDefaults(spec)
	clusterRaw, clusterOK := step.ResolvedInputs["cluster-connection"]
	deploymentRaw, deploymentOK := step.ResolvedInputs["application-deployment"]
	profilesRaw, profilesOK := step.ResolvedInputs["load-scenario-set"]
	if !clusterOK || !deploymentOK || !profilesOK {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, errors.New("one or more verified load session inputs are unavailable")
	}
	var cluster clusterConnection
	var deployment applicationDeployment
	var profiles loadProfileSet
	if err := json.Unmarshal(clusterRaw.Value, &cluster); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, err
	}
	if err := json.Unmarshal(deploymentRaw.Value, &deployment); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, err
	}
	if err := json.Unmarshal(profilesRaw.Value, &profiles); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, err
	}
	var profile loadProfile
	for _, candidate := range profiles.Spec.Scenarios {
		if candidate.ID == spec.ScenarioID {
			profile = candidate
		}
	}
	if profile.ID == "" {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, fmt.Errorf("load scenario %s is not declared", spec.ScenarioID)
	}
	profile.Workload = scenarioWorkload(profile.ID)
	var endpoint serviceEndpoint
	for _, candidate := range deployment.Spec.Endpoints {
		if candidate.ID == profile.TargetEndpoint {
			endpoint = candidate
		}
	}
	if endpoint.ID == "" {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, fmt.Errorf("load target endpoint %s is unavailable", profile.TargetEndpoint)
	}
	return spec, cluster, deployment, profiles, profile, endpoint, nil
}

func validateArtifacts(cluster clusterConnection, deployment applicationDeployment, profiles loadProfileSet, profile loadProfile, endpoint serviceEndpoint) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Distribution == "" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if err := validateKubeconfig(cluster.Spec.Kubeconfig, cluster.Spec.Server); err != nil {
		return err
	}
	if deployment.APIVersion != artifactAPI || deployment.Kind != "ApplicationDeployment" || deployment.Metadata.Name == "" || deployment.Metadata.Version != "v1alpha1" || deployment.Metadata.OwnershipMarker == "" || deployment.Spec.ApplicationRef == "" || deployment.Spec.ClusterServer != cluster.Spec.Server || deployment.Spec.Namespace != deployment.Metadata.Name {
		return errors.New("application deployment identity is invalid")
	}
	if profiles.APIVersion != artifactAPI || profiles.Kind != "LoadScenarioSet" || profiles.Metadata.ApplicationRef != deployment.Spec.ApplicationRef || len(profiles.Spec.Scenarios) == 0 {
		return errors.New("load scenario set identity is invalid")
	}
	digest, err := digestJSON(profiles.Spec.Scenarios)
	if err != nil || digest != profiles.Metadata.Digest {
		return errors.New("load scenario set digest is invalid")
	}
	if profile.ID == "" || profile.Engine != "locust" || profile.TargetEndpoint != endpoint.ID || profile.RuntimeImage == "" || strings.ContainsAny(profile.RuntimeImage, "\n\r ") || profile.Script == "" || profile.Workload.ID != profile.ID || profile.Workload.APIVersion == "" || profile.Workload.Kind == "" || profile.Workload.Name == "" || len(profile.Workload.Selector) == 0 {
		return errors.New("load scenario interface is invalid")
	}
	seenParameters := map[string]bool{}
	for name, value := range profile.Parameters {
		key := strings.ToUpper(name)
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`).MatchString(name) || len(value) > 4096 || seenParameters[key] {
			return errors.New("load scenario parameters are invalid")
		}
		seenParameters[key] = true
	}
	if endpoint.External {
		target, err := url.Parse(endpoint.URL)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.Fragment != "" || endpoint.Protocol != target.Scheme {
			return errors.New("external load endpoint is invalid")
		}
	} else if endpoint.Service == "" || endpoint.Port < 1 || endpoint.Port > 65535 || (endpoint.Protocol != "http" && endpoint.Protocol != "https" && endpoint.Protocol != "tcp") {
		return errors.New("application load endpoint is invalid")
	}
	scriptDigest := sha256.Sum256([]byte(profile.Script))
	if profile.ScriptDigest != "sha256:"+hex.EncodeToString(scriptDigest[:]) {
		return errors.New("load scenario script digest is invalid")
	}
	return nil
}

func validateKubeconfig(value, server string) error {
	return kubeconfigutil.ValidateClientIdentity(value, server)
}

func verifyNamespace(ctx context.Context, runner commandRunner, kubeconfig string, deployment applicationDeployment) error {
	marker, err := runner.Run(ctx, kubeconfig, nil, "get", "namespace", deployment.Spec.Namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(marker) != deployment.Metadata.OwnershipMarker {
		return errors.New("application namespace ownership does not match its verified deployment")
	}
	return nil
}

func readWorkload(ctx context.Context, runner commandRunner, kubeconfig, namespace string, workload workloadTarget) (workloadState, bool, error) {
	value, err := runner.Run(ctx, kubeconfig, nil, "get", resourceName(workload), "-n", namespace, "--ignore-not-found", "-o", "json")
	if err != nil {
		return workloadState{}, false, err
	}
	if strings.TrimSpace(value) == "" {
		return workloadState{}, false, nil
	}
	var state workloadState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return workloadState{}, false, err
	}
	return state, true, nil
}

func ownedBy(state workloadState, deployment applicationDeployment, profile loadProfile) bool {
	return state.Metadata.Annotations[applicationOwnershipKey] == deployment.Metadata.OwnershipMarker && state.Metadata.Annotations[loadProfileKey] == profile.ID
}

func waitForReady(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int, log plugins.Logger) error {
	result := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, kubeconfig, nil, "rollout", "status", resourceName(profile.Workload), "-n", namespace, "--timeout=10m")
		result <- err
	}()
	health := time.NewTicker(2 * time.Second)
	defer health.Stop()
	diagnostics := time.NewTicker(30 * time.Second)
	defer diagnostics.Stop()
	for {
		select {
		case err := <-result:
			if err != nil {
				return fmt.Errorf("load driver pods did not become ready: %w", err)
			}
			state, exists, err := readWorkload(ctx, runner, kubeconfig, namespace, profile.Workload)
			if err != nil || !exists || state.Status.ReadyReplicas != replicas {
				return errors.New("load driver ready replicas do not match the requested intensity")
			}
			return nil
		case <-health.C:
			if err := inspectLoadPods(ctx, runner, kubeconfig, namespace, profile, replicas, false); err != nil {
				return err
			}
		case <-diagnostics.C:
			pods, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "wide")
			if err != nil {
				if logErr := log("warning", "Load readiness diagnostics failed: "+err.Error()); logErr != nil {
					return logErr
				}
			} else if logErr := log("info", "Load readiness snapshot:\n"+strings.TrimSpace(pods)); logErr != nil {
				return logErr
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func inspectLoadPods(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int, requireReady bool) error {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "json")
	if err != nil {
		return fmt.Errorf("inspect load driver pods: %w", err)
	}
	var pods podListState
	if err := json.Unmarshal([]byte(raw), &pods); err != nil {
		return errors.New("load driver pod status is invalid")
	}
	if requireReady && len(pods.Items) != replicas {
		return errors.New("load driver pod set is incomplete")
	}
	for _, pod := range pods.Items {
		locustFound := false
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name != "locust" {
				continue
			}
			locustFound = true
			if container.RestartCount != 0 || len(container.State.Terminated) != 0 {
				logs, _ := runner.Run(ctx, kubeconfig, nil, "logs", pod.Metadata.Name, "-n", namespace, "-c", "locust", "--tail=80", "--prefix=true")
				if logs != "" {
					return fmt.Errorf("load driver %s became unhealthy:\n%s", pod.Metadata.Name, strings.TrimSpace(logs))
				}
				return fmt.Errorf("load driver %s became unhealthy", pod.Metadata.Name)
			}
			if requireReady && (!container.Ready || len(container.State.Running) == 0) {
				return fmt.Errorf("load driver %s is not ready", pod.Metadata.Name)
			}
		}
		if requireReady && !locustFound {
			return fmt.Errorf("load driver %s has no Locust container status", pod.Metadata.Name)
		}
	}
	return nil
}

func waitForStopped(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, log plugins.Logger) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	diagnostics := time.NewTicker(30 * time.Second)
	defer diagnostics.Stop()
	for {
		pods, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "name")
		if err != nil {
			return err
		}
		if strings.TrimSpace(pods) == "" {
			return nil
		}
		select {
		case <-ticker.C:
		case <-diagnostics.C:
			if err := log("info", "Waiting for load driver pods to terminate: "+strings.ReplaceAll(strings.TrimSpace(pods), "\n", ", ")); err != nil {
				return err
			}
		case <-deadline.C:
			return errors.New("load driver pods remain after scaling to zero")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func validateResult(session loadSession, spec Spec, cluster clusterConnection, deployment applicationDeployment, profile loadProfile, state string, replicas int) error {
	durationSeconds, _ := validateSpecTimeline(spec)
	expectedSteps, _ := json.Marshal(spec.Steps)
	actualSteps, _ := json.Marshal(session.Spec.Steps)
	expectedGeography, _ := json.Marshal(spec.GeographicSteps)
	actualGeography, _ := json.Marshal(session.Spec.GeographicSteps)
	if session.APIVersion != artifactAPI || session.Kind != "LoadSession" || session.Metadata.Name != deployment.Spec.Namespace+"/"+profile.ID || session.Metadata.Version != "v1alpha1" || session.Spec.ApplicationRef != deployment.Spec.ApplicationRef || session.Spec.ClusterServer != cluster.Spec.Server || session.Spec.Namespace != deployment.Spec.Namespace || session.Spec.ScenarioID != profile.ID || session.Spec.Action != spec.Action || session.Spec.State != state || session.Spec.Replicas != replicas || session.Spec.DurationSeconds != durationSeconds || string(actualSteps) != string(expectedSteps) || string(actualGeography) != string(expectedGeography) || session.Spec.Workload.Name != profile.Workload.Name {
		return errors.New("load session artifact does not match the validated transition")
	}
	if state == "completed" && (session.Spec.StartedAt == nil || session.Spec.CompletedAt == nil || session.Spec.CompletedAt.Before(*session.Spec.StartedAt) || session.Spec.CompletedAt.Sub(*session.Spec.StartedAt) < time.Duration(durationSeconds)*time.Second) {
		return errors.New("load session artifact does not contain a complete measurement interval")
	}
	if err := validateRequestMixResult(session, spec); err != nil {
		return err
	}
	if session.Spec.Clock == "" {
		return nil
	}
	// A shared-start window is exact: t=0 and t=end of the profile, after the warmup.
	if session.Spec.Clock != sharedStartClock || state != "completed" || session.Spec.CompletedAt.Sub(*session.Spec.StartedAt) != time.Duration(durationSeconds)*time.Second || session.Spec.WarmupSeconds == nil || *session.Spec.WarmupSeconds != spec.WarmupSeconds || session.Spec.DrainSeconds == nil || *session.Spec.DrainSeconds != drainSeconds(spec) {
		return errors.New("load session artifact does not match the shared measurement window")
	}
	if (spec.WarmupSeconds > 0) != (session.Spec.WarmupStartedAt != nil) || (session.Spec.WarmupStartedAt != nil && session.Spec.StartedAt.Sub(*session.Spec.WarmupStartedAt) != time.Duration(spec.WarmupSeconds)*time.Second) {
		return errors.New("load session artifact does not match the configured warmup")
	}
	if summary := session.Spec.LocustSummary; summary != nil && (summary.MeasuredFrom == nil || summary.MeasuredTo == nil || !summary.MeasuredFrom.Equal(*session.Spec.StartedAt) || !summary.MeasuredTo.Equal(*session.Spec.CompletedAt)) {
		return errors.New("Locust summary does not cover the measured window")
	}
	return nil
}

func resourceName(workload workloadTarget) string {
	return strings.ToLower(workload.Kind) + "/" + workload.Name
}

func selectorValue(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+selector[key])
	}
	return strings.Join(values, ",")
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateLoadTimeline(steps []LoadStep) (int, error) {
	if len(steps) == 0 || len(steps) > 100 {
		return 0, errors.New("add between one and 100 workload steps")
	}
	total := 0
	for index, step := range steps {
		if step.DurationSeconds < 1 || step.DurationSeconds > 86400 {
			return 0, fmt.Errorf("step %d duration must be between one second and 24 hours", index+1)
		}
		total += step.DurationSeconds
		if total > 86400 {
			return 0, errors.New("the complete workload timeline cannot exceed 24 hours")
		}
		switch step.Type {
		case "constant":
			if step.RPS < 0 || step.RPS > 100000 {
				return 0, fmt.Errorf("step %d RPS must be between zero and 100000", index+1)
			}
		case "sinusoidal":
			if step.MinRPS != nil || step.MaxRPS != nil {
				if step.MinRPS == nil || step.MaxRPS == nil {
					return 0, fmt.Errorf("step %d requires both minimum and maximum requests per second", index+1)
				}
				min, max := *step.MinRPS, *step.MaxRPS
				if math.IsNaN(min) || math.IsNaN(max) || math.IsInf(min, 0) || math.IsInf(max, 0) || min < 0 || max <= min || min > 100000 || max > 200000 || (min+max)/2 > 100000 {
					return 0, fmt.Errorf("step %d requires minimum >= 0 and maximum > minimum, within supported request rates", index+1)
				}
			}
			if step.BaselineRPS < 0 || step.BaselineRPS > 100000 || step.AmplitudeRPS < 0 || step.AmplitudeRPS > 100000 || step.PeriodSeconds < 1 || step.PeriodSeconds > 86400 || step.PhaseSeconds < 0 || step.PhaseSeconds > 86400 {
				return 0, fmt.Errorf("step %d has invalid sinusoidal parameters", index+1)
			}
		case "exponential":
			if step.StartRPS < 0 || step.StartRPS > 100000 || step.EndRPS < 0 || step.EndRPS > 100000 || step.Curve < -20 || step.Curve > 20 {
				return 0, fmt.Errorf("step %d has invalid exponential parameters", index+1)
			}
		default:
			return 0, fmt.Errorf("step %d type must be constant, sinusoidal or exponential", index+1)
		}
	}
	return total, nil
}

func validateSpecTimeline(spec Spec) (int, error) {
	if spec.WarmupSeconds < 0 || spec.WarmupSeconds > maxWarmupSeconds {
		return 0, errors.New("warmup must be between 0 and 3600 seconds")
	}
	if spec.DrainSeconds != nil && (*spec.DrainSeconds < 0 || *spec.DrainSeconds > maxDrainSeconds) {
		return 0, errors.New("drain must be between 0 and 600 seconds")
	}
	if spec.NetworkTimeline && (spec.Action != "start" || spec.Interactive || len(spec.Steps) == 0) {
		return 0, errors.New("Network phases require a finite start load profile.")
	}
	if spec.Interactive {
		if len(spec.Steps) > 0 || len(spec.GeographicSteps) > 0 {
			return 0, errors.New("an interactive session cannot also define a finite timeline")
		}
		return 0, nil
	}
	return validateLoadTimeline(spec.Steps)
}

func validateGeographicTimeline(steps []GeographicStep, workloadDuration int) error {
	if len(steps) == 0 {
		return nil
	}
	if len(steps) > 100 {
		return errors.New("add at most 100 geographic steps")
	}
	total := 0
	for index, step := range steps {
		if step.DurationSeconds < 1 || step.DurationSeconds > 86400 {
			return fmt.Errorf("geographic step %d has an invalid duration", index+1)
		}
		total += step.DurationSeconds
		switch step.Type {
		case "constant":
			if err := validateWeights(step.Weights); err != nil {
				return fmt.Errorf("geographic step %d: %w", index+1, err)
			}
		case "linear":
			if err := validateWeights(step.StartWeights); err != nil {
				return fmt.Errorf("geographic step %d start: %w", index+1, err)
			}
			if err := validateWeights(step.EndWeights); err != nil {
				return fmt.Errorf("geographic step %d end: %w", index+1, err)
			}
		default:
			return fmt.Errorf("geographic step %d type must be constant or linear", index+1)
		}
	}
	if total != workloadDuration {
		return fmt.Errorf("geographic timeline duration (%s) must equal workload duration (%s)", (time.Duration(total) * time.Second).String(), (time.Duration(workloadDuration) * time.Second).String())
	}
	return nil
}

func validateWeights(weights map[string]float64) error {
	if len(weights) == 0 {
		return errors.New("zone weights are required")
	}
	total := 0.0
	for zone, weight := range weights {
		if strings.TrimSpace(zone) == "" || weight < 0 || weight > 1000 {
			return errors.New("zone names and weights must be valid")
		}
		total += weight
	}
	if total <= 0 {
		return errors.New("at least one zone needs a positive weight")
	}
	return nil
}

func geographicZones(steps []GeographicStep) map[string]bool {
	zones := map[string]bool{}
	for _, step := range steps {
		for zone := range step.Weights {
			zones[zone] = true
		}
		for zone := range step.StartWeights {
			zones[zone] = true
		}
		for zone := range step.EndWeights {
			zones[zone] = true
		}
	}
	return zones
}

func withDefaults(spec Spec) Spec {
	if spec.MaxUsers == 0 {
		spec.MaxUsers = 1000
	}
	if spec.SpawnRate == 0 {
		spec.SpawnRate = 10
	}
	if len(spec.Steps) == 0 && !spec.Interactive {
		spec.Steps = []LoadStep{{Type: "constant", DurationSeconds: 900, RPS: 30}}
	}
	if spec.DrainSeconds == nil {
		drain := defaultDrainSeconds
		spec.DrainSeconds = &drain
	}
	if spec.ProfileCallChain {
		if spec.ProfileSecondsPerTask == 0 {
			spec.ProfileSecondsPerTask = defaultProfileSecondsPerTask
		}
		if spec.ProfileRPS == 0 {
			spec.ProfileRPS = defaultProfileRPS(spec.Steps)
		}
	}
	return spec
}

func scenarioWorkload(scenarioID string) workloadTarget {
	name := "kubephos-load-" + scenarioID
	if len(name) > 63 {
		digest := sha256.Sum256([]byte(name))
		name = name[:50] + "-" + hex.EncodeToString(digest[:])[:12]
	}
	selector := map[string]string{"app.kubernetes.io/name": "kubephos-load", "kubephos.dev/load-scenario": scenarioID}
	return workloadTarget{ID: scenarioID, APIVersion: "apps/v1", Kind: "Deployment", Name: name, Selector: selector}
}

func resolveLoadTargets(ctx context.Context, runner commandRunner, kubeconfig, namespace string, endpoint serviceEndpoint, spec Spec) ([]loadTarget, error) {
	if endpoint.External {
		endpoints, err := externaldestination.Normalize(endpoint.URL, endpoint.Targets)
		if err != nil {
			return nil, err
		}
		known := map[string]bool{}
		targets := []loadTarget{}
		for _, target := range endpoints {
			known[target.Label] = true
			targets = append(targets, loadTarget{URL: target.URL, Zone: target.Label, Weight: 1})
		}
		for label := range geographicZones(spec.GeographicSteps) {
			if !known[label] {
				return nil, fmt.Errorf("unknown external endpoint %s", label)
			}
		}
		return targets, nil
	}
	path := endpoint.Path
	if path == "" {
		path = "/"
	}
	fallback := []loadTarget{{URL: fmt.Sprintf("%s://%s:%d%s", strings.ToLower(endpoint.Protocol), endpoint.Service, endpoint.Port, path), Weight: 1}}
	if endpoint.Protocol == "tcp" && len(spec.GeographicSteps) > 0 {
		return nil, errors.New("geographic traffic distribution requires an HTTP node proxy and is unavailable for native TCP workloads")
	}
	zones := geographicZones(spec.GeographicSteps)
	if len(zones) == 0 {
		return fallback, nil
	}
	serviceRaw, err := runner.Run(ctx, kubeconfig, nil, "get", "service", endpoint.Service, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("read geographic load endpoint: %w", err)
	}
	var service serviceState
	if err := json.Unmarshal([]byte(serviceRaw), &service); err != nil || len(service.Spec.Ports) == 0 || service.Spec.Ports[0].NodePort == 0 {
		return nil, errors.New("the selected application load endpoint must expose a NodePort for geographic traffic")
	}
	nodesRaw, err := runner.Run(ctx, kubeconfig, nil, "get", "nodes", "-l", "kubephos.dev/role=application", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("read application nodes: %w", err)
	}
	var nodes nodeStateList
	if err := json.Unmarshal([]byte(nodesRaw), &nodes); err != nil {
		return nil, err
	}
	zoneCounts := map[string]int{}
	for _, node := range nodes.Items {
		if zone := node.Metadata.Labels["topology.kubernetes.io/zone"]; zones[zone] {
			zoneCounts[zone]++
		}
	}
	targets := []loadTarget{}
	seenZones := map[string]bool{}
	for _, node := range nodes.Items {
		zone := node.Metadata.Labels["topology.kubernetes.io/zone"]
		if !zones[zone] || zoneCounts[zone] == 0 {
			continue
		}
		address := ""
		for _, candidate := range node.Status.Addresses {
			if candidate.Type == "InternalIP" {
				address = candidate.Address
				break
			}
		}
		if address == "" {
			continue
		}
		seenZones[zone] = true
		targets = append(targets, loadTarget{URL: strings.ToLower(endpoint.Protocol) + "://" + net.JoinHostPort(address, fmt.Sprint(service.Spec.Ports[0].NodePort)) + path, Zone: zone, Weight: 1})
	}
	for zone := range zones {
		if !seenZones[zone] {
			return nil, fmt.Errorf("application zone %s has no reachable node proxy", zone)
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no geographic load target is reachable")
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Zone == targets[right].Zone {
			return targets[left].URL < targets[right].URL
		}
		return targets[left].Zone < targets[right].Zone
	})
	return targets, nil
}

func waitForLoadDuration(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int, duration time.Duration, log plugins.Logger, networkChecks ...func() error) error {
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	diagnostics := time.NewTicker(30 * time.Second)
	defer diagnostics.Stop()
	started := time.Now()
	check := func() error {
		state, exists, err := readWorkload(ctx, runner, kubeconfig, namespace, profile.Workload)
		if err != nil || !exists || state.Spec.Replicas != replicas || state.Status.ReadyReplicas != replicas {
			return errors.New("load driver lost the requested ready replicas")
		}
		if err := inspectLoadPods(ctx, runner, kubeconfig, namespace, profile, replicas, true); err != nil {
			return err
		}
		for _, check := range networkChecks {
			if err := check(); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	for {
		select {
		case <-deadline.C:
			return check()
		case <-ticker.C:
			if err := check(); err != nil {
				return err
			}
		case <-diagnostics.C:
			remaining := duration - time.Since(started)
			if remaining < 0 {
				remaining = 0
			}
			if err := log("info", fmt.Sprintf("Load profile is active; %s remaining", remaining.Round(time.Second))); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func loadManifest(profile loadProfile, marker string, spec Spec, targets []loadTarget, namespaces ...string) ([]byte, error) {
	annotations := map[string]any{applicationOwnershipKey: marker, loadProfileKey: profile.ID}
	labels := map[string]any{"app.kubernetes.io/name": "kubephos-load", "kubephos.dev/load-scenario": profile.ID}
	podAnnotations := map[string]any{applicationOwnershipKey: marker, loadProfileKey: profile.ID, "prometheus.io/scrape": "true", "prometheus.io/path": "/stats/prometheus", "prometheus.io/port": "9646"}
	native := strings.HasPrefix(targets[0].URL, "tcp://")
	if native {
		podAnnotations["sidecar.istio.io/inject"] = "false"
	}
	targetValue, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	stepsValue, err := json.Marshal(spec.Steps)
	if err != nil {
		return nil, err
	}
	geographicValue, err := json.Marshal(spec.GeographicSteps)
	if err != nil {
		return nil, err
	}
	parametersValue, err := json.Marshal(profile.Parameters)
	if err != nil {
		return nil, err
	}
	environment := []any{
		map[string]any{"name": "KUBEPHOS_LOAD_METRICS", "value": "true"},
		map[string]any{"name": "KUBEPHOS_TARGETS", "value": string(targetValue)},
		map[string]any{"name": "KUBEPHOS_MAX_USERS", "value": fmt.Sprint(spec.MaxUsers)},
		map[string]any{"name": "KUBEPHOS_SPAWN_RATE", "value": fmt.Sprint(spec.SpawnRate)},
		map[string]any{"name": loadWorkersEnv, "value": fmt.Sprint(loadWorkers(spec.Replicas))},
		map[string]any{"name": startClockEnv, "value": startClockMode(spec)},
		map[string]any{"name": "KUBEPHOS_WARMUP_SECONDS", "value": fmt.Sprint(spec.WarmupSeconds)},
		map[string]any{"name": "KUBEPHOS_STEPS", "value": string(stepsValue)},
		map[string]any{"name": "KUBEPHOS_GEOGRAPHIC_STEPS", "value": string(geographicValue)},
		map[string]any{"name": "KUBEPHOS_PARAMETERS", "value": string(parametersValue)},
	}
	environment = append(environment, requestMixEnvironment(spec)...)
	parameterNames := make([]string, 0, len(profile.Parameters))
	for name := range profile.Parameters {
		parameterNames = append(parameterNames, name)
	}
	sort.Slice(parameterNames, func(left, right int) bool {
		return strings.ToUpper(parameterNames[left]) < strings.ToUpper(parameterNames[right])
	})
	for _, name := range parameterNames {
		environment = append(environment, map[string]any{"name": "KUBEPHOS_PARAM_" + strings.ToUpper(name), "value": profile.Parameters[name]})
	}
	configMap := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": profile.Workload.Name, "annotations": annotations, "labels": labels},
		"data":       map[string]any{"application.py": profile.Script, "locustfile.py": locustWrapper},
	}
	deployment := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": profile.Workload.Name, "annotations": annotations, "labels": labels},
		"spec": map[string]any{
			"replicas": 0,
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels, "annotations": podAnnotations},
				"spec": map[string]any{
					"nodeSelector": map[string]any{"kubephos.dev/role": "management"},
					"containers": []any{map[string]any{
						"name": "locust", "image": profile.RuntimeImage, "imagePullPolicy": "IfNotPresent",
						"ports":        []any{map[string]any{"name": "load-metrics", "containerPort": 9646}},
						"command":      []any{"locust"},
						"args":         []any{"-f", "/scenario/locustfile.py", "--headless", "--host", targets[0].URL, "--users", fmt.Sprint(spec.MaxUsers), "--spawn-rate", fmt.Sprint(spec.SpawnRate)},
						"env":          environment,
						"volumeMounts": []any{map[string]any{"name": "scenario", "mountPath": "/scenario", "readOnly": true}},
					}},
					"volumes": []any{map[string]any{"name": "scenario", "configMap": map[string]any{"name": profile.Workload.Name}}},
				},
			},
		},
	}
	if spec.NetworkTimeline {
		if len(namespaces) != 1 || namespaces[0] == "" {
			return nil, errors.New("Network clock requires the load namespace.")
		}
		pod := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		pod["serviceAccountName"] = networkClockName(marker)
		pod["automountServiceAccountToken"] = true
		environment = append(environment, map[string]any{"name": "KUBEPHOS_NETWORK_CLOCK", "value": networkClockName(marker)}, map[string]any{"name": "KUBEPHOS_NETWORK_NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]string{"fieldPath": "metadata.namespace"}}})
		pod["containers"].([]any)[0].(map[string]any)["env"] = environment
	}
	configValue, err := yaml.Marshal(configMap)
	if err != nil {
		return nil, err
	}
	deploymentValue, err := yaml.Marshal(deployment)
	if err != nil {
		return nil, err
	}
	result := append(append(configValue, []byte("---\n")...), deploymentValue...)
	if spec.NetworkTimeline {
		for _, resource := range networkTimelineResources(marker, namespaces[0]) {
			value, err := yaml.Marshal(resource)
			if err != nil {
				return nil, err
			}
			result = append(append(result, []byte("---\n")...), value...)
		}
	}
	return result, nil
}

const locustWrapper = `import fractions
import functools
import importlib.util
import json
import math
import os
import random
import time
from urllib.parse import urljoin, urlsplit
import gevent
from gevent.lock import Semaphore
from locust import LoadTestShape, TaskSet, events
from locust.clients import HttpSession
from locust.exception import RescheduleTaskImmediately
from locust.contrib.fasthttp import FastHttpSession
from locust.user import User

module_spec = importlib.util.spec_from_file_location("kubephos_application", "/scenario/application.py")
module = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(module)
targets = json.loads(os.environ["KUBEPHOS_TARGETS"])
steps = json.loads(os.environ["KUBEPHOS_STEPS"])
geographic_steps = json.loads(os.environ["KUBEPHOS_GEOGRAPHIC_STEPS"])
max_users = int(os.environ["KUBEPHOS_MAX_USERS"])
spawn_rate = float(os.environ["KUBEPHOS_SPAWN_RATE"])

def configured_load_workers(value):
    # Every Deployment replica is an independent headless Locust process, so the
    # configured profile is the total offered load and each worker paces its share.
    try:
        return max(1, int(value))
    except (TypeError, ValueError):
        return 1

def configured_seconds(value):
    try:
        seconds = float(value)
    except (TypeError, ValueError):
        return 0.0
    return seconds if math.isfinite(seconds) and seconds > 0 else 0.0

load_workers = configured_load_workers(os.environ.get("KUBEPHOS_LOAD_WORKERS", "1"))
warmup_seconds = configured_seconds(os.environ.get("KUBEPHOS_WARMUP_SECONDS", "0"))
start_clock = os.environ.get("KUBEPHOS_START_CLOCK", "local")
start_file = "/tmp/kubephos-start"
started_at = time.monotonic()
shared_start = None

def profile_elapsed():
    # Seconds since t=0 of the measured profile; negative during the warmup. With
    # the shared clock every worker idles (None) until KubePhos publishes the Unix
    # second of t=0, chosen after all workers are ready, so they start together.
    global shared_start
    if start_clock != "shared":
        return time.monotonic() - started_at - warmup_seconds
    if shared_start is None:
        try:
            with open(start_file) as source:
                value = source.read().strip()
            shared_start = float(value) if value else None
        except (OSError, ValueError):
            return None
        if shared_start is None:
            return None
    return time.time() - shared_start

request_lock = Semaphore()
next_request_at = None

def no_wait(_self):
    return 0.0

user_classes = []
for name, value in vars(module).items():
    if not name.startswith("_") and isinstance(value, type) and issubclass(value, User) and value.abstract is False:
        value.wait_time = no_wait
        globals()[name] = value
        user_classes.append(value)
del name, value

# Request mix: Locust stores the weighted tasks of every User and TaskSet class as
# a list repeating each task "weight" times. KubePhos rebuilds those lists with
# the configured weights, wraps every task function to count its executions and,
# while profiling the call chain, narrows every list to a single task.
max_task_entries = 10000
task_plan_file = "/tmp/kubephos-task-plan.json"
task_classes = {}
task_class_order = []
task_executions = {}
current_tasks = {}
task_mode = {"target": None}
task_plan = {"taskMix": [], "profileTasks": []}

def parse_task_weights(value):
    try:
        raw = json.loads(value) if value else {}
    except ValueError:
        print("KubePhos: ignoring invalid task weights", flush=True)
        return {}
    if not isinstance(raw, dict):
        return {}
    weights = {}
    for name, weight in raw.items():
        if isinstance(weight, bool) or not isinstance(weight, (int, float)):
            continue
        weight = float(weight)
        if math.isfinite(weight) and weight >= 0:
            weights[str(name)] = weight
    return weights

def task_name(task):
    return getattr(task, "kubephos_task", None) or getattr(task, "__name__", None) or repr(task)

def is_task_set(task):
    return isinstance(task, type) and issubclass(task, TaskSet)

def distinct_tasks(tasks):
    # (task, repetitions) in order of first appearance: the Locust weights.
    order, counts = [], {}
    for task in tasks:
        if id(task) not in counts:
            counts[id(task)] = 0
            order.append(task)
        counts[id(task)] += 1
    return [(task, counts[id(task)]) for task in order]

def integer_weights(weights):
    # Locust weights are repetitions: fractional weights (percentages) are scaled
    # to at most max_task_entries entries, every positive weight keeps at least one
    # entry and the result is reduced by the common divisor, preserving ratios.
    positive = [weight for weight in weights if weight > 0]
    if not positive:
        return [0 for _ in weights]
    if all(float(weight).is_integer() for weight in positive) and sum(positive) <= max_task_entries:
        return [int(weight) for weight in weights]
    # Decimal percentages (33.3/66.7, 0.5/0.25) have an exact small common scale.
    exact = [fractions.Fraction(weight).limit_denominator(1000) for weight in weights]
    scale = 1
    for value in exact:
        scale = scale * value.denominator // math.gcd(scale, value.denominator)
    if all(abs(float(value) - weight) <= 1e-9 * max(1.0, weight) for value, weight in zip(exact, weights)):
        values = [int(value * scale) for value in exact]
        divisor = 0
        for value in values:
            divisor = math.gcd(divisor, value)
        values = [value // divisor for value in values]
        if sum(values) <= max_task_entries:
            return values
    factor = max(len(positive), max_task_entries - len(positive)) / sum(positive)
    values = [max(1, int(round(weight * factor))) if weight > 0 else 0 for weight in weights]
    divisor = 0
    for value in values:
        divisor = math.gcd(divisor, value)
    return [value // divisor for value in values]

def counted_task(task):
    name = task_name(task)

    @functools.wraps(task)
    def run(*args, **kwargs):
        task_executions[name] = task_executions.get(name, 0) + 1
        key = id(gevent.getcurrent())
        previous = current_tasks.get(key)
        current_tasks[key] = name
        try:
            return task(*args, **kwargs)
        finally:
            if previous is None:
                current_tasks.pop(key, None)
            else:
                current_tasks[key] = previous
    run.kubephos_task = name
    return run

def kubephos_idle(parent):
    # A class that cannot reach the profiled task: a nested TaskSet returns to its
    # parent, a user waits without sending requests.
    if isinstance(parent, TaskSet):
        parent.interrupt(reschedule=True)
    gevent.sleep(0.5)

def apply_tasks(target):
    # None restores the configured mix; a task name narrows every class to it.
    task_mode["target"] = target
    for cls in task_class_order:
        entries = task_classes[cls]
        if not entries:
            continue
        if target is None:
            cls.tasks = [task for task, _, integer in entries for _ in range(integer)]
            continue
        chosen = [task for task, _, integer in entries if integer > 0 and (reaches_task(task, target, (cls, task)) if is_task_set(task) else task_name(task) == target)]
        cls.tasks = chosen or [kubephos_idle]

def reaches_task(cls, target, path):
    for task, _, integer in task_classes.get(cls) or []:
        if integer <= 0:
            continue
        if is_task_set(task):
            if task not in path and reaches_task(task, target, path + (task,)):
                return True
        elif task_name(task) == target:
            return True
    return False

def leaf_mix(roots):
    # Share of task executions of every task function, through nested TaskSets and
    # across User classes (by their weight). TaskSet entries are paths, not tasks.
    shares, weights, order = {}, {}, []

    def walk(cls, share, path):
        entries = task_classes.get(cls) or []
        if not entries:
            return
        total = sum(integer for _, _, integer in entries)
        if total <= 0:
            raise RuntimeError("KubePhos task weights leave %s without tasks" % getattr(cls, "__name__", cls))
        for task, weight, integer in entries:
            if integer <= 0:
                continue
            part = share * integer / total
            if is_task_set(task):
                if task not in path:
                    walk(task, part, path + (task,))
                continue
            name = task_name(task)
            if name not in shares:
                order.append(name)
                shares[name], weights[name] = 0.0, weight
            shares[name] += part

    roots = [cls for cls in roots if task_classes.get(cls)]
    user_weights = [max(0.0, float(getattr(cls, "weight", 1) or 0)) for cls in roots]
    total_weight = sum(user_weights)
    for cls, weight in zip(roots, user_weights):
        if weight > 0:
            walk(cls, weight / total_weight, (cls,))
    total = sum(shares.values())
    if total <= 0:
        raise RuntimeError("KubePhos task weights leave the scenario without tasks")
    return [{"task": name, "weight": weights[name], "share": shares[name] / total} for name in order]

def configure_task_mix(roots, overrides):
    wrappers = {}
    known = set()

    def visit(cls):
        if cls in task_classes:
            return
        original = distinct_tasks(list(getattr(cls, "tasks", None) or []))
        names = [task_name(task) for task, _ in original]
        known.update(names)
        configured = [float(overrides.get(name, count)) for (_, count), name in zip(original, names)]
        changed = any(name in overrides for name in names)
        integers = integer_weights(configured) if changed else [count for _, count in original]
        entries = []
        for (task, _), weight, integer in zip(original, configured, integers):
            if not is_task_set(task):
                if id(task) not in wrappers:
                    wrappers[id(task)] = counted_task(task)
                task = wrappers[id(task)]
                task_executions.setdefault(task_name(task), 0)
            entries.append((task, weight, integer))
        task_classes[cls] = entries
        task_class_order.append(cls)
        for task, _ in original:
            if is_task_set(task):
                visit(task)

    for cls in roots:
        visit(cls)
    unknown = sorted(set(overrides) - known)
    if unknown:
        print("KubePhos: ignoring task weights of unknown tasks: " + ", ".join(unknown), flush=True)
    apply_tasks(None)
    task_plan["taskMix"] = leaf_mix(roots)
    task_plan["profileTasks"] = [entry["task"] for entry in task_plan["taskMix"] if entry["share"] > 0]
    return task_plan

def profiling_task_at(elapsed):
    # Call-chain profiling runs before the warmup: one window per task, in order.
    if not profile_tasks or elapsed is None or elapsed >= -warmup_seconds:
        return None
    offset = elapsed + warmup_seconds + len(profile_tasks) * profile_seconds_per_task
    if offset < 0:
        return None
    return profile_tasks[min(len(profile_tasks) - 1, int(offset // profile_seconds_per_task))]

def sync_task_mode(elapsed):
    global next_request_at
    target = profiling_task_at(elapsed)
    if target != task_mode["target"]:
        apply_tasks(target)
        if target is not None:
            # Slots reserved by the previous task's users are discarded when they
            # abort; the new task's users reserve from now, so no second is lost.
            next_request_at = None
    return target

def check_task_mode():
    # Before each request: a task started in an earlier profiling window stops
    # instead of sending its remaining requests into another task's window.
    target = sync_task_mode(profile_elapsed())
    if target is None:
        return
    current = current_tasks.get(id(gevent.getcurrent()))
    if current is not None and current != target:
        raise RescheduleTaskImmediately()

def write_task_plan(path):
    try:
        with open(path + ".tmp", "w") as target:
            json.dump({"taskMix": task_plan["taskMix"], "profileTasks": profile_tasks}, target)
        os.replace(path + ".tmp", path)
    except OSError as error:
        print("KubePhos: cannot write the task plan: %s" % error, flush=True)

profile_call_chain = os.environ.get("KUBEPHOS_PROFILE_CALL_CHAIN") == "true" and start_clock == "shared"
profile_seconds_per_task = configured_seconds(os.environ.get("KUBEPHOS_PROFILE_SECONDS_PER_TASK", "60"))
profile_rps = configured_seconds(os.environ.get("KUBEPHOS_PROFILE_RPS", "0"))
configure_task_mix(user_classes, parse_task_weights(os.environ.get("KUBEPHOS_TASK_WEIGHTS", "")))
profile_tasks = list(task_plan["profileTasks"]) if profile_call_chain and profile_seconds_per_task > 0 and profile_rps > 0 else []
write_task_plan(task_plan_file)

def duration(items):
    return sum(float(item["durationSeconds"]) for item in items)

def active_step(items, elapsed):
    cursor = 0.0
    for item in items:
        item_duration = float(item["durationSeconds"])
        if elapsed < cursor + item_duration:
            return item, elapsed - cursor
        cursor += item_duration
    return (items[-1], float(items[-1]["durationSeconds"])) if items else (None, 0.0)

def step_number(step, key):
    # The Go encoder omits zero-valued numeric fields, so a missing field means 0.
    value = step.get(key)
    return 0.0 if value is None else float(value)

def rps_at(elapsed):
    step, local_time = active_step(steps, elapsed)
    if step is None:
        return 0.0
    kind = step["type"]
    if kind == "constant":
        return max(0.0, step_number(step, "rps"))
    if kind == "sinusoidal":
        period = max(step_number(step, "periodSeconds"), 1.0)
        phase = step_number(step, "phaseSeconds")
        if "minRps" in step and "maxRps" in step:
            baseline = (step_number(step, "minRps") + step_number(step, "maxRps")) / 2.0
            amplitude = (step_number(step, "maxRps") - step_number(step, "minRps")) / 2.0
        else:
            baseline = step_number(step, "baselineRps")
            amplitude = step_number(step, "amplitudeRps")
        return max(0.0, baseline + amplitude * math.sin((2.0 * math.pi * (local_time + phase)) / period))
    start = step_number(step, "startRps")
    end = step_number(step, "endRps")
    progress = min(max(local_time / max(step_number(step, "durationSeconds"), 1.0), 0.0), 1.0)
    # A missing curve is 0 (linear), matching the UI preview and the metrics collector.
    curve = step_number(step, "curve")
    shaped = progress if abs(curve) < 1e-9 else (math.exp(curve * progress) - 1.0) / (math.exp(curve) - 1.0)
    return max(0.0, start + (end - start) * shaped)

def worker_rps_at(elapsed):
    # The warmup offers the profile's initial rate; nothing is sent before it or
    # after the profile ends (drain).
    phase = load_phase(elapsed)
    if phase == 3:
        return profile_rps / load_workers
    if phase == 0:
        return rps_at(0.0) / load_workers
    if phase == 1:
        return rps_at(elapsed) / load_workers
    return 0.0

# Open-loop sizing (Little's law): a user without think time completes one
# request per latency period, so rate x latency users are busy at any instant.
# With only ceil(rate) users the generator falls behind its schedule as soon as
# the mean latency exceeds one second, and the system under test is offered
# less load exactly when it saturates (coordinated omission). Users therefore
# follow the observed latency with headroom; ceil(rate) remains the floor.
user_headroom = 1.5
latency_window = {"sum": 0.0, "count": 0, "estimate": 0.0, "updated_at": None}

def observe_latency(request_type=None, name=None, response_time=None, exception=None, **kwargs):
    if response_time is None or not math.isfinite(response_time) or response_time < 0:
        return
    latency_window["sum"] += response_time / 1000.0
    latency_window["count"] += 1

def latency_estimate():
    # Mean latency (seconds) of the requests completed since the previous update,
    # at most once per second. It rises at once, so users are added before the
    # schedule slips, and decays slowly, so the user count does not oscillate.
    now = time.monotonic()
    updated_at = latency_window["updated_at"]
    if updated_at is None or now - updated_at >= 1.0:
        latency_window["updated_at"] = now
        if latency_window["count"]:
            mean = latency_window["sum"] / latency_window["count"]
            estimate = latency_window["estimate"]
            latency_window["estimate"] = mean if mean >= estimate else 0.8 * estimate + 0.2 * mean
            latency_window["sum"] = 0.0
            latency_window["count"] = 0
    return latency_window["estimate"]

def worker_users_at(elapsed):
    rate = worker_rps_at(elapsed)
    if rate <= 0:
        return 0
    requested = math.ceil(rate * max(1.0, user_headroom * latency_estimate()) - 1e-9)
    return min(max_users, max(1, requested))

def load_phase(elapsed):
    # kubephos_load_phase: 3 call-chain profiling (before the warmup), 0 warmup,
    # 1 measured profile, 2 drain; None while idle.
    if elapsed is None:
        return None
    if elapsed < -warmup_seconds:
        return 3 if profile_tasks and elapsed >= -warmup_seconds - len(profile_tasks) * profile_seconds_per_task else None
    if elapsed < 0:
        return 0
    if elapsed <= duration(steps):
        return 1
    return 2

def choose_weighted(items):
    return random.choices(items, weights=[float(item.get("weight", 1.0)) for item in items], k=1)[0]

def zone_weights_at(elapsed, zones):
    step, local_time = active_step(geographic_steps, elapsed)
    if step is None:
        return {zone: 1.0 for zone in zones}
    if step["type"] == "constant":
        return {zone: float(step.get("weights", {}).get(zone, 0.0)) for zone in zones}
    progress = min(max(local_time / max(float(step["durationSeconds"]), 1.0), 0.0), 1.0)
    start = step.get("startWeights", {})
    end = step.get("endWeights", {})
    return {zone: float(start.get(zone, 0.0)) + (float(end.get(zone, 0.0)) - float(start.get(zone, 0.0))) * progress for zone in zones}

def choose_target():
    by_zone = {}
    for target in targets:
        by_zone.setdefault(target.get("zone", "default"), []).append(target)
    if not geographic_steps:
        return choose_weighted(targets)["url"]
    # The warmup uses the geographic distribution of the profile's t=0.
    elapsed = max(0.0, profile_elapsed() or 0.0)
    zones = sorted(by_zone)
    weights = zone_weights_at(elapsed, zones)
    candidates = [{"zone": zone, "weight": weights[zone]} for zone in zones if weights[zone] > 0]
    selected_zone = choose_weighted(candidates)["zone"]
    return random.choice(by_zone[selected_zone])["url"]

def request_url(url):
    if urlsplit(str(url)).scheme:
        return url
    return urljoin(choose_target().rstrip("/") + "/", str(url).lstrip("/"))

def request_path(url):
    parts = urlsplit(str(url))
    return (parts.path or "/") + ("?" + parts.query if parts.query else "")

def wait_for_request_slot():
    global next_request_at
    while True:
        elapsed = profile_elapsed()
        if elapsed is None:
            gevent.sleep(0.1)
            continue
        target_rps = worker_rps_at(elapsed)
        if target_rps <= 0:
            with request_lock:
                next_request_at = None
            gevent.sleep(0.1)
            continue
        now = time.monotonic()
        # Slots are spaced 1/rate apart and never reset to "now" while users are
        # queued: resetting a queued schedule starts a new burst and overshoots
        # the target. Reservations are limited to a short horizon so a later rate
        # change applies quickly; users beyond it wait and retry without a slot.
        horizon = max(1.0, 2.0 / target_rps)
        with request_lock:
            scheduled_at = now if next_request_at is None else next_request_at
            slot_at = max(now, scheduled_at)
            reserved = slot_at <= now + horizon
            if reserved:
                next_request_at = slot_at + (1.0 / target_rps)
        if not reserved:
            gevent.sleep(max(0.01, min(0.25, slot_at - horizon - now)))
            continue
        # A slot that is already in the past was missed because every user was busy:
        # the closed-loop generator fell behind the open-loop schedule.
        record_schedule_lag(now - scheduled_at, target_rps)
        if slot_at > now:
            gevent.sleep(slot_at - now)
            # A slot reserved ahead can fall after the profile ended (or in a
            # zero-rate step): send nothing then, so the drain carries no traffic.
            elapsed = profile_elapsed()
            if elapsed is None or worker_rps_at(elapsed) <= 0:
                continue
        return

# Coordinated-omission evidence: lag = issue time - scheduled slot time, cumulative
# for Prometheus and since the start of the measured profile for the summary.
schedule_stats = {"count": 0, "lag_sum": 0.0, "late": 0, "max_lag": 0.0}
measured_schedule = {"count": 0, "lag_sum": 0.0, "late": 0, "max_lag": 0.0}

def late_threshold(target_rps):
    return max(0.05, 0.5 / target_rps)

def record_schedule_lag(lag, target_rps):
    lag = max(0.0, lag)
    late = lag > late_threshold(target_rps)
    for stats in (schedule_stats, measured_schedule):
        stats["count"] += 1
        stats["lag_sum"] += lag
        stats["max_lag"] = max(stats["max_lag"], lag)
        if late:
            stats["late"] += 1

original_http_request = HttpSession.request
original_fast_http_request = FastHttpSession.request

def paced_http_request(self, *args, **kwargs):
    check_task_mode()
    wait_for_request_slot()
    # The slot can fall in the next profiling window (reservations reach ahead).
    check_task_mode()
    if "url" in kwargs:
        kwargs["url"] = request_url(kwargs["url"])
    elif len(args) >= 2:
        args = (args[0], request_url(args[1]), *args[2:])
    return original_http_request(self, *args, **kwargs)

def paced_fast_http_request(self, *args, **kwargs):
    check_task_mode()
    wait_for_request_slot()
    # The slot can fall in the next profiling window (reservations reach ahead).
    check_task_mode()
    if "url" in kwargs:
        original = kwargs["url"]
        kwargs["url"] = request_url(original)
        rewritten = kwargs["url"]
    elif len(args) >= 2:
        original = args[1]
        args = (args[0], request_url(args[1]), *args[2:])
        rewritten = args[1]
    else:
        original = rewritten = None
    # FastHttpSession names requests by the URL it receives; keep the path, like
    # HttpSession, so endpoints are not split by the chosen target host.
    if rewritten is not None and rewritten != original and len(args) < 3 and not kwargs.get("name") and not getattr(self, "request_name", None):
        kwargs["name"] = request_path(rewritten)
    return original_fast_http_request(self, *args, **kwargs)

module.kubephos_wait_for_request_slot = wait_for_request_slot

HttpSession.request = paced_http_request
FastHttpSession.request = paced_fast_http_request

# Measurement window: Locust statistics are reset at the profile's t=0 so the
# summary covers only the measured profile, not the warmup.
measurement = {"environment": None, "phase": None, "measured_from": None, "cpu_max": 0.0, "cpu_warnings": 0, "cpu_warning_emitted": False}
cpu_warning_event = getattr(events, "cpu_warning", None)

def locust_runner():
    return getattr(measurement["environment"], "runner", None)

def begin_measurement(elapsed):
    measurement["measured_from"] = shared_start if shared_start is not None else time.time() - elapsed
    for key in measured_schedule:
        measured_schedule[key] = 0.0 if key in ("lag_sum", "max_lag") else 0
    measurement["cpu_max"] = 0.0
    measurement["cpu_warnings"] = 0
    runner = locust_runner()
    if runner is not None:
        measurement["cpu_warning_emitted"] = bool(getattr(runner, "cpu_warning_emitted", False))
        runner.stats.reset_all()

def sample_cpu():
    runner = locust_runner()
    if runner is None:
        return
    usage = getattr(runner, "current_cpu_usage", None)
    if isinstance(usage, (int, float)) and math.isfinite(usage):
        measurement["cpu_max"] = max(measurement["cpu_max"], float(usage))
    # Without the cpu_warning event only Locust's one-shot flag is available.
    if cpu_warning_event is None and getattr(runner, "cpu_warning_emitted", False) and not measurement["cpu_warning_emitted"]:
        measurement["cpu_warning_emitted"] = True
        measurement["cpu_warnings"] += 1

def record_cpu_warning(cpu_usage=None, **kwargs):
    if measurement["phase"] == 1:
        measurement["cpu_warnings"] += 1
        if isinstance(cpu_usage, (int, float)) and math.isfinite(cpu_usage):
            measurement["cpu_max"] = max(measurement["cpu_max"], float(cpu_usage))

def update_phase(elapsed):
    phase = load_phase(elapsed)
    sync_task_mode(elapsed)
    if phase == 1 and measurement["measured_from"] is None:
        begin_measurement(elapsed)
    measurement["phase"] = phase
    if phase == 1:
        sample_cpu()
    return phase

def stats_entry_summary(entry):
    minimum = getattr(entry, "min_response_time", None)
    return {
        "method": entry.method or "",
        "name": entry.name or "",
        "numRequests": int(entry.num_requests),
        "numNoneRequests": int(getattr(entry, "num_none_requests", 0) or 0),
        "numFailures": int(entry.num_failures),
        "totalResponseTime": float(entry.total_response_time or 0),
        "minResponseTime": None if minimum is None else float(minimum),
        "maxResponseTime": float(entry.max_response_time or 0),
        "responseTimes": {str(key): int(count) for key, count in (entry.response_times or {}).items()},
    }

def error_text(error):
    parse = getattr(type(error), "parse_error", None)
    try:
        return str(parse(error.error)) if parse is not None else str(error.error)
    except Exception:
        return str(error.error)

def locust_summary():
    runner = locust_runner()
    stats = getattr(runner, "stats", None)
    return {
        "phase": measurement["phase"],
        "measuredFrom": measurement["measured_from"],
        "entries": [stats_entry_summary(entry) for entry in stats.entries.values()] if stats is not None else [],
        "total": stats_entry_summary(stats.total) if stats is not None else None,
        "errors": [{"method": error.method or "", "name": error.name or "", "error": error_text(error), "occurrences": int(error.occurrences)} for error in stats.errors.values()] if stats is not None else [],
        "cpu": {"maxPercent": measurement["cpu_max"], "warnings": measurement["cpu_warnings"]},
        "schedule": {"count": measured_schedule["count"], "lagSum": measured_schedule["lag_sum"], "late": measured_schedule["late"], "maxLag": measured_schedule["max_lag"]},
        "taskMix": task_plan["taskMix"],
        "taskExecutions": dict(task_executions),
    }

def next_phase_delay(elapsed):
    # Wake exactly at every phase and profiling-window transition.
    if elapsed is None:
        return 0.2
    boundaries = [0.0, -warmup_seconds] + [-warmup_seconds - profile_seconds_per_task * index for index in range(1, len(profile_tasks) + 1)]
    upcoming = [boundary - elapsed for boundary in boundaries if boundary > elapsed]
    return max(0.001, min([1.0] + upcoming))

def watch_phases():
    while True:
        elapsed = profile_elapsed()
        update_phase(elapsed)
        gevent.sleep(next_phase_delay(elapsed))

@events.init.add_listener
def start_measurement(environment, **kwargs):
    measurement["environment"] = environment
    if cpu_warning_event is not None:
        cpu_warning_event.add_listener(record_cpu_warning)
    gevent.spawn(watch_phases)

buckets = (0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000)
max_metric_names = 50
metrics = {}
metric_names = set()

def metric_counts():
    return {"success": 0, "failure": 0, "sum": 0.0, "count": 0, "buckets": [0] * len(buckets)}

def metric_name(name):
    # Bounded cardinality: after 50 distinct request names the rest are "other".
    name = "" if name is None else str(name)
    if name in metric_names:
        return name
    if len(metric_names) < max_metric_names:
        metric_names.add(name)
        return name
    return "other"

def label_value(value):
    # Prometheus text format escaping of label values.
    return str(value).replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n")

def record_request(request_type, name, response_time, exception, **kwargs):
    operation = name if request_type == "redis" else request_type
    counts = metrics.setdefault((str(operation), metric_name(name)), metric_counts())
    counts["failure" if exception is not None else "success"] += 1
    if response_time is None or not math.isfinite(response_time) or response_time < 0:
        return
    counts["sum"] += response_time
    counts["count"] += 1
    for i, bound in enumerate(buckets):
        if response_time <= bound:
            counts["buckets"][i] += 1

def seed_metrics(redis_operation):
    # Seed both outcomes before traffic starts, including all-success runs.
    if redis_operation == "mixed":
        operations = ("GET", "SET")
    elif redis_operation:
        operations = (redis_operation,)
    else:
        operations = ("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS")
    for operation in operations:
        metrics[(operation, "")] = metric_counts()

def prometheus_number(value):
    return repr(float(value))

def prometheus_exposition():
    series = sorted(metrics.items())
    labels = ['operation="%s",name="%s"' % (label_value(operation), label_value(name)) for (operation, name), _ in series]
    lines = ["# TYPE kubephos_load_requests_total counter"]
    for label, (_, counts) in zip(labels, series):
        for outcome in ("success", "failure"):
            lines.append('kubephos_load_requests_total{%s,outcome="%s"} %d' % (label, outcome, counts[outcome]))
    lines.append("# TYPE kubephos_load_duration_milliseconds histogram")
    for label, (_, counts) in zip(labels, series):
        for bound, count in zip(buckets, counts["buckets"]):
            lines.append('kubephos_load_duration_milliseconds_bucket{%s,le="%s"} %d' % (label, bound, count))
        lines.append('kubephos_load_duration_milliseconds_bucket{%s,le="+Inf"} %d' % (label, counts["count"]))
        lines.append('kubephos_load_duration_milliseconds_count{%s} %d' % (label, counts["count"]))
        lines.append('kubephos_load_duration_milliseconds_sum{%s} %s' % (label, prometheus_number(counts["sum"])))
    elapsed = profile_elapsed()
    phase = load_phase(elapsed)
    runner = locust_runner()
    lines.append("# TYPE kubephos_load_target_rps gauge")
    lines.append("kubephos_load_target_rps %s" % prometheus_number(0.0 if elapsed is None else worker_rps_at(elapsed)))
    lines.append("# TYPE kubephos_load_users gauge")
    lines.append("kubephos_load_users %d" % int(getattr(runner, "user_count", 0) or 0))
    lines.append("# TYPE kubephos_load_schedule_lag_seconds summary")
    lines.append("kubephos_load_schedule_lag_seconds_sum %s" % prometheus_number(schedule_stats["lag_sum"]))
    lines.append("kubephos_load_schedule_lag_seconds_count %d" % schedule_stats["count"])
    lines.append("# TYPE kubephos_load_late_requests_total counter")
    lines.append("kubephos_load_late_requests_total %d" % schedule_stats["late"])
    lines.append("# TYPE kubephos_load_phase gauge")
    if phase is not None:
        lines.append("kubephos_load_phase %d" % phase)
    if task_executions:
        lines.append("# TYPE kubephos_load_task_executions_total counter")
        for task in sorted(task_executions):
            lines.append('kubephos_load_task_executions_total{task="%s"} %d' % (label_value(task), task_executions[task]))
    if profile_tasks:
        target = profiling_task_at(elapsed)
        lines.append("# TYPE kubephos_load_profiling_task gauge")
        for task in profile_tasks:
            lines.append('kubephos_load_profiling_task{task="%s"} %d' % (label_value(task), 1 if task == target else 0))
    return "\n".join(lines) + "\n"

def serve_stats(environ, start_response):
    path = environ.get("PATH_INFO")
    if path == "/stats/prometheus":
        body, content_type = prometheus_exposition(), "text/plain; version=0.0.4"
    elif path == "/stats/summary":
        body, content_type = json.dumps(locust_summary()), "application/json"
    else:
        start_response("404 Not Found", [])
        return [b""]
    start_response("200 OK", [("Content-Type", content_type)])
    return [body.encode()]

events.request.add_listener(observe_latency)

if os.environ.get("KUBEPHOS_LOAD_METRICS") == "true":
    from gevent.pywsgi import WSGIServer
    metrics_server = None
    events.request.add_listener(record_request)

    @events.init.add_listener
    def start_metrics(**kwargs):
        global metrics_server
        seed_metrics(getattr(module, "CONFIG", {}).get("operation"))
        metrics_server = WSGIServer(("0.0.0.0", 9646), serve_stats, log=None)
        metrics_server.start()

class KubePhosLoadShape(LoadTestShape):
    def tick(self):
        elapsed = profile_elapsed()
        if load_phase(elapsed) not in (0, 1, 3):
            return 0, spawn_rate
        return worker_users_at(elapsed), spawn_rate
`

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return localRunner{}
}

func (plugin Plugin) now() time.Time {
	if plugin.Clock != nil {
		return plugin.Clock()
	}
	return time.Now()
}

type localRunner struct{}

func (localRunner) Run(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	output, err := kubeconfigutil.RunKubectl(ctx, kubeconfig, stdin, args...)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 1200 {
			message = message[:1200]
		}
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return string(output), nil
}
