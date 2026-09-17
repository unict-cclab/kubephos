package loadsession

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID                = "io.kubephos.load.kubernetes.session"
	artifactAPI             = "artifacts.kubephos.dev/v1alpha1"
	ownershipKey            = "kubephos.dev/ownership-marker"
	applicationOwnershipKey = "kubephos.dev/application-marker"
	loadProfileKey          = "kubephos.dev/load-scenario"
	previousReplicasKey     = "kubephos.dev/load-previous-replicas"
)

type Plugin struct {
	Runner commandRunner
	Clock  func() time.Time
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
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
}

type LoadStep struct {
	Type            string  `json:"type"`
	DurationSeconds int     `json:"durationSeconds"`
	RPS             float64 `json:"rps,omitempty"`
	BaselineRPS     float64 `json:"baselineRps,omitempty"`
	AmplitudeRPS    float64 `json:"amplitudeRps,omitempty"`
	PeriodSeconds   int     `json:"periodSeconds,omitempty"`
	PhaseSeconds    int     `json:"phaseSeconds,omitempty"`
	StartRPS        float64 `json:"startRps,omitempty"`
	EndRPS          float64 `json:"endRps,omitempty"`
	Curve           float64 `json:"curve,omitempty"`
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
	ID        string `json:"id"`
	Component string `json:"component"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Path      string `json:"path,omitempty"`
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
	ID             string         `json:"id"`
	Engine         string         `json:"engine"`
	TargetEndpoint string         `json:"targetEndpoint"`
	RuntimeImage   string         `json:"runtimeImage"`
	Script         string         `json:"script"`
	ScriptDigest   string         `json:"scriptDigest"`
	Workload       workloadTarget `json:"-"`
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
	StartedAt        *time.Time       `json:"startedAt,omitempty"`
	CompletedAt      *time.Time       `json:"completedAt,omitempty"`
	Workload         workloadTarget   `json:"workload"`
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
		ID: pluginID, Name: "Interactive Kubernetes load session", Version: "0.6.1",
		Description:     "Runs an ordered RPS and geographic timeline using the immutable Locust journey declared by an application.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","loadScenarioSetRef","scenarioId","action","replicas","maxUsers","spawnRate"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"loadScenarioSetRef":{"type":"string","title":"Load scenarios","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadScenarioSet","x-kubephos-artifact-version":"v1alpha1"},"scenarioId":{"type":"string","title":"Load scenario","description":"User behavior implemented by the Locust file supplied by the selected application.","minLength":1,"maxLength":63,"default":"storefront-journey"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["start","stop","resume"],"default":"start"},"interactive":{"type":"boolean","title":"Interactive session","default":false},"replicas":{"type":"integer","title":"Load workers","minimum":1,"maximum":100,"default":1},"maxUsers":{"type":"integer","title":"Maximum concurrent users per worker","minimum":1,"maximum":100000,"default":1000},"spawnRate":{"type":"number","title":"Users started per second","minimum":0.1,"maximum":100000,"default":10},"steps":{"type":"array","title":"Temporal workload timeline","maxItems":100,"items":{"type":"object"}},"geographicSteps":{"type":"array","title":"Geographic traffic timeline","maxItems":100,"items":{"type":"object"}}}}`),
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
	duration, err := validateSpecTimeline(spec)
	if err != nil {
		return invalid(report, "steps", err.Error())
	}
	if err := validateGeographicTimeline(spec.GeographicSteps, duration); err != nil {
		return invalid(report, "geographicSteps", err.Error())
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
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "control-load-session", Name: strings.ToUpper(spec.Action[:1]) + spec.Action[1:] + " declared load profile", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", ArtifactID: spec.LoadScenarioSetRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "load-session", Type: "LoadSession", Version: "v1alpha1", MediaType: "application/json", Source: "/loadSession"}},
	}}}, nil
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
		targets, err := resolveLoadTargets(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, endpoint, spec)
		if err != nil {
			return unhealthy(err.Error(), "geographicDistribution", "invalid"), nil
		}
		manifest, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, targets)
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
	if spec.Action != "stop" {
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
	var startedAt, completedAt *time.Time
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
		targets, err := resolveLoadTargets(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, endpoint, spec)
		if err != nil {
			return nil, err
		}
		manifest, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, targets)
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
			started := plugin.now().UTC()
			startedAt = &started
			if err := waitForLoadDuration(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas, time.Duration(durationSeconds)*time.Second, log); err != nil {
				return nil, err
			}
			if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", "0"); err != nil {
				return nil, err
			}
			completed := plugin.now().UTC()
			completedAt = &completed
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
		addresses, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "endpoints", endpoint.Service, "-n", deployment.Spec.Namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
		if err != nil || strings.TrimSpace(addresses) == "" {
			return unhealthy("Load target became unavailable", "targetEndpoint", "unhealthy"), nil
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
		_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", resource, "configmap/"+profile.Workload.Name, "-n", deployment.Spec.Namespace, "--ignore-not-found", "--wait=true", "--timeout=5m")
		return err
	}
	previous, err := storedPreviousReplicas(state)
	if err != nil {
		return err
	}
	if err := log("warning", fmt.Sprintf("Restoring the previous load intensity %d", previous)); err != nil {
		return err
	}
	_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(previous))
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
	scriptDigest := sha256.Sum256([]byte(profile.Script))
	if profile.ScriptDigest != "sha256:"+hex.EncodeToString(scriptDigest[:]) {
		return errors.New("load scenario script digest is invalid")
	}
	return nil
}

