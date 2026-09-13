package monagentprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID          = "io.kubephos.monitoring.mon-agent.profile"
	artifactAPI       = "artifacts.kubephos.dev/v1alpha1"
	profileMarkerKey  = "kubephos.dev/mon-agent-profile"
	previousPeriodKey = "kubephos.dev/mon-agent-previous-period"
	previousRangeKey  = "kubephos.dev/mon-agent-previous-range"
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
	ObservabilityRef         string `json:"observabilityRef"`
	ScrapePeriodSeconds      int    `json:"scrapePeriodSeconds"`
	PromQLRange              string `json:"promQLRange"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Server     string `json:"server"`
		Kubeconfig string `json:"kubeconfig"`
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
		ApplicationRef string `json:"applicationRef"`
		ClusterServer  string `json:"clusterServer"`
		Namespace      string `json:"namespace"`
	} `json:"spec"`
}

type observabilityCapability struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		ClusterServer string `json:"clusterServer"`
		Namespace     string `json:"namespace"`
		MonAgent      struct {
			Name                string `json:"name"`
			Version             string `json:"version"`
			Namespace           string `json:"namespace"`
			ScrapePeriodSeconds int    `json:"scrapePeriodSeconds"`
			PromQLRange         string `json:"promQLRange"`
			Status              string `json:"status"`
		} `json:"monAgent"`
	} `json:"spec"`
}

type deploymentState struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
					Env  []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type profileArtifact struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   profileMetadata `json:"metadata"`
	Spec       profileSpec     `json:"spec"`
}

type profileMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type profileSpec struct {
	ApplicationRef        string `json:"applicationRef"`
	ClusterServer         string `json:"clusterServer"`
	ScrapePeriodSeconds   int    `json:"scrapePeriodSeconds"`
	PromQLRange           string `json:"promQLRange"`
	PreviousPeriodSeconds int    `json:"previousPeriodSeconds"`
	PreviousPromQLRange   string `json:"previousPromQLRange"`
}

type result struct {
	MonAgentProfile profileArtifact `json:"monAgentProfile"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Mon-agent experiment profile", Version: "0.1.0",
		Description:     "Applies and restores the two experiment-sensitive mon-agent sampling parameters.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","observabilityRef","scrapePeriodSeconds","promQLRange"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityRef":{"type":"string","title":"Managed observability","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"},"scrapePeriodSeconds":{"type":"integer","title":"Mon-agent sampling period in seconds","minimum":5,"maximum":300,"default":30},"promQLRange":{"type":"string","title":"Mon-agent PromQL range","enum":["1m","5m","10m","15m","30m","1h"],"default":"5m"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "ObservabilityCapability", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "MonAgentProfile", Version: "v1alpha1"}},
		Capabilities:    []string{"monitoring.mon-agent.configure", "monitoring.mon-agent.preflight", "monitoring.mon-agent.cleanup", "lifecycle.cleanup"},
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
	for path, value := range map[string]string{"clusterConnectionRef": spec.ClusterConnectionRef, "applicationDeploymentRef": spec.ApplicationDeploymentRef, "observabilityRef": spec.ObservabilityRef} {
		if !strings.HasPrefix(value, "art_") {
			return invalid(report, path, "Select a verified artifact.")
		}
	}
	if spec.ScrapePeriodSeconds < 5 || spec.ScrapePeriodSeconds > 300 {
		return invalid(report, "scrapePeriodSeconds", "Sampling period must be between 5 and 300 seconds.")
	}
	if !oneOf(spec.PromQLRange, "1m", "5m", "10m", "15m", "30m", "1h") {
		return invalid(report, "promQLRange", "Select a supported PromQL range.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The managed mon-agent and its current settings will be revalidated before they are changed."})
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
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "configure-mon-agent", Name: "Configure and verify mon-agent", Input: raw, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}, {Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef}, {Name: "observability", Type: "ObservabilityCapability", Version: "v1alpha1", ArtifactID: spec.ObservabilityRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "mon-agent-profile", Type: "MonAgentProfile", Version: "v1alpha1", MediaType: "application/json", Source: "/monAgentProfile"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	_, cluster, application, observability, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(cluster, application, observability); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	state, err := readDeployment(ctx, runner, cluster.Spec.Kubeconfig, observability)
	if err != nil {
		return unhealthy("Managed mon-agent is unavailable: "+err.Error(), "monAgent", "unavailable"), nil
	}
	marker := state.Metadata.Annotations[profileMarkerKey]
	if step.Cleanup {
		if marker != "" && marker != application.Metadata.OwnershipMarker {
			return unhealthy("Mon-agent is owned by another run", "ownership", "conflict"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Mon-agent settings are safe to restore", Checks: map[string]string{"api": "ready", "ownership": "verified"}}, nil
	}
	if marker != "" {
		return unhealthy("Mon-agent already has an active experiment profile", "ownership", "conflict"), nil
	}
	period, queryRange, err := settings(state, observability.Spec.MonAgent.Name)
	if err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Validated mon-agent %s with current period %ds and range %s", observability.Spec.MonAgent.Version, period, queryRange)); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Mon-agent is ready for the experiment profile", Checks: map[string]string{"api": "ready", "monAgent": "ready", "ownership": "available"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, application, observability, err := resolve(step)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	state, err := readDeployment(ctx, runner, cluster.Spec.Kubeconfig, observability)
	if err != nil {
		return nil, err
	}
	previousPeriod, previousRange, err := settings(state, observability.Spec.MonAgent.Name)
	if err != nil {
		return nil, err
	}
	resource := "deployment/" + observability.Spec.MonAgent.Name
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "annotate", resource, "-n", observability.Spec.MonAgent.Namespace, profileMarkerKey+"="+application.Metadata.OwnershipMarker, previousPeriodKey+"="+strconv.Itoa(previousPeriod), previousRangeKey+"="+previousRange, "--overwrite"); err != nil {
		return nil, err
	}
	if err := log("info", fmt.Sprintf("Applying mon-agent period %ds and PromQL range %s", spec.ScrapePeriodSeconds, spec.PromQLRange)); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "set", "env", resource, "-n", observability.Spec.MonAgent.Namespace, "SCRAPE_PERIOD_SECONDS="+strconv.Itoa(spec.ScrapePeriodSeconds), "PROMQL_RANGE="+spec.PromQLRange); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", resource, "-n", observability.Spec.MonAgent.Namespace, "--timeout=5m"); err != nil {
		return nil, err
	}
	value := result{MonAgentProfile: profileArtifact{APIVersion: artifactAPI, Kind: "MonAgentProfile", Metadata: profileMetadata{Name: application.Spec.Namespace, Version: "v1alpha1"}, Spec: profileSpec{ApplicationRef: application.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, ScrapePeriodSeconds: spec.ScrapePeriodSeconds, PromQLRange: spec.PromQLRange, PreviousPeriodSeconds: previousPeriod, PreviousPromQLRange: previousRange}}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, application, observability, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	state, err := readDeployment(ctx, plugin.runner(), cluster.Spec.Kubeconfig, observability)
	if err != nil {
		return unhealthy(err.Error(), "monAgent", "unavailable"), nil
	}
	period, queryRange, err := settings(state, observability.Spec.MonAgent.Name)
	if err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	if step.Cleanup {
		if state.Metadata.Annotations[profileMarkerKey] != "" || period != observability.Spec.MonAgent.ScrapePeriodSeconds || queryRange != observability.Spec.MonAgent.PromQLRange {
			return unhealthy("Mon-agent defaults were not restored", "monAgent", "invalid"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Mon-agent defaults were restored", Checks: map[string]string{"monAgent": "ready", "profile": "default"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if value.MonAgentProfile.APIVersion != artifactAPI || value.MonAgentProfile.Kind != "MonAgentProfile" || value.MonAgentProfile.Spec.ApplicationRef != application.Spec.ApplicationRef || value.MonAgentProfile.Spec.ClusterServer != cluster.Spec.Server || period != spec.ScrapePeriodSeconds || queryRange != spec.PromQLRange || state.Metadata.Annotations[profileMarkerKey] != application.Metadata.OwnershipMarker {
		return unhealthy("Mon-agent profile does not match the validated configuration", "monAgent", "invalid"), nil
	}
	if err := log("info", "Mon-agent experiment settings and ownership are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Mon-agent experiment profile is active", Checks: map[string]string{"periodSeconds": strconv.Itoa(period), "promQLRange": queryRange}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	_, cluster, application, observability, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	state, err := readDeployment(ctx, runner, cluster.Spec.Kubeconfig, observability)
	if err != nil {
		return err
	}
	marker := state.Metadata.Annotations[profileMarkerKey]
	if marker == "" {
		return nil
	}
	if marker != application.Metadata.OwnershipMarker {
		return errors.New("refusing to restore a mon-agent profile owned by another run")
	}
	previousPeriod, err := strconv.Atoi(state.Metadata.Annotations[previousPeriodKey])
	if err != nil || previousPeriod < 1 {
		return errors.New("previous mon-agent period is invalid")
	}
	previousRange := state.Metadata.Annotations[previousRangeKey]
	if previousRange == "" {
		return errors.New("previous mon-agent query range is invalid")
	}
	resource := "deployment/" + observability.Spec.MonAgent.Name
	if err := log("warning", "Restoring the previous mon-agent settings"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "set", "env", resource, "-n", observability.Spec.MonAgent.Namespace, "SCRAPE_PERIOD_SECONDS="+strconv.Itoa(previousPeriod), "PROMQL_RANGE="+previousRange); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "annotate", resource, "-n", observability.Spec.MonAgent.Namespace, profileMarkerKey+"-", previousPeriodKey+"-", previousRangeKey+"-"); err != nil {
		return err
	}
	_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", resource, "-n", observability.Spec.MonAgent.Namespace, "--timeout=5m")
	return err
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, observabilityCapability, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, err
	}
	clusterRaw, clusterOK := step.ResolvedInputs["cluster-connection"]
	applicationRaw, applicationOK := step.ResolvedInputs["application-deployment"]
	observabilityRaw, observabilityOK := step.ResolvedInputs["observability"]
	if !clusterOK || !applicationOK || !observabilityOK {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, errors.New("verified mon-agent profile inputs are unavailable")
	}
	var cluster clusterConnection
	var application applicationDeployment
	var observability observabilityCapability
	if err := json.Unmarshal(clusterRaw.Value, &cluster); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, err
	}
	if err := json.Unmarshal(applicationRaw.Value, &application); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, err
	}
	if err := json.Unmarshal(observabilityRaw.Value, &observability); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, err
	}
	return spec, cluster, application, observability, nil
}

