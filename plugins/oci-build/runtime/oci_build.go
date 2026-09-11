package ocibuild

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const pluginID = "io.kubephos.build.oci"
const artifactAPI = "artifacts.kubephos.dev/v1alpha1"
const buildContextByteLimit = 2 << 30
const buildContextFileLimit = 200000

var commitExpression = regexp.MustCompile(`^[0-9a-f]{40}$`)
var imageNameExpression = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var tagSuffixExpression = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,31}$`)
var relativePathExpression = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)

type Plugin struct {
	Runner buildRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	RegistryEndpointRef   string `json:"registryEndpointRef"`
	RegistryCredentialRef string `json:"registryCredentialRef"`
	RepositoryURL         string `json:"repositoryURL"`
	Commit                string `json:"commit"`
	ContextPath           string `json:"contextPath"`
	Dockerfile            string `json:"dockerfile"`
	ImageName             string `json:"imageName"`
	TagSuffix             string `json:"tagSuffix"`
}

type registryEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Protocol string   `json:"protocol"`
		Host     string   `json:"host"`
		URL      string   `json:"url"`
		APIURL   string   `json:"apiURL"`
		CABundle string   `json:"caBundle"`
		Insecure bool     `json:"insecure"`
		Projects []string `json:"projects"`
	} `json:"spec"`
}

type registryCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
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

type ociImage struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ociImageMetadata `json:"metadata"`
	Spec       ociImageSpec     `json:"spec"`
}

type ociImageMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ociImageSpec struct {
	Reference  string         `json:"reference"`
	Registry   string         `json:"registry"`
	Repository string         `json:"repository"`
	Tag        string         `json:"tag"`
	Digest     string         `json:"digest"`
	Source     ociImageSource `json:"source"`
}

type ociImageSource struct {
	RepositoryURL string `json:"repositoryURL"`
	Commit        string `json:"commit"`
	ContextPath   string `json:"contextPath"`
	Dockerfile    string `json:"dockerfile"`
}

type buildResult struct {
	Image ociImage `json:"image"`
}

type buildRunner interface {
	Precheck(context.Context, Spec, registryEndpoint, registryCredential, plugins.Logger) error
	Build(context.Context, Spec, registryEndpoint, registryCredential, plugins.Logger) (ociImage, error)
	Verify(context.Context, ociImage, registryEndpoint, registryCredential, plugins.Logger) error
	Cleanup(context.Context, Spec, registryEndpoint, registryCredential, plugins.Logger) error
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "OCI image build", Version: "0.2.0",
		Description:     "Builds an immutable OCI image from an exact Git commit and publishes it to a managed registry.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["registryEndpointRef","registryCredentialRef","repositoryURL","commit","contextPath","dockerfile","imageName","tagSuffix"],"properties":{"registryEndpointRef":{"type":"string","title":"Target registry","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryEndpoint","x-kubephos-artifact-version":"v1alpha1"},"registryCredentialRef":{"type":"string","title":"Push credential","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryCredential","x-kubephos-artifact-version":"v1alpha1"},"repositoryURL":{"type":"string","title":"Git repository","pattern":"^https://[^[:space:]]+$","maxLength":2048},"commit":{"type":"string","title":"Full commit SHA","pattern":"^[0-9a-f]{40}$"},"contextPath":{"type":"string","title":"Build context","pattern":"^(\\.|[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)$","maxLength":256,"default":"."},"dockerfile":{"type":"string","title":"Dockerfile","pattern":"^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$","maxLength":256,"default":"Dockerfile"},"imageName":{"type":"string","title":"Image name","pattern":"^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$","maxLength":255},"tagSuffix":{"type":"string","title":"Tag suffix","pattern":"^[a-z0-9][a-z0-9.-]{0,31}$","default":"dev"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "RegistryEndpoint", Version: "v1alpha1"}, {Type: "RegistryCredential", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "OCIImage", Version: "v1alpha1"}},
		Capabilities:    []string{"build.oci.execute", "build.oci.preflight", "lifecycle.cleanup"},
		Permissions:     []string{"network.git.read", "registry.push", "executor.build"},
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
	for _, issue := range validateSpec(spec) {
		report.Issues = append(report.Issues, issue)
		if issue.Level == "error" {
			report.Valid = false
		}
	}
	if report.Valid {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will verify source access, the rootless executor and registry permissions before starting the build."})
	}
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
	if len(validateSpec(spec)) != 0 {
		return domain.Plan{}, errors.New("build configuration is invalid")
	}
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "build-and-publish", Name: "Build and publish immutable OCI image", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "registry-endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", ArtifactID: spec.RegistryEndpointRef},
			{Name: "registry-credential", Type: "RegistryCredential", Version: "v1alpha1", ArtifactID: spec.RegistryCredentialRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "oci-image", Type: "OCIImage", Version: "v1alpha1", MediaType: "application/json", Source: "/image"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResolved(spec, endpoint, credential); err != nil {
		return unhealthy(err.Error(), "inputs", "invalid"), nil
	}
	if err := plugin.runner().Precheck(ctx, spec, endpoint, credential, log); err != nil {
		return unhealthy("Build precheck failed: "+err.Error(), "build", "blocked"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Source, rootless executor and target registry are ready", Checks: map[string]string{"source": spec.Commit[:12], "executor": "rootless", "registry": endpoint.Spec.Host, "project": credential.Spec.Project}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return nil, err
	}
	image, err := plugin.runner().Build(ctx, spec, endpoint, credential, log)
	if err != nil {
		return nil, err
	}
	return json.Marshal(buildResult{Image: image})
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var result buildResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateImage(spec, endpoint, credential, result.Image); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if err := plugin.runner().Verify(ctx, result.Image, endpoint, credential, log); err != nil {
		return unhealthy("Published image verification failed: "+err.Error(), "registry", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The immutable image is available from the managed registry", Checks: map[string]string{"reference": result.Image.Spec.Reference, "source": spec.Commit}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return err
	}
	return plugin.runner().Cleanup(ctx, spec, endpoint, credential, log)
}

func (plugin Plugin) runner() buildRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return systemBuildRunner{}
}

func resolve(step domain.PlanStep) (Spec, registryEndpoint, registryCredential, error) {
	var spec Spec
	var endpoint registryEndpoint
	var credential registryCredential
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return spec, endpoint, credential, err
	}
	endpointInput, found := step.ResolvedInputs["registry-endpoint"]
	if !found {
		return spec, endpoint, credential, errors.New("verified registry endpoint is missing")
	}
	credentialInput, found := step.ResolvedInputs["registry-credential"]
	if !found {
		return spec, endpoint, credential, errors.New("verified registry credential is missing")
	}
	if err := json.Unmarshal(endpointInput.Value, &endpoint); err != nil {
		return spec, endpoint, credential, errors.New("registry endpoint artifact is invalid")
	}
	if err := json.Unmarshal(credentialInput.Value, &credential); err != nil {
		return spec, endpoint, credential, errors.New("registry credential artifact is invalid")
	}
	return spec, endpoint, credential, nil
}

func validateSpec(spec Spec) []domain.ValidationIssue {
	issues := []domain.ValidationIssue{}
	if !strings.HasPrefix(spec.RegistryEndpointRef, "art_") {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "registryEndpointRef", Message: "Select a verified managed registry endpoint."})
	}
	if !strings.HasPrefix(spec.RegistryCredentialRef, "art_") || spec.RegistryCredentialRef == spec.RegistryEndpointRef {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "registryCredentialRef", Message: "Select the matching protected push credential."})
	}
	repository, err := url.Parse(spec.RepositoryURL)
	if err != nil || len(spec.RepositoryURL) > 2048 || repository.Scheme != "https" || repository.Host == "" || repository.User != nil || repository.RawQuery != "" || repository.Fragment != "" {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "repositoryURL", Message: "Repository URL must be an HTTPS URL without credentials, query or fragment."})
	}
	if !commitExpression.MatchString(spec.Commit) {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "commit", Message: "Commit must be a full lowercase 40-character SHA."})
	}
	if len(spec.ContextPath) > 256 || spec.ContextPath != "." && !safeRelativePath(spec.ContextPath) {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "contextPath", Message: "Build context must be a safe relative path."})
	}
	if len(spec.Dockerfile) > 256 || !safeRelativePath(spec.Dockerfile) {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "dockerfile", Message: "Dockerfile must be a safe path relative to the build context."})
	}
	if len(spec.ImageName) > 255 || !imageNameExpression.MatchString(spec.ImageName) {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "imageName", Message: "Image name must be a lowercase OCI repository path."})
	}
	if !tagSuffixExpression.MatchString(spec.TagSuffix) {
		issues = append(issues, domain.ValidationIssue{Level: "error", Path: "tagSuffix", Message: "Tag suffix must be a lowercase label with at most 32 characters."})
	}
	return issues
}

func safeRelativePath(value string) bool {
	if !relativePathExpression.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validateResolved(spec Spec, endpoint registryEndpoint, credential registryCredential) error {
	if len(validateSpec(spec)) != 0 {
		return errors.New("build configuration is invalid")
	}
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "RegistryEndpoint" || endpoint.Metadata.Version == "" || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Host == "" || endpoint.Spec.URL == "" || endpoint.Spec.Insecure {
		return errors.New("registry endpoint does not implement RegistryEndpoint/v1alpha1")
	}
	if _, err := registryCertPool(endpoint.Spec.CABundle); err != nil {
		return errors.New("registry endpoint does not contain a valid certificate authority")
	}
	if credential.APIVersion != artifactAPI || credential.Kind != "RegistryCredential" || credential.Metadata.Role != "push" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || credential.Spec.Project == "" || !contains(credential.Spec.Scopes, "push") || !contains(endpoint.Spec.Projects, credential.Spec.Project) {
		return errors.New("registry credential is not a matching project-scoped push credential")
	}
	endpointURL, err := url.Parse(endpoint.Spec.URL)
	if err != nil || endpointURL.Host != endpoint.Spec.Host || endpointURL.User != nil || endpointURL.RawQuery != "" || endpointURL.Fragment != "" || endpointURL.Path != "" || endpointURL.Scheme != "https" {
		return errors.New("registry endpoint URL is inconsistent")
	}
	return nil
}

func validateImage(spec Spec, endpoint registryEndpoint, credential registryCredential, image ociImage) error {
	if image.APIVersion != artifactAPI || image.Kind != "OCIImage" || image.Metadata.Name != spec.ImageName || image.Metadata.Version != tag(spec) {
		return errors.New("OCI image artifact identity does not match the validated build")
	}
	repository := credential.Spec.Project + "/" + spec.ImageName
	expectedPrefix := endpoint.Spec.Host + "/" + repository + "@"
	if image.Spec.Registry != endpoint.Spec.Host || image.Spec.Repository != repository || image.Spec.Tag != tag(spec) || !strings.HasPrefix(image.Spec.Reference, expectedPrefix) || image.Spec.Reference != endpoint.Spec.Host+"/"+repository+"@"+image.Spec.Digest || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(image.Spec.Digest) {
		return errors.New("OCI image artifact does not contain a valid immutable registry reference")
	}
	if image.Spec.Source.RepositoryURL != spec.RepositoryURL || image.Spec.Source.Commit != spec.Commit || image.Spec.Source.ContextPath != spec.ContextPath || image.Spec.Source.Dockerfile != spec.Dockerfile {
		return errors.New("OCI image provenance does not match the validated source")
	}
	return nil
}

func tag(spec Spec) string {
	return "git-" + spec.Commit[:12] + "-" + spec.TagSuffix
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, check, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{check: value}}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type systemBuildRunner struct{}

type dockerSettings struct {
	host       string
	ca         string
	cert       string
	key        string
	registries []string
}

type registryClientFiles struct {
	root     string
	certDir  string
	authFile string
}

func (systemBuildRunner) Precheck(ctx context.Context, spec Spec, endpoint registryEndpoint, credential registryCredential, log plugins.Logger) error {
	if err := validateGitHost(spec.RepositoryURL); err != nil {
		return err
	}
	settings, runner, err := configuredRunner()
	if err != nil {
		return err
	}
	if err := plugins.ValidateRuntimeImage(runner, endpoint.Spec.Host+"/policy-check@sha256:"+strings.Repeat("0", 64)); err != nil {
		return err
	}
	readyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = runner.Ready(readyContext)
	cancel()
	if err != nil {
		return err
	}
	if err := log("info", "Checking access to the exact source repository and managed registry"); err != nil {
		return err
	}
	if _, err := exec.LookPath("skopeo"); err != nil {
		return errors.New("OCI registry client is unavailable")
	}
	sourceContext, sourceCancel := context.WithTimeout(ctx, 20*time.Second)
	defer sourceCancel()
	command := exec.CommandContext(sourceContext, "git", "ls-remote", "--heads", spec.RepositoryURL)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	command.Stdout = io.Discard
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return commandError("source repository is unavailable", stderr.Bytes(), err)
	}
	if err := registryPing(ctx, endpoint, credential, settings); err != nil {
		return err
	}
	files, err := prepareRegistryClient(endpoint)
	if err != nil {
		return err
	}
	defer os.RemoveAll(files.root)
	return registryLogin(ctx, files, endpoint, credential)
}

func (systemBuildRunner) Build(ctx context.Context, spec Spec, endpoint registryEndpoint, credential registryCredential, log plugins.Logger) (ociImage, error) {
	if err := validateGitHost(spec.RepositoryURL); err != nil {
		return ociImage{}, err
	}
	settings, runner, err := configuredRunner()
	if err != nil {
		return ociImage{}, err
	}
	if err := plugins.ValidateRuntimeImage(runner, endpoint.Spec.Host+"/policy-check@sha256:"+strings.Repeat("0", 64)); err != nil {
		return ociImage{}, err
	}
	readyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = runner.Ready(readyContext)
	cancel()
	if err != nil {
		return ociImage{}, err
	}
	directory, err := os.MkdirTemp("", "kubephos-build-")
	if err != nil {
		return ociImage{}, err
	}
	if err := log("info", "Fetching immutable source commit "+spec.Commit[:12]); err != nil {
		return ociImage{}, err
	}
	commands := [][]string{{"init", "--quiet", directory}, {"-C", directory, "remote", "add", "origin", spec.RepositoryURL}, {"-c", "protocol.version=2", "-C", directory, "fetch", "--quiet", "--depth=1", "origin", spec.Commit}, {"-C", directory, "checkout", "--quiet", "--detach", "FETCH_HEAD"}}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", arguments...)
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if output, err := command.CombinedOutput(); err != nil {
			return ociImage{}, commandError("fetch source", output, err)
		}
	}
	output, err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(output)) != spec.Commit {
		return ociImage{}, errors.New("fetched source does not match the validated commit")
	}
	if err := os.RemoveAll(filepath.Join(directory, ".git")); err != nil {
		return ociImage{}, errors.New("could not remove source-control metadata from the build context")
	}
	contextDirectory, dockerfile, err := buildPaths(directory, spec)
	if err != nil {
		return ociImage{}, err
	}
	if err := validateBuildContext(contextDirectory); err != nil {
		return ociImage{}, err
	}
	dockerConfig, err := os.MkdirTemp("", "kubephos-docker-config-")
	if err != nil {
		os.RemoveAll(directory)
		return ociImage{}, err
	}
	repository := credential.Spec.Project + "/" + spec.ImageName
	taggedReference := endpoint.Spec.Host + "/" + repository + ":" + tag(spec)
	defer func() {
		removeLocalImage(settings, dockerConfig, taggedReference)
		os.RemoveAll(dockerConfig)
		os.RemoveAll(directory)
	}()
	registryFiles, err := prepareRegistryClient(endpoint)
	if err != nil {
		return ociImage{}, err
	}
	defer os.RemoveAll(registryFiles.root)
	if err := registryLogin(ctx, registryFiles, endpoint, credential); err != nil {
		return ociImage{}, err
	}
	if err := log("info", "Building "+taggedReference+" on the dedicated rootless executor"); err != nil {
		return ociImage{}, err
	}
	if err := runLogged(ctx, dockerArguments(settings, "build", "--pull", "--label", "io.kubephos.source="+spec.Commit, "--file", dockerfile, "--tag", taggedReference, contextDirectory), dockerConfig, log); err != nil {
		return ociImage{}, err
	}
	if err := log("info", "Publishing image to the managed registry"); err != nil {
		return ociImage{}, err
	}
	archive := filepath.Join(registryFiles.root, "image.tar")
	if output, err := exec.CommandContext(ctx, "docker", dockerArguments(settings, "save", "--output", archive, taggedReference)...).CombinedOutput(); err != nil {
		return ociImage{}, commandError("export built image", output, err)
	}
	if err := runLoggedProgram(ctx, "skopeo", []string{"copy", "--retry-times", "3", "--authfile", registryFiles.authFile, "--dest-cert-dir", registryFiles.certDir, "docker-archive:" + archive, "docker://" + taggedReference}, nil, log); err != nil {
		return ociImage{}, err
	}
	digest, err := repositoryDigest(ctx, registryFiles, taggedReference)
	if err != nil {
		return ociImage{}, err
	}
	image := ociImage{
		APIVersion: artifactAPI, Kind: "OCIImage", Metadata: ociImageMetadata{Name: spec.ImageName, Version: tag(spec)},
		Spec: ociImageSpec{Reference: endpoint.Spec.Host + "/" + repository + "@" + digest, Registry: endpoint.Spec.Host, Repository: repository, Tag: tag(spec), Digest: digest, Source: ociImageSource{RepositoryURL: spec.RepositoryURL, Commit: spec.Commit, ContextPath: spec.ContextPath, Dockerfile: spec.Dockerfile}},
	}
	if err := log("info", "Published immutable image "+image.Spec.Reference); err != nil {
		return ociImage{}, err
	}
	return image, nil
}

func (systemBuildRunner) Verify(ctx context.Context, image ociImage, endpoint registryEndpoint, credential registryCredential, log plugins.Logger) error {
	if err := registryManifest(ctx, image, endpoint, credential); err != nil {
		return err
	}
	return log("info", "Registry returned the exact published manifest digest")
}

func (systemBuildRunner) Cleanup(_ context.Context, _ Spec, _ registryEndpoint, _ registryCredential, log plugins.Logger) error {
	return log("info", "The ephemeral source and local builder image are removed by the build runtime")
}

func configuredRunner() (dockerSettings, plugins.ContainerRunner, error) {
	settings := dockerSettings{host: strings.TrimSpace(os.Getenv("KUBEPHOS_PLUGIN_RUNTIME_HOST")), ca: strings.TrimSpace(os.Getenv("KUBEPHOS_PLUGIN_RUNTIME_CA")), cert: strings.TrimSpace(os.Getenv("KUBEPHOS_PLUGIN_RUNTIME_CERT")), key: strings.TrimSpace(os.Getenv("KUBEPHOS_PLUGIN_RUNTIME_KEY"))}
	for _, registry := range strings.Split(os.Getenv("KUBEPHOS_PLUGIN_ALLOWED_REGISTRIES"), ",") {
		if registry = strings.TrimSpace(registry); registry != "" {
			settings.registries = append(settings.registries, registry)
		}
	}
	runner := plugins.NewDockerRunner(settings.host, settings.ca, settings.cert, settings.key, settings.registries...)
	if runner == nil {
		return settings, nil, errors.New("dedicated OCI build executor is not configured")
	}
	return settings, runner, nil
}

func validateGitHost(repositoryURL string) error {
	parsed, err := url.Parse(repositoryURL)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("source repository URL is invalid")
	}
	allowed := map[string]bool{"github.com": true}
	if configured := strings.TrimSpace(os.Getenv("KUBEPHOS_BUILD_ALLOWED_GIT_HOSTS")); configured != "" {
		allowed = map[string]bool{}
		for _, host := range strings.Split(configured, ",") {
			if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
				allowed[host] = true
			}
		}
	}
	host := strings.ToLower(parsed.Host)
	if !allowed[host] {
		values := make([]string, 0, len(allowed))
		for value := range allowed {
			values = append(values, value)
		}
		sort.Strings(values)
		return fmt.Errorf("Git host %q is not allowed; allowed hosts: %s", host, strings.Join(values, ", "))
	}
	return nil
}

func buildPaths(root string, spec Spec) (string, string, error) {
	contextDirectory := root
	if spec.ContextPath != "." {
		contextDirectory = filepath.Join(root, filepath.FromSlash(spec.ContextPath))
	}
	resolvedContext, err := filepath.EvalSymlinks(contextDirectory)
	if err != nil {
		return "", "", errors.New("build context does not exist")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || (resolvedContext != resolvedRoot && !strings.HasPrefix(resolvedContext, resolvedRoot+string(os.PathSeparator))) {
		return "", "", errors.New("build context escapes the source checkout")
	}
	info, err := os.Stat(resolvedContext)
	if err != nil || !info.IsDir() {
		return "", "", errors.New("build context is not a directory")
	}
	dockerfile := filepath.Join(resolvedContext, filepath.FromSlash(spec.Dockerfile))
	resolvedDockerfile, err := filepath.EvalSymlinks(dockerfile)
	if err != nil || !strings.HasPrefix(resolvedDockerfile, resolvedContext+string(os.PathSeparator)) {
		return "", "", errors.New("Dockerfile is unavailable or escapes the build context")
	}
	info, err = os.Stat(resolvedDockerfile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", "", errors.New("Dockerfile must be a regular file no larger than 1 MiB")
	}
	return resolvedContext, resolvedDockerfile, nil
}

func validateBuildContext(root string) error {
	var bytesTotal int64
	files := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files++
		if files > buildContextFileLimit {
			return errors.New("build context contains too many files")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			bytesTotal += info.Size()
			if bytesTotal > buildContextByteLimit {
				return errors.New("build context exceeds 2 GiB")
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("validate build context: %w", err)
	}
	return nil
}

func dockerArguments(settings dockerSettings, arguments ...string) []string {
	connection := []string{"--host", settings.host, "--tlsverify", "--tlscacert", settings.ca, "--tlscert", settings.cert, "--tlskey", settings.key}
	return append(connection, arguments...)
}

func prepareRegistryClient(endpoint registryEndpoint) (registryClientFiles, error) {
	root, err := os.MkdirTemp("", "kubephos-registry-")
	if err != nil {
		return registryClientFiles{}, err
	}
	files := registryClientFiles{root: root, certDir: filepath.Join(root, "certs"), authFile: filepath.Join(root, "auth.json")}
	if err := os.Mkdir(files.certDir, 0700); err != nil {
		os.RemoveAll(root)
		return registryClientFiles{}, err
	}
	if err := os.WriteFile(filepath.Join(files.certDir, "ca.crt"), []byte(endpoint.Spec.CABundle), 0600); err != nil {
		os.RemoveAll(root)
		return registryClientFiles{}, err
	}
	return files, nil
}

func registryLogin(ctx context.Context, files registryClientFiles, endpoint registryEndpoint, credential registryCredential) error {
	command := exec.CommandContext(ctx, "skopeo", "login", "--tls-verify=true", "--cert-dir", files.certDir, "--authfile", files.authFile, "--username", credential.Spec.Username, "--password-stdin", endpoint.Spec.Host)
	command.Stdin = strings.NewReader(credential.Spec.Password)
	output, err := command.CombinedOutput()
	if err != nil {
		return commandError("registry login", output, err)
	}
	return nil
}

func runLogged(ctx context.Context, arguments []string, dockerConfig string, log plugins.Logger) error {
	return runLoggedProgram(ctx, "docker", arguments, []string{"DOCKER_CONFIG=" + dockerConfig}, log)
}

func runLoggedProgram(ctx context.Context, program string, arguments, environment []string, log plugins.Logger) error {
	command := exec.CommandContext(ctx, program, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	command.WaitDelay = 5 * time.Second
	command.Env = append(os.Environ(), environment...)
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		return err
	}
	writer.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 16*1024)
	events := 0
	for scanner.Scan() {
		if events < 5000 {
			if err := log("info", scanner.Text()); err != nil {
				reader.Close()
				_ = command.Process.Kill()
				_ = command.Wait()
				return err
			}
			events++
		}
	}
	reader.Close()
	if err := scanner.Err(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return errors.New("build output exceeded the line limit")
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("%s command failed: %w", program, err)
	}
	return nil
}

func repositoryDigest(ctx context.Context, files registryClientFiles, taggedReference string) (string, error) {
	command := exec.CommandContext(ctx, "skopeo", "inspect", "--authfile", files.authFile, "--cert-dir", files.certDir, "--format", "{{.Digest}}", "docker://"+taggedReference)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", commandError("inspect published image", output, err)
	}
	digest := strings.TrimSpace(string(output))
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(digest) {
		return "", errors.New("published image returned an invalid digest")
	}
	return digest, nil
}

func removeLocalImage(settings dockerSettings, dockerConfig, reference string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", dockerArguments(settings, "image", "rm", "--force", reference)...)
	command.Env = append(os.Environ(), "DOCKER_CONFIG="+dockerConfig)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	_ = command.Run()
}

func registryPing(ctx context.Context, endpoint registryEndpoint, credential registryCredential, _ dockerSettings) error {
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, strings.TrimSuffix(endpoint.Spec.URL, "/")+"/v2/", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(credential.Spec.Username, credential.Spec.Password)
	client, err := registryHTTPClient(endpoint)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized && strings.HasPrefix(strings.ToLower(response.Header.Get("WWW-Authenticate")), "bearer ") {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("registry API returned %s", response.Status)
	}
	return nil
}

func registryManifest(ctx context.Context, image ociImage, endpoint registryEndpoint, credential registryCredential) error {
	files, err := prepareRegistryClient(endpoint)
	if err != nil {
		return err
	}
	defer os.RemoveAll(files.root)
	if err := registryLogin(ctx, files, endpoint, credential); err != nil {
		return err
	}
	digest, err := repositoryDigest(ctx, files, image.Spec.Reference)
	if err != nil {
		return err
	}
	if digest != image.Spec.Digest {
		return errors.New("registry manifest digest does not match the build result")
	}
	return nil
}

func registryHTTPClient(endpoint registryEndpoint) (*http.Client, error) {
	pool, err := registryCertPool(endpoint.Spec.CABundle)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func registryCertPool(bundle string) (*x509.CertPool, error) {
	block, rest := pem.Decode([]byte(bundle))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("registry certificate authority must contain exactly one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA || time.Now().Before(certificate.NotBefore) || !time.Now().Before(certificate.NotAfter) {
		return nil, errors.New("registry certificate authority is not valid and active")
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if strings.TrimSpace(bundle) == "" || !pool.AppendCertsFromPEM([]byte(bundle)) {
		return nil, errors.New("registry certificate authority is invalid")
	}
	return pool, nil
}

func commandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if len(message) > 1024 {
		message = message[len(message)-1024:]
	}
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("%s: %s", action, message)
}