func validateKubeconfig(value, server string) error {
	var config struct {
		APIVersion     string `yaml:"apiVersion"`
		Kind           string `yaml:"kind"`
		CurrentContext string `yaml:"current-context"`
		Clusters       []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				Certificate string `yaml:"client-certificate-data"`
				Key         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(value), &config); err != nil || config.APIVersion != "v1" || config.Kind != "Config" || config.CurrentContext == "" {
		return errors.New("cluster kubeconfig is invalid")
	}
	var clusterName, userName string
	for _, item := range config.Contexts {
		if item.Name == config.CurrentContext {
			clusterName, userName = item.Context.Cluster, item.Context.User
		}
	}
	clusterOK := false
	for _, item := range config.Clusters {
		clusterOK = clusterOK || item.Name == clusterName && item.Cluster.Server == server
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name == userName {
			certificate, certificateErr := base64.StdEncoding.DecodeString(item.User.Certificate)
			key, keyErr := base64.StdEncoding.DecodeString(item.User.Key)
			userOK = certificateErr == nil && keyErr == nil && len(certificate) > 0 && len(key) > 0
		}
	}
	if !clusterOK || !userOK {
		return errors.New("cluster kubeconfig target or client identity is invalid")
	}
	return nil
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
	path := endpoint.Path
	if path == "" {
		path = "/"
	}
	fallback := []loadTarget{{URL: fmt.Sprintf("%s://%s:%d%s", strings.ToLower(endpoint.Protocol), endpoint.Service, endpoint.Port, path), Weight: 1}}
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

func waitForLoadDuration(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int, duration time.Duration, log plugins.Logger) error {
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
		return inspectLoadPods(ctx, runner, kubeconfig, namespace, profile, replicas, true)
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

func loadManifest(profile loadProfile, marker string, spec Spec, targets []loadTarget) ([]byte, error) {
	annotations := map[string]any{applicationOwnershipKey: marker, loadProfileKey: profile.ID}
	labels := map[string]any{"app.kubernetes.io/name": "kubephos-load", "kubephos.dev/load-scenario": profile.ID}
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
				"metadata": map[string]any{"labels": labels, "annotations": annotations},
				"spec": map[string]any{
					"nodeSelector": map[string]any{"kubephos.dev/role": "management"},
					"containers": []any{map[string]any{
						"name": "locust", "image": profile.RuntimeImage, "imagePullPolicy": "IfNotPresent",
						"command": []any{"locust"},
						"args":    []any{"-f", "/scenario/locustfile.py", "--headless", "--host", targets[0].URL, "--users", fmt.Sprint(spec.MaxUsers), "--spawn-rate", fmt.Sprint(spec.SpawnRate)},
						"env": []any{
							map[string]any{"name": "KUBEPHOS_TARGETS", "value": string(targetValue)},
							map[string]any{"name": "KUBEPHOS_MAX_USERS", "value": fmt.Sprint(spec.MaxUsers)},
							map[string]any{"name": "KUBEPHOS_SPAWN_RATE", "value": fmt.Sprint(spec.SpawnRate)},
							map[string]any{"name": "KUBEPHOS_STEPS", "value": string(stepsValue)},
							map[string]any{"name": "KUBEPHOS_GEOGRAPHIC_STEPS", "value": string(geographicValue)},
						},
						"volumeMounts": []any{map[string]any{"name": "scenario", "mountPath": "/scenario", "readOnly": true}},
						"resources":    map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]any{"cpu": "1", "memory": "512Mi"}},
					}},
					"volumes": []any{map[string]any{"name": "scenario", "configMap": map[string]any{"name": profile.Workload.Name}}},
				},
			},
		},
	}
	configValue, err := yaml.Marshal(configMap)
	if err != nil {
		return nil, err
	}
	deploymentValue, err := yaml.Marshal(deployment)
	if err != nil {
		return nil, err
	}
	return append(append(configValue, []byte("---\n")...), deploymentValue...), nil
}