func validateArtifacts(cluster clusterConnection, application applicationDeployment, observability observabilityCapability) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Kubeconfig == "" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if application.APIVersion != artifactAPI || application.Kind != "ApplicationDeployment" || application.Metadata.Version != "v1alpha1" || application.Metadata.OwnershipMarker == "" || application.Spec.ClusterServer != cluster.Spec.Server {
		return errors.New("application deployment identity is invalid")
	}
	if observability.APIVersion != artifactAPI || observability.Kind != "ObservabilityCapability" || observability.Spec.ClusterServer != cluster.Spec.Server || observability.Spec.MonAgent.Name == "" || observability.Spec.MonAgent.Namespace == "" || observability.Spec.MonAgent.Version == "" || observability.Spec.MonAgent.Status != "ready" {
		return errors.New("managed mon-agent capability is invalid")
	}
	return nil
}

func readDeployment(ctx context.Context, runner commandRunner, kubeconfig string, observability observabilityCapability) (deploymentState, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "deployment/"+observability.Spec.MonAgent.Name, "-n", observability.Spec.MonAgent.Namespace, "-o", "json")
	if err != nil {
		return deploymentState{}, err
	}
	var state deploymentState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return deploymentState{}, err
	}
	return state, nil
}

func settings(state deploymentState, containerName string) (int, string, error) {
	for _, container := range state.Spec.Template.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		values := map[string]string{}
		for _, variable := range container.Env {
			values[variable.Name] = variable.Value
		}
		period, err := strconv.Atoi(values["SCRAPE_PERIOD_SECONDS"])
		if err != nil || period < 1 || values["PROMQL_RANGE"] == "" {
			return 0, "", errors.New("managed mon-agent settings are invalid")
		}
		return period, values["PROMQL_RANGE"], nil
	}
	return 0, "", errors.New("managed mon-agent container is unavailable")
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
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
	file, err := os.CreateTemp("", "kubephos-kubeconfig-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.WriteString(kubeconfig); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
