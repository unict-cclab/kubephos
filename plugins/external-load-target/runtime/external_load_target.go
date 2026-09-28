package externalloadtarget

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID     = "io.kubephos.load.external.target"
	artifactAPI  = "artifacts.kubephos.dev/v1alpha1"
	ownershipKey = "kubephos.dev/ownership-marker"
	scenarioID   = "external-load"
)

var parameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type Plugin struct{ Runner commandRunner }

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef string            `json:"clusterConnectionRef"`
	TargetURL            string            `json:"targetUrl"`
	RuntimeImage         string            `json:"runtimeImage"`
	Script               string            `json:"script"`
	Parameters           map[string]string `json:"parameters,omitempty"`
}

type stepInput struct {
	Spec
	Marker    string `json:"marker"`
	Namespace string `json:"namespace"`
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

type workloadTarget struct {
	ID         string            `json:"id"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Selector   map[string]string `json:"selector"`
	Traits     []string          `json:"traits"`
}

type serviceEndpoint struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	URL      string `json:"url"`
	External bool   `json:"external"`
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
		External       bool              `json:"external"`
		ClusterServer  string            `json:"clusterServer"`
		Namespace      string            `json:"namespace"`
		ManifestDigest string            `json:"manifestDigest"`
		Workloads      []workloadTarget  `json:"workloads"`
		Endpoints      []serviceEndpoint `json:"endpoints"`
	} `json:"spec"`
}

type loadScenario struct {
	ID             string            `json:"id"`
	Engine         string            `json:"engine"`
	TargetEndpoint string            `json:"targetEndpoint"`
	RuntimeImage   string            `json:"runtimeImage"`
	Script         string            `json:"script"`
	ScriptDigest   string            `json:"scriptDigest"`
	Parameters     map[string]string `json:"parameters,omitempty"`
}

type loadScenarioSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ApplicationRef string `json:"applicationRef"`
		Digest         string `json:"digest"`
	} `json:"metadata"`
	Spec struct {
		Scenarios []loadScenario `json:"scenarios"`
	} `json:"spec"`
}

type result struct {
	ApplicationDeployment applicationDeployment `json:"applicationDeployment"`
	LoadScenarioSet       loadScenarioSet       `json:"loadScenarioSet"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "External load destination", Version: "0.1.0",
		Description:     "Prepares an isolated Locust driver namespace and a verified external HTTP destination.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","targetUrl","runtimeImage","script"],"properties":{"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"targetUrl":{"type":"string","title":"External endpoint","minLength":1,"maxLength":2048},"runtimeImage":{"type":"string","title":"Locust runtime image","default":"locustio/locust:2.42.6","minLength":1,"maxLength":512},"script":{"type":"string","title":"Locust file template","format":"multiline","x-kubephos-multiline":true,"minLength":1,"maxLength":262144},"parameters":{"type":"object","title":"Locust parameters","additionalProperties":true}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "LoadScenarioSet", Version: "v1alpha1"}},
		Capabilities:    []string{"loads.external.prepare", "loads.external.preflight", "loads.external.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"cluster.admin"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if json.Unmarshal(invocation.Input, &spec) != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	if !strings.HasPrefix(spec.ClusterConnectionRef, "art_") {
		return invalid(report, "clusterConnectionRef", "Select a verified cluster connection.")
	}
	if err := validateSpec(spec); err != nil {
		return invalid(report, "externalDestination", err.Error())
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will create an isolated, service-mesh-enabled namespace for the external Locust driver."})
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
	if err := validateSpec(spec); err != nil {
		return domain.Plan{}, err
	}
	marker := domain.RuntimeExecutionIDToken
	encoded, err := json.Marshal(stepInput{Spec: spec, Marker: marker, Namespace: "kubephos-load-" + marker})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "prepare-external-load", Name: "Prepare external load destination", Input: encoded, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}},
		Outputs: []domain.ArtifactOutput{
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", MediaType: "application/json", Source: "/applicationDeployment"},
			{Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", MediaType: "application/json", Source: "/loadScenarioSet"},
		},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := log("info", "Validating cluster access and isolated external load namespace"); err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	allowed, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(allowed) != "yes" {
		return unhealthy("The cluster connection does not have administrative permissions", "authorization", "denied"), nil
	}
	marker, err := namespaceMarker(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace)
	if step.Cleanup {
		if err != nil {
			return unhealthy(err.Error(), "namespace", "unknown"), nil
		}
		if marker != "" && marker != input.Marker {
			return unhealthy("The load namespace is owned by another execution", "ownership", "blocked"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "External load namespace is safe to remove"}, nil
	}
	if err != nil {
		return unhealthy(err.Error(), "namespace", "unknown"), nil
	}
	if marker != "" {
		return unhealthy("The generated external load namespace already exists", "namespace", "conflict"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "External load destination is valid and ready", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "namespace": "available"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Creating the isolated namespace for the external Locust driver"); err != nil {
		return nil, err
	}
	manifest, _ := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]any{"name": input.Namespace, "annotations": map[string]string{ownershipKey: input.Marker}, "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos", "istio-injection": "enabled", "kubephos.dev/monitored": "true", "kubephos.dev/application": input.Namespace}},
	})
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("create external load namespace: %w", err)
	}
	value, err := buildResult(input, cluster)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	marker, err := namespaceMarker(ctx, plugin.runner(), cluster.Spec.Kubeconfig, input.Namespace)
	if step.Cleanup {
		if err != nil {
			return domain.HealthReport{}, err
		}
		if marker != "" {
			return unhealthy("External load namespace remains after cleanup", "namespace", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "External load namespace was removed"}, nil
	}
	if err != nil || marker != input.Marker {
		return unhealthy("External load namespace ownership could not be verified", "ownership", "invalid"), nil
	}
	var actual result
	if err := json.Unmarshal(raw, &actual); err != nil {
		return domain.HealthReport{}, err
	}
	expected, err := buildResult(input, cluster)
	if err != nil {
		return domain.HealthReport{}, err
	}
	a, _ := json.Marshal(actual)
	e, _ := json.Marshal(expected)
	if !bytes.Equal(a, e) {
		return unhealthy("External destination artifacts do not match the validated configuration", "artifacts", "invalid"), nil
	}
	if err := log("info", "Verified external target, Locust template and isolated namespace"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "External load destination is prepared", Checks: map[string]string{"namespace": input.Namespace, "target": "verified", "scenario": scenarioID}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, err := resolve(step)
	if err != nil {
		return err
	}
	marker, err := namespaceMarker(ctx, plugin.runner(), cluster.Spec.Kubeconfig, input.Namespace)
	if err != nil || marker == "" {
		return err
	}
	if marker != input.Marker {
		return errors.New("refusing to remove an external load namespace owned by another execution")
	}
	if err := log("warning", "Removing the isolated external load namespace"); err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", input.Namespace, "--wait=true", "--timeout=5m")
	return err
}

func buildResult(input stepInput, cluster clusterConnection) (result, error) {
	normalized, err := normalizedSpec(input.Spec)
	if err != nil {
		return result{}, err
	}
	scriptDigest := digest([]byte(normalized.Script))
	profile := loadScenario{ID: scenarioID, Engine: "locust", TargetEndpoint: "external-destination", RuntimeImage: normalized.RuntimeImage, Script: normalized.Script, ScriptDigest: scriptDigest, Parameters: normalized.Parameters}
	profiles := []loadScenario{profile}
	profilesRaw, _ := json.Marshal(profiles)
	identityRaw, _ := json.Marshal(struct {
		TargetURL    string            `json:"targetUrl"`
		RuntimeImage string            `json:"runtimeImage"`
		Script       string            `json:"script"`
		Parameters   map[string]string `json:"parameters,omitempty"`
	}{TargetURL: normalized.TargetURL, RuntimeImage: normalized.RuntimeImage, Script: normalized.Script, Parameters: normalized.Parameters})
	applicationRef := "external:" + strings.TrimPrefix(digest(identityRaw), "sha256:")
	workloadName := "kubephos-load-" + scenarioID
	selector := map[string]string{"app.kubernetes.io/name": "kubephos-load", "kubephos.dev/load-scenario": scenarioID}
	parsed, _ := url.Parse(normalized.TargetURL)
	value := result{}
	value.ApplicationDeployment.APIVersion = artifactAPI
	value.ApplicationDeployment.Kind = "ApplicationDeployment"
	value.ApplicationDeployment.Metadata.Name = input.Namespace
	value.ApplicationDeployment.Metadata.Version = "v1alpha1"
	value.ApplicationDeployment.Metadata.OwnershipMarker = input.Marker
	value.ApplicationDeployment.Spec.ApplicationRef = applicationRef
	value.ApplicationDeployment.Spec.External = true
	value.ApplicationDeployment.Spec.ClusterServer = cluster.Spec.Server
	value.ApplicationDeployment.Spec.Namespace = input.Namespace
	value.ApplicationDeployment.Spec.ManifestDigest = digest(identityRaw)
	value.ApplicationDeployment.Spec.Workloads = []workloadTarget{{ID: scenarioID, APIVersion: "apps/v1", Kind: "Deployment", Name: workloadName, Selector: selector, Traits: []string{"traffic-entrypoint"}}}
	value.ApplicationDeployment.Spec.Endpoints = []serviceEndpoint{{ID: "external-destination", Protocol: parsed.Scheme, URL: normalized.TargetURL, External: true}}
	value.LoadScenarioSet.APIVersion = artifactAPI
	value.LoadScenarioSet.Kind = "LoadScenarioSet"
	value.LoadScenarioSet.Metadata.ApplicationRef = applicationRef
	value.LoadScenarioSet.Metadata.Digest = digest(profilesRaw)
	value.LoadScenarioSet.Spec.Scenarios = profiles
	return value, nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, err
	}
	if err := validateSpec(input.Spec); err != nil || input.Marker == "" || input.Namespace == "" {
		if err == nil {
			err = errors.New("runtime identity is missing")
		}
		return stepInput{}, clusterConnection{}, err
	}
	raw, ok := step.ResolvedInputs["cluster-connection"]
	if !ok {
		return stepInput{}, clusterConnection{}, errors.New("verified cluster connection is unavailable")
	}
	var cluster clusterConnection
	if err := json.Unmarshal(raw.Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, err
	}
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Server == "" || cluster.Spec.Kubeconfig == "" {
		return stepInput{}, clusterConnection{}, errors.New("cluster connection identity is invalid")
	}
	return input, cluster, nil
}