const locustWrapper = `import importlib.util
import json
import math
import os
import random
import time
from urllib.parse import urljoin, urlsplit
import gevent
from gevent.lock import Semaphore
from locust import LoadTestShape
from locust.clients import HttpSession
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
started_at = time.monotonic()
request_lock = Semaphore()
next_request_at = time.monotonic()

def no_wait(_self):
    return 0.0

for name, value in vars(module).items():
    if not name.startswith("_") and isinstance(value, type) and issubclass(value, User) and value.abstract is False:
        value.wait_time = no_wait
        globals()[name] = value
del name, value

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

def rps_at(elapsed):
    step, local_time = active_step(steps, elapsed)
    if step is None:
        return 0.0
    kind = step["type"]
    if kind == "constant":
        return max(0.0, float(step["rps"]))
    if kind == "sinusoidal":
        period = float(step["periodSeconds"])
        phase = float(step.get("phaseSeconds", 0))
        return max(0.0, float(step["baselineRps"]) + float(step["amplitudeRps"]) * math.sin((2.0 * math.pi * (local_time + phase)) / period))
    start = float(step["startRps"])
    end = float(step["endRps"])
    progress = min(max(local_time / max(float(step["durationSeconds"]), 1.0), 0.0), 1.0)
    curve = float(step.get("curve", 3.0))
    shaped = progress if abs(curve) < 1e-9 else (math.exp(curve * progress) - 1.0) / (math.exp(curve) - 1.0)
    return max(0.0, start + (end - start) * shaped)

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
    elapsed = time.monotonic() - started_at
    zones = sorted(zone for zone in by_zone if zone != "default")
    weights = zone_weights_at(elapsed, zones)
    candidates = [{"zone": zone, "weight": weights[zone]} for zone in zones if weights[zone] > 0]
    selected_zone = choose_weighted(candidates)["zone"]
    return random.choice(by_zone[selected_zone])["url"]

def request_url(url):
    if urlsplit(str(url)).scheme:
        return url
    return urljoin(choose_target().rstrip("/") + "/", str(url).lstrip("/"))

def wait_for_request_slot():
    global next_request_at
    while True:
        target_rps = rps_at(time.monotonic() - started_at)
        if target_rps <= 0:
            with request_lock:
                next_request_at = time.monotonic()
            gevent.sleep(0.1)
            continue
        now = time.monotonic()
        with request_lock:
            if next_request_at > now + max(1.0, 2.0 / target_rps):
                next_request_at = now
            slot_at = max(now, next_request_at)
            next_request_at = slot_at + (1.0 / target_rps)
        if slot_at > now:
            gevent.sleep(slot_at - now)
        return

original_http_request = HttpSession.request
original_fast_http_request = FastHttpSession.request

def paced_http_request(self, *args, **kwargs):
    wait_for_request_slot()
    if "url" in kwargs:
        kwargs["url"] = request_url(kwargs["url"])
    elif len(args) >= 2:
        args = (args[0], request_url(args[1]), *args[2:])
    return original_http_request(self, *args, **kwargs)

def paced_fast_http_request(self, *args, **kwargs):
    wait_for_request_slot()
    if "url" in kwargs:
        kwargs["url"] = request_url(kwargs["url"])
    elif len(args) >= 2:
        args = (args[0], request_url(args[1]), *args[2:])
    return original_fast_http_request(self, *args, **kwargs)

HttpSession.request = paced_http_request
FastHttpSession.request = paced_fast_http_request

class KubePhosLoadShape(LoadTestShape):
    def tick(self):
        elapsed = self.get_run_time()
        if elapsed > duration(steps):
            return 0, spawn_rate
        requested = math.ceil(rps_at(elapsed))
        return min(max_users, max(1, requested)) if requested > 0 else 0, spawn_rate
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
	directory, err := os.MkdirTemp("", "kubephos-cluster-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	path := directory + "/kubeconfig"
	if err := os.WriteFile(path, []byte(kubeconfig), 0600); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.CombinedOutput()
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
