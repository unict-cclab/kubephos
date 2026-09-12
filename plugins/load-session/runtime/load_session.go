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
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string         `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string         `json:"applicationDeploymentRef"`
	LoadScenarioSetRef       string         `json:"loadScenarioSetRef"`
	ScenarioID               string         `json:"scenarioId"`
	Action                   string         `json:"action"`
	Replicas                 int            `json:"replicas"`
	Users                    int            `json:"users,omitempty"`
	SpawnRate                int            `json:"spawnRate,omitempty"`
	Pattern                  string         `json:"pattern,omitempty"`
	DurationSeconds          int            `json:"durationSeconds,omitempty"`
	ZoneWeights              map[string]int `json:"zoneWeights,omitempty"`
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
	ApplicationRef   string         `json:"applicationRef"`
	ClusterServer    string         `json:"clusterServer"`
	Namespace        string         `json:"namespace"`
	ScenarioID       string         `json:"scenarioId"`
	Action           string         `json:"action"`
	State            string         `json:"state"`
	Replicas         int            `json:"replicas"`
	PreviousReplicas int            `json:"previousReplicas"`
	Pattern          string         `json:"pattern"`
	DurationSeconds  int            `json:"durationSeconds"`
	ZoneWeights      map[string]int `json:"zoneWeights,omitempty"`
	Workload         workloadTarget `json:"workload"`
}

type loadTarget struct {
	URL    string `json:"url"`
	Zone   string `json:"zone,omitempty"`
	Weight int    `json:"weight"`
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

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Interactive Kubernetes load session", Version: "0.3.0",
		Description:     "Starts, stops or resumes a generic Locust runtime using the immutable scenario declared by an application.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","loadScenarioSetRef","scenarioId","action","replicas","users","spawnRate","pattern","durationSeconds"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"loadScenarioSetRef":{"type":"string","title":"Load scenarios","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadScenarioSet","x-kubephos-artifact-version":"v1alpha1"},"scenarioId":{"type":"string","title":"Application journey","description":"Immutable Locust scenario supplied by the selected application.","minLength":1,"maxLength":63,"default":"storefront-journey"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["start","stop","resume"],"default":"start"},"replicas":{"type":"integer","title":"Load workers","minimum":1,"maximum":100,"default":1},"users":{"type":"integer","title":"Concurrent users per worker","minimum":1,"maximum":100000,"default":10},"spawnRate":{"type":"integer","title":"Users started per second","minimum":1,"maximum":100000,"default":1},"pattern":{"type":"string","title":"Temporal profile","enum":["constant","ramp","steps"],"default":"constant"},"durationSeconds":{"type":"integer","title":"Duration in seconds","description":"Use zero for an interactive session that runs until stopped.","minimum":0,"maximum":7200,"default":900},"zoneWeights":{"type":"object","title":"Geographic distribution","additionalProperties":true}}}`),
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
	if spec.Users != 0 && (spec.Users < 1 || spec.Users > 100000) {
		return invalid(report, "users", "Concurrent users must be between 1 and 100000.")
	}
	if spec.SpawnRate != 0 && (spec.SpawnRate < 1 || spec.SpawnRate > 100000) {
		return invalid(report, "spawnRate", "Spawn rate must be between 1 and 100000.")
	}
	if spec.Pattern != "" && spec.Pattern != "constant" && spec.Pattern != "ramp" && spec.Pattern != "steps" {
		return invalid(report, "pattern", "Temporal profile must be constant, ramp or steps.")
	}
	if spec.DurationSeconds < 0 || spec.DurationSeconds > 7200 {
		return invalid(report, "durationSeconds", "Duration must be between 0 and 7200 seconds.")
	}
	for zone, weight := range spec.ZoneWeights {
		if strings.TrimSpace(zone) == "" || weight < 0 || weight > 1000 {
			return invalid(report, "zoneWeights", "Zone weights must contain valid zone names and values between 0 and 1000.")
		}
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will revalidate the cluster, application ownership, load profile, current session state and target endpoint before changing load."})
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
		if spec.DurationSeconds > 0 {
			if err := waitForReady(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, profile, spec.Replicas, log); err != nil {
				return nil, err
			}
			if err := waitForLoadDuration(ctx, time.Duration(spec.DurationSeconds)*time.Second, log); err != nil {
				return nil, err
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
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(replicas)); err != nil {
			return nil, err
		}
	}
	replicas := spec.Replicas
	stateValue := "running"
	if spec.Action == "start" && spec.DurationSeconds > 0 {
		replicas = 0
		stateValue = "completed"
	}
	if spec.Action == "stop" {
		replicas = 0
		stateValue = "stopped"
	}
	value := result{LoadSession: loadSession{
		APIVersion: artifactAPI, Kind: "LoadSession", Metadata: loadSessionMetadata{Name: deployment.Spec.Namespace + "/" + profile.ID, Version: "v1alpha1"},
		Spec: loadSessionSpec{ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace, ScenarioID: profile.ID, Action: spec.Action, State: stateValue, Replicas: replicas, PreviousReplicas: previous, Pattern: spec.Pattern, DurationSeconds: spec.DurationSeconds, ZoneWeights: spec.ZoneWeights, Workload: profile.Workload},
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
	if spec.Action == "start" && spec.DurationSeconds > 0 {
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
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
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
		case <-ticker.C:
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
	if session.APIVersion != artifactAPI || session.Kind != "LoadSession" || session.Metadata.Name != deployment.Spec.Namespace+"/"+profile.ID || session.Metadata.Version != "v1alpha1" || session.Spec.ApplicationRef != deployment.Spec.ApplicationRef || session.Spec.ClusterServer != cluster.Spec.Server || session.Spec.Namespace != deployment.Spec.Namespace || session.Spec.ScenarioID != profile.ID || session.Spec.Action != spec.Action || session.Spec.State != state || session.Spec.Replicas != replicas || session.Spec.Pattern != spec.Pattern || session.Spec.DurationSeconds != spec.DurationSeconds || session.Spec.Workload.Name != profile.Workload.Name {
		return errors.New("load session artifact does not match the validated transition")
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

func withDefaults(spec Spec) Spec {
	if spec.Users == 0 {
		spec.Users = 10
	}
	if spec.SpawnRate == 0 {
		spec.SpawnRate = 1
	}
	if spec.Pattern == "" {
		spec.Pattern = "constant"
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
	if len(spec.ZoneWeights) == 0 {
		return fallback, nil
	}
	total := 0
	for _, weight := range spec.ZoneWeights {
		total += weight
	}
	if total == 0 {
		return nil, errors.New("at least one application zone must have a positive traffic weight")
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
		if zone := node.Metadata.Labels["topology.kubernetes.io/zone"]; spec.ZoneWeights[zone] > 0 {
			zoneCounts[zone]++
		}
	}
	targets := []loadTarget{}
	seenZones := map[string]bool{}
	for _, node := range nodes.Items {
		zone := node.Metadata.Labels["topology.kubernetes.io/zone"]
		weight := spec.ZoneWeights[zone]
		if weight <= 0 || zoneCounts[zone] == 0 {
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
		targets = append(targets, loadTarget{URL: strings.ToLower(endpoint.Protocol) + "://" + net.JoinHostPort(address, fmt.Sprint(service.Spec.Ports[0].NodePort)) + path, Zone: zone, Weight: weight})
	}
	for zone, weight := range spec.ZoneWeights {
		if weight > 0 && !seenZones[zone] {
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

func waitForLoadDuration(ctx context.Context, duration time.Duration, log plugins.Logger) error {
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	for {
		select {
		case <-deadline.C:
			return nil
		case <-ticker.C:
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
						"args":    []any{"-f", "/scenario/locustfile.py", "--headless", "--users", fmt.Sprint(spec.Users), "--spawn-rate", fmt.Sprint(spec.SpawnRate)},
						"env": []any{
							map[string]any{"name": "KUBEPHOS_TARGETS", "value": string(targetValue)},
							map[string]any{"name": "KUBEPHOS_USERS", "value": fmt.Sprint(spec.Users)},
							map[string]any{"name": "KUBEPHOS_SPAWN_RATE", "value": fmt.Sprint(spec.SpawnRate)},
							map[string]any{"name": "KUBEPHOS_PATTERN", "value": spec.Pattern},
							map[string]any{"name": "KUBEPHOS_DURATION", "value": fmt.Sprint(spec.DurationSeconds)},
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
from locust import HttpUser, LoadTestShape

module_spec = importlib.util.spec_from_file_location("kubephos_application", "/scenario/application.py")
module = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(module)
targets = json.loads(os.environ["KUBEPHOS_TARGETS"])
users = int(os.environ["KUBEPHOS_USERS"])
spawn_rate = int(os.environ["KUBEPHOS_SPAWN_RATE"])
pattern = os.environ["KUBEPHOS_PATTERN"]
duration = int(os.environ["KUBEPHOS_DURATION"])

def choose_target():
    zones = {}
    for target in targets:
        zones.setdefault(target.get("zone", "default"), []).append(target)
    weighted_zones = [(zone, values[0].get("weight", 1)) for zone, values in zones.items()]
    zone = random.choices([item[0] for item in weighted_zones], weights=[item[1] for item in weighted_zones], k=1)[0]
    return random.choice(zones[zone])["url"]

def wrap_initializer(initializer):
    def initialize(instance, environment):
        instance.host = choose_target()
        initializer(instance, environment)
    return initialize

for name, value in vars(module).items():
    if isinstance(value, type) and issubclass(value, HttpUser) and value is not HttpUser:
        value.__init__ = wrap_initializer(value.__init__)
        globals()[name] = value

class KubePhosLoadShape(LoadTestShape):
    def tick(self):
        elapsed = self.get_run_time()
        progress = min(elapsed / duration, 1.0) if duration > 0 else 1.0
        if pattern == "ramp":
            target = max(1, math.ceil(users * progress))
        elif pattern == "steps":
            target = max(1, math.ceil(users * min(math.floor(progress * 4) + 1, 4) / 4))
        else:
            target = users
        return target, spawn_rate
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
