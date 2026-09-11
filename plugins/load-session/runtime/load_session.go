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
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
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
	loadProfileKey          = "kubephos.dev/load-profile"
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
	LoadProfileSetRef        string `json:"loadProfileSetRef"`
	ProfileID                string `json:"profileId"`
	Action                   string `json:"action"`
	Replicas                 int    `json:"replicas"`
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
		Profiles []loadProfile `json:"profiles"`
	} `json:"spec"`
}

type loadProfile struct {
	ID             string         `json:"id"`
	TargetEndpoint string         `json:"targetEndpoint"`
	Replicas       int            `json:"replicas"`
	Workload       workloadTarget `json:"workload"`
	Manifest       string         `json:"manifest"`
	ManifestDigest string         `json:"manifestDigest"`
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
	ProfileID        string         `json:"profileId"`
	Action           string         `json:"action"`
	State            string         `json:"state"`
	Replicas         int            `json:"replicas"`
	PreviousReplicas int            `json:"previousReplicas"`
	Workload         workloadTarget `json:"workload"`
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

type manifestResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Interactive Kubernetes load session", Version: "0.1.0",
		Description:     "Starts, stops or resumes a declared load profile without coupling KubePhos to a load-testing tool.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","loadProfileSetRef","profileId","action","replicas"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"loadProfileSetRef":{"type":"string","title":"Load profiles","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadProfileSet","x-kubephos-artifact-version":"v1alpha1"},"profileId":{"type":"string","title":"Load profile","description":"Stable profile ID declared by the application package.","minLength":1,"maxLength":63,"default":"default-load"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["start","stop","resume"],"default":"start"},"replicas":{"type":"integer","title":"Load intensity","description":"Number of load driver replicas used by start or resume.","minimum":1,"maximum":1000,"default":1}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "LoadProfileSet", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "LoadSession", Version: "v1alpha1"}},
		Capabilities:    []string{"load.session.control", "load.session.preflight"},
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
	refs := []struct{ path, value string }{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"applicationDeploymentRef", spec.ApplicationDeploymentRef}, {"loadProfileSetRef", spec.LoadProfileSetRef}}
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
	if spec.ProfileID == "" || len(spec.ProfileID) > 63 || strings.ContainsAny(spec.ProfileID, "\n\r") {
		return invalid(report, "profileId", "Select a valid declared load profile.")
	}
	if spec.Action != "start" && spec.Action != "stop" && spec.Action != "resume" {
		return invalid(report, "action", "Action must be start, stop or resume.")
	}
	if spec.Replicas < 1 || spec.Replicas > 1000 {
		return invalid(report, "replicas", "Load intensity must be between 1 and 1000.")
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
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "control-load-session", Name: strings.ToUpper(spec.Action[:1]) + spec.Action[1:] + " declared load profile", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "load-profile-set", Type: "LoadProfileSet", Version: "v1alpha1", ArtifactID: spec.LoadProfileSetRef},
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
	switch spec.Action {
	case "start":
		if exists {
			return unhealthy("The declared load workload already exists; use resume only after a KubePhos stop", "loadSession", "conflict"), nil
		}
		manifest, err := ownedManifest(profile.Manifest, deployment.Metadata.OwnershipMarker, profile.ID)
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
	spec, cluster, deployment, _, profile, _, err := resolve(step)
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
		manifest, err := ownedManifest(profile.Manifest, deployment.Metadata.OwnershipMarker, profile.ID)
		if err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--namespace", deployment.Spec.Namespace, "-f", "-"); err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(spec.Replicas)); err != nil {
			return nil, err
		}
	case "stop", "resume":
		if !ownedBy(state, deployment, profile) {
			return nil, errors.New("load workload ownership changed after precheck")
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
	if spec.Action == "stop" {
		replicas = 0
		stateValue = "stopped"
	}
	value := result{LoadSession: loadSession{
		APIVersion: artifactAPI, Kind: "LoadSession", Metadata: loadSessionMetadata{Name: deployment.Spec.Namespace + "/" + profile.ID, Version: "v1alpha1"},
		Spec: loadSessionSpec{ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace, ProfileID: profile.ID, Action: spec.Action, State: stateValue, Replicas: replicas, PreviousReplicas: previous, Workload: profile.Workload},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, deployment, _, profile, endpoint, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	expectedReplicas := spec.Replicas
	expectedState := "running"
	if spec.Action == "stop" {
		expectedReplicas = 0
		expectedState = "stopped"
	}
	if err := validateResult(value.LoadSession, spec, cluster, deployment, profile, expectedState, expectedReplicas); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	runner := plugin.runner()
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
	var value result
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	resource := resourceName(profile.Workload)
	if spec.Action == "start" {
		if err := log("warning", "Removing the load workload created by the failed start operation"); err != nil {
			return err
		}
		_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", resource, "-n", deployment.Spec.Namespace, "--wait=true", "--timeout=5m")
		return err
	}
	previous := value.LoadSession.Spec.PreviousReplicas
	if previous < 0 || previous > 1000 {
		return errors.New("previous load intensity is unavailable for compensation")
	}
	if err := log("warning", fmt.Sprintf("Restoring the previous load intensity %d", previous)); err != nil {
		return err
	}
	_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "scale", resource, "-n", deployment.Spec.Namespace, "--replicas", fmt.Sprint(previous))
	return err
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, loadProfileSet, loadProfile, serviceEndpoint, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, err
	}
	clusterRaw, clusterOK := step.ResolvedInputs["cluster-connection"]
	deploymentRaw, deploymentOK := step.ResolvedInputs["application-deployment"]
	profilesRaw, profilesOK := step.ResolvedInputs["load-profile-set"]
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
	for _, candidate := range profiles.Spec.Profiles {
		if candidate.ID == spec.ProfileID {
			profile = candidate
		}
	}
	if profile.ID == "" {
		return Spec{}, clusterConnection{}, applicationDeployment{}, loadProfileSet{}, loadProfile{}, serviceEndpoint{}, fmt.Errorf("load profile %s is not declared", spec.ProfileID)
	}
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
	if profiles.APIVersion != artifactAPI || profiles.Kind != "LoadProfileSet" || profiles.Metadata.ApplicationRef != deployment.Spec.ApplicationRef || len(profiles.Spec.Profiles) == 0 {
		return errors.New("load profile set identity is invalid")
	}
	digest, err := digestJSON(profiles.Spec.Profiles)
	if err != nil || digest != profiles.Metadata.Digest {
		return errors.New("load profile set digest is invalid")
	}
	if profile.ID == "" || profile.TargetEndpoint != endpoint.ID || profile.Replicas < 1 || profile.Replicas > 1000 || profile.Workload.ID != profile.ID || profile.Workload.APIVersion == "" || profile.Workload.Kind == "" || profile.Workload.Name == "" || len(profile.Workload.Selector) == 0 {
		return errors.New("load profile interface is invalid")
	}
	manifestDigest := sha256.Sum256([]byte(profile.Manifest))
	if profile.ManifestDigest != "sha256:"+hex.EncodeToString(manifestDigest[:]) {
		return errors.New("load profile manifest digest is invalid")
	}
	var resource manifestResource
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(profile.Manifest)))
	if err := decoder.Decode(&resource); err != nil {
		return fmt.Errorf("decode load profile manifest: %w", err)
	}
	var extra any
	err = decoder.Decode(&extra)
	if err == nil {
		return errors.New("load profile manifest must contain exactly one resource")
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing load profile manifest: %w", err)
	}
	if resource.APIVersion != profile.Workload.APIVersion || resource.Kind != profile.Workload.Kind || resource.Metadata.Name != profile.Workload.Name || resource.Metadata.Namespace != "" {
		return errors.New("load profile manifest does not match its declared workload")
	}
	for key, expected := range profile.Workload.Selector {
		if resource.Spec.Template.Metadata.Labels[key] != expected {
			return errors.New("load profile selector does not match its Pod template")
		}
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
	if session.APIVersion != artifactAPI || session.Kind != "LoadSession" || session.Metadata.Name != deployment.Spec.Namespace+"/"+profile.ID || session.Metadata.Version != "v1alpha1" || session.Spec.ApplicationRef != deployment.Spec.ApplicationRef || session.Spec.ClusterServer != cluster.Spec.Server || session.Spec.Namespace != deployment.Spec.Namespace || session.Spec.ProfileID != profile.ID || session.Spec.Action != spec.Action || session.Spec.State != state || session.Spec.Replicas != replicas || session.Spec.Workload.Name != profile.Workload.Name {
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

func ownedManifest(value, marker, profileID string) ([]byte, error) {
	var resource map[string]any
	if err := yaml.Unmarshal([]byte(value), &resource); err != nil {
		return nil, err
	}
	metadata, ok := resource["metadata"].(map[string]any)
	if !ok {
		return nil, errors.New("load profile manifest metadata is invalid")
	}
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		annotations = map[string]any{}
	}
	annotations[applicationOwnershipKey] = marker
	annotations[loadProfileKey] = profileID
	metadata["annotations"] = annotations
	return yaml.Marshal(resource)
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
