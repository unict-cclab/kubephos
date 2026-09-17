package strategyimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID    = "io.kubephos.catalog.strategy-image"
	artifactAPI = "artifacts.kubephos.dev/v1alpha1"
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	RegistryEndpointRef   string          `json:"registryEndpointRef"`
	RegistryCredentialRef string          `json:"registryCredentialRef"`
	Name                  string          `json:"name"`
	Kind                  string          `json:"kind"`
	SourceImage           string          `json:"sourceImage"`
	DefaultConfiguration  json.RawMessage `json:"defaultConfiguration"`
}

type stepInput struct {
	Spec
}

type registryEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		CABundle string `json:"caBundle"`
		Insecure bool   `json:"insecure"`
	} `json:"spec"`
}

type registryCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Role string `json:"role"`
	} `json:"metadata"`
	Spec struct {
		Server   string   `json:"server"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		Project  string   `json:"project"`
		Scopes   []string `json:"scopes"`
	} `json:"spec"`
}

type strategyImage struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   strategyImageMetadata `json:"metadata"`
	Spec       strategyImageSpec     `json:"spec"`
}

type strategyImageMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type strategyImageSpec struct {
	StrategyKind         string          `json:"strategyKind"`
	SourceImage          string          `json:"sourceImage"`
	SourceDigest         string          `json:"sourceDigest"`
	MirroredImage        string          `json:"mirroredImage"`
	MirroredDigest       string          `json:"mirroredDigest"`
	DefaultConfiguration json.RawMessage `json:"defaultConfiguration"`
}

type result struct {
	StrategyImage strategyImage `json:"strategyImage"`
}

type commandRunner interface {
	Run(context.Context, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Strategy image mirror", Version: "0.1.0",
		Description:     "Validates an OCI scheduler, descheduler or autoscaler image and mirrors it into managed Harbor.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["registryEndpointRef","registryCredentialRef","name","kind","sourceImage","defaultConfiguration"],"properties":{"registryEndpointRef":{"type":"string","title":"Harbor endpoint","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryEndpoint","x-kubephos-artifact-version":"v1alpha1"},"registryCredentialRef":{"type":"string","title":"Harbor push identity","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryCredential","x-kubephos-artifact-version":"v1alpha1"},"name":{"type":"string","title":"Name","pattern":"^[a-z0-9][a-z0-9-]{0,62}$"},"kind":{"type":"string","title":"Type","enum":["scheduler","descheduler","autoscaler"]},"sourceImage":{"type":"string","title":"Docker image"},"defaultConfiguration":{"type":"object","title":"Default configuration","additionalProperties":true}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "RegistryEndpoint", Version: "v1alpha1"}, {Type: "RegistryCredential", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "StrategyImage", Version: "v1alpha1"}},
		Capabilities:    []string{"catalog.strategy.mirror", "catalog.strategy.preflight", "catalog.strategy.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"network.http", "registry.manage"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	if !strings.HasPrefix(spec.RegistryEndpointRef, "art_") || !strings.HasPrefix(spec.RegistryCredentialRef, "art_") || spec.RegistryEndpointRef == spec.RegistryCredentialRef {
		return invalid(report, "registryEndpointRef", "Select a verified Harbor endpoint and its push identity.")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(spec.Name) {
		return invalid(report, "name", "Use a lowercase name with at most 63 characters.")
	}
	if spec.Kind != "scheduler" && spec.Kind != "descheduler" && spec.Kind != "autoscaler" {
		return invalid(report, "kind", "Select scheduler, descheduler or autoscaler.")
	}
	if err := validateImageReference(spec.SourceImage); err != nil {
		return invalid(report, "sourceImage", err.Error())
	}
	if len(spec.DefaultConfiguration) == 0 || !json.Valid(spec.DefaultConfiguration) {
		return invalid(report, "defaultConfiguration", "Default configuration must be a JSON object.")
	}
	var configuration map[string]any
	if json.Unmarshal(spec.DefaultConfiguration, &configuration) != nil {
		return invalid(report, "defaultConfiguration", "Default configuration must be a JSON object.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The source manifest and digest will be verified before the image is copied to Harbor."})
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
	input, err := json.Marshal(stepInput{Spec: spec})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "mirror-strategy-image", Name: "Validate and mirror strategy image", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "registry-endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", ArtifactID: spec.RegistryEndpointRef}, {Name: "registry-credential", Type: "RegistryCredential", Version: "v1alpha1", ArtifactID: spec.RegistryCredentialRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "strategy-image", Type: "StrategyImage", Version: "v1alpha1", MediaType: "application/json", Source: "/strategyImage"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, endpoint, credential, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateRegistry(endpoint, credential); err != nil {
		return unhealthy(err.Error(), "registry", "invalid"), nil
	}
	if step.Cleanup {
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The mirrored image reference is safe to remove", Checks: map[string]string{"registry": endpoint.Spec.Host}}, nil
	}
	if err := log("info", "Inspecting the source OCI manifest before any registry mutation"); err != nil {
		return domain.HealthReport{}, err
	}
	value, err := plugin.runner().Run(ctx, nil, "inspect", "--format", "{{.Digest}}", "docker://"+input.SourceImage)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(value), "sha256:") {
		return unhealthy("Source image inspection failed: "+errorText(err), "sourceImage", "unavailable"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Source image and Harbor push identity are valid", Checks: map[string]string{"sourceDigest": strings.TrimSpace(value), "registry": endpoint.Spec.Host}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, endpoint, credential, err := resolve(step)
	if err != nil {
		return nil, err
	}
	sourceDigest, err := plugin.runner().Run(ctx, nil, "inspect", "--format", "{{.Digest}}", "docker://"+input.SourceImage)
	if err != nil {
		return nil, err
	}
	sourceDigest = strings.TrimSpace(sourceDigest)
	if !strings.HasPrefix(sourceDigest, "sha256:") || len(strings.TrimPrefix(sourceDigest, "sha256:")) < 12 {
		return nil, errors.New("source image did not expose a valid immutable digest")
	}
	target := endpoint.Spec.Host + "/" + credential.Spec.Project + "/" + input.Kind + "-" + input.Name + ":" + strings.TrimPrefix(sourceDigest, "sha256:")[:12]
	auth, ca, err := registryFiles(endpoint, credential)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Dir(auth.Path))
	if err := log("info", "Copying the immutable source digest into managed Harbor"); err != nil {
		return nil, err
	}
	args := []string{"copy", "--authfile", auth.Path, "--dest-tls-verify=true"}
	if endpoint.Spec.Insecure {
		args[len(args)-1] = "--dest-tls-verify=false"
	} else if ca.Path != "" {
		args = append(args, "--dest-cert-dir", filepath.Dir(ca.Path))
	}
	args = append(args, "docker://"+pinnedImage(input.SourceImage, sourceDigest), "docker://"+target)
	if _, err := plugin.runner().Run(ctx, auth.Value, args...); err != nil {
		return nil, err
	}
	inspectArgs := []string{"inspect", "--authfile", auth.Path, tlsFlag(endpoint), "--format", "{{.Digest}}"}
	if ca.Path != "" {
		inspectArgs = append(inspectArgs, "--cert-dir", filepath.Dir(ca.Path))
	}
	inspectArgs = append(inspectArgs, "docker://"+target)
	mirroredDigest, err := plugin.runner().Run(ctx, auth.Value, inspectArgs...)
	if err != nil {
		return nil, err
	}
	mirroredDigest = strings.TrimSpace(mirroredDigest)
	value := result{StrategyImage: strategyImage{APIVersion: artifactAPI, Kind: "StrategyImage", Metadata: strategyImageMetadata{Name: input.Name, Version: strings.TrimPrefix(sourceDigest, "sha256:")[:12]}, Spec: strategyImageSpec{StrategyKind: input.Kind, SourceImage: input.SourceImage, SourceDigest: sourceDigest, MirroredImage: target + "@" + mirroredDigest, MirroredDigest: mirroredDigest, DefaultConfiguration: input.DefaultConfiguration}}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, _ plugins.Logger) (domain.HealthReport, error) {
	_, endpoint, credential, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup {
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Mirrored image was removed", Checks: map[string]string{"image": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil || value.StrategyImage.Kind != "StrategyImage" {
		return unhealthy("Strategy image artifact is invalid", "artifact", "invalid"), nil
	}
	auth, ca, err := registryFiles(endpoint, credential)
	if err != nil {
		return domain.HealthReport{}, err
	}
	defer os.RemoveAll(filepath.Dir(auth.Path))
	args := []string{"inspect", "--authfile", auth.Path, tlsFlag(endpoint), "--format", "{{.Digest}}"}
	if ca.Path != "" {
		args = append(args, "--cert-dir", filepath.Dir(ca.Path))
	}
	args = append(args, "docker://"+strings.Split(value.StrategyImage.Spec.MirroredImage, "@")[0])
	digest, err := plugin.runner().Run(ctx, auth.Value, args...)
	if err != nil || strings.TrimSpace(digest) != value.StrategyImage.Spec.MirroredDigest {
		return unhealthy("Mirrored image digest verification failed", "mirroredImage", "invalid"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Strategy image is immutable and available in Harbor", Checks: map[string]string{"digest": strings.TrimSpace(digest)}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, raw json.RawMessage, _ plugins.Logger) error {
	_, endpoint, credential, err := resolve(step)
	if err != nil {
		return err
	}
	var value result
	if json.Unmarshal(raw, &value) != nil || value.StrategyImage.Spec.MirroredImage == "" {
		return nil
	}
	auth, ca, err := registryFiles(endpoint, credential)
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(auth.Path))
	args := []string{"delete", "--authfile", auth.Path, tlsFlag(endpoint)}
	if ca.Path != "" {
		args = append(args, "--cert-dir", filepath.Dir(ca.Path))
	}
	args = append(args, "docker://"+value.StrategyImage.Spec.MirroredImage)
	_, err = plugin.runner().Run(ctx, auth.Value, args...)
	return err
}

type temporaryFile struct {
	Path  string
	Value []byte
}

func registryFiles(endpoint registryEndpoint, credential registryCredential) (temporaryFile, temporaryFile, error) {
	directory, err := os.MkdirTemp("", "kubephos-strategy-registry-*")
	if err != nil {
		return temporaryFile{}, temporaryFile{}, err
	}
	authValue, _ := json.Marshal(map[string]any{"auths": map[string]any{endpoint.Spec.Host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(credential.Spec.Username + ":" + credential.Spec.Password))}}})
	authPath := filepath.Join(directory, "auth.json")
	if err := os.WriteFile(authPath, authValue, 0600); err != nil {
		os.RemoveAll(directory)
		return temporaryFile{}, temporaryFile{}, err
	}
	caPath := ""
	if endpoint.Spec.CABundle != "" {
		caPath = filepath.Join(directory, "ca.crt")
		if err := os.WriteFile(caPath, []byte(endpoint.Spec.CABundle), 0600); err != nil {
			os.RemoveAll(directory)
			return temporaryFile{}, temporaryFile{}, err
		}
	}
	return temporaryFile{Path: authPath, Value: authValue}, temporaryFile{Path: caPath}, nil
}

func pinnedImage(image, digest string) string {
	if position := strings.LastIndex(image, "@"); position >= 0 {
		image = image[:position]
	}
	return image + "@" + digest
}

func resolve(step domain.PlanStep) (stepInput, registryEndpoint, registryCredential, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return input, registryEndpoint{}, registryCredential{}, err
	}
	endpointValue, endpointOK := step.ResolvedInputs["registry-endpoint"]
	credentialValue, credentialOK := step.ResolvedInputs["registry-credential"]
	if !endpointOK || !credentialOK {
		return input, registryEndpoint{}, registryCredential{}, errors.New("verified Harbor artifacts are unavailable")
	}
	var endpoint registryEndpoint
	var credential registryCredential
	if err := json.Unmarshal(endpointValue.Value, &endpoint); err != nil {
		return input, endpoint, credential, err
	}
	if err := json.Unmarshal(credentialValue.Value, &credential); err != nil {
		return input, endpoint, credential, err
	}
	return input, endpoint, credential, nil
}

func validateRegistry(endpoint registryEndpoint, credential registryCredential) error {
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Host == "" {
		return errors.New("Harbor endpoint is invalid")
	}
	if credential.APIVersion != artifactAPI || credential.Kind != "RegistryCredential" || credential.Metadata.Role != "push" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || credential.Spec.Project == "" || !contains(credential.Spec.Scopes, "push") {
		return errors.New("Harbor push identity does not match the endpoint")
	}
	return nil
}

func validateImageReference(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return errors.New("Use an OCI image reference such as ghcr.io/team/scheduler:v1.")
	}
	parsed, err := url.Parse("docker://" + value)
	if err != nil || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return errors.New("Docker image reference is invalid.")
	}
	return nil
}

func tlsFlag(endpoint registryEndpoint) string {
	if endpoint.Spec.Insecure {
		return "--tls-verify=false"
	}
	return "--tls-verify=true"
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func errorText(err error) string {
	if err == nil {
		return "digest is missing"
	}
	return err.Error()
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(message, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: message, Checks: map[string]string{key: value}}
}

type localRunner struct{}

func (localRunner) Run(ctx context.Context, _ []byte, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "skopeo", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("skopeo: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return localRunner{}
}