func validateSpec(spec Spec) error {
	_, err := normalizedSpec(spec)
	return err
}

func normalizedSpec(spec Spec) (Spec, error) {
	spec.TargetURL = strings.TrimSpace(spec.TargetURL)
	spec.RuntimeImage = strings.TrimSpace(spec.RuntimeImage)
	spec.Script = strings.TrimSpace(spec.Script)
	if len(spec.TargetURL) > 2048 {
		return Spec{}, errors.New("external endpoint is too long")
	}
	target, err := url.Parse(spec.TargetURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.Fragment != "" {
		return Spec{}, errors.New("external endpoint must be an HTTP or HTTPS URL without credentials or fragment")
	}
	if spec.RuntimeImage == "" || len(spec.RuntimeImage) > 512 || strings.ContainsAny(spec.RuntimeImage, "\n\r \t") {
		return Spec{}, errors.New("Locust runtime image is invalid")
	}
	if spec.Script == "" || len(spec.Script) > 262144 || (!strings.Contains(spec.Script, "from locust") && !strings.Contains(spec.Script, "import locust")) {
		return Spec{}, errors.New("Locust template must contain a Locust import and at most 256 KiB")
	}
	if len(spec.Parameters) > 50 {
		return Spec{}, errors.New("at most 50 Locust parameters are allowed")
	}
	seen := map[string]bool{}
	for name, value := range spec.Parameters {
		key := strings.ToUpper(name)
		if !parameterName.MatchString(name) || len(value) > 4096 || seen[key] {
			return Spec{}, fmt.Errorf("Locust parameter %q is invalid", name)
		}
		seen[key] = true
	}
	return spec, nil
}

func namespaceMarker(ctx context.Context, runner commandRunner, kubeconfig, namespace string) (string, error) {
	value, err := runner.Run(ctx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
	return strings.TrimSpace(value), err
}

func digest(value []byte) string {
	hash := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(hash[:])
}

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
	directory, err := os.MkdirTemp("", "kubephos-external-load-")
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
		return string(output), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
