package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
	loadsession "kubephos.dev/kubephos/plugins/load-session/runtime"
)

type unavailableRunner struct{}

type loadSessionPluginAdapter struct {
	plugin loadsession.Plugin
}

func (adapter loadSessionPluginAdapter) Manifest() plugins.Manifest {
	return adapter.plugin.Manifest()
}

func (adapter loadSessionPluginAdapter) Validate(ctx context.Context, input json.RawMessage) domain.ValidationReport {
	return adapter.plugin.Validate(ctx, loadsession.Invocation{Input: input})
}

func (adapter loadSessionPluginAdapter) Plan(ctx context.Context, input json.RawMessage) (domain.Plan, error) {
	return adapter.plugin.Plan(ctx, input)
}

func (adapter loadSessionPluginAdapter) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	return adapter.plugin.Precheck(ctx, step, log)
}

func (adapter loadSessionPluginAdapter) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	return adapter.plugin.Execute(ctx, step, log)
}

func (adapter loadSessionPluginAdapter) Verify(ctx context.Context, step domain.PlanStep, result json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	return adapter.plugin.Verify(ctx, step, result, log)
}

func (adapter loadSessionPluginAdapter) Cleanup(ctx context.Context, step domain.PlanStep, result json.RawMessage, log plugins.Logger) error {
	return adapter.plugin.Cleanup(ctx, step, result, log)
}

func TestSecurityHeadersAllowLocalChartRasterization(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, request)
	if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "img-src 'self' data: blob:") {
		t.Fatalf("chart rasterization is blocked by CSP %q", policy)
	}
}

func TestValidateExperimentLoadConfigurationRejectsMismatchedGeographicDuration(t *testing.T) {
	configuration := json.RawMessage(`{
		"scenarioId":"default-journey","action":"start","interactive":false,"replicas":1,"maxUsers":1000,"spawnRate":10,
		"steps":[
			{"type":"constant","durationSeconds":600,"rps":10},
			{"type":"constant","durationSeconds":600,"rps":10},
			{"type":"constant","durationSeconds":600,"rps":10}
		],
		"geographicSteps":[
			{"type":"constant","durationSeconds":588,"weights":{"zone-a":1}},
			{"type":"constant","durationSeconds":600,"weights":{"zone-b":1}},
			{"type":"constant","durationSeconds":600,"weights":{"zone-c":1}}
		]
	}`)
	plugin := loadSessionPluginAdapter{plugin: loadsession.Plugin{}}
	report, err := validateExperimentComponentConfiguration(context.Background(), plugin, configuration, "app:dev.kubephos.mubench@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Issues) == 0 || !strings.Contains(report.Issues[0].Message, "29m48s") || !strings.Contains(report.Issues[0].Message, "30m0s") {
		t.Fatalf("expected the geographic duration mismatch, got %#v", report)
	}
	configuration = json.RawMessage(strings.Replace(string(configuration), `"durationSeconds":588`, `"durationSeconds":600`, 1))
	report, err = validateExperimentComponentConfiguration(context.Background(), plugin, configuration, "app:dev.kubephos.mubench@1.0.0")
	if err != nil || !report.Valid {
		t.Fatalf("expected matching timelines to validate, got %#v, %v", report, err)
	}
}

func TestNormalizeExternalLoadDestination(t *testing.T) {
	input := externalLoadDestinationInput{
		TargetURL:    "  https://api.example.test/v1  ",
		RuntimeImage: "  locustio/locust:2.42.6  ",
		Script:       "  from locust import HttpUser\n  ",
		Parameters:   map[string]string{"tenant": "demo"},
	}
	normalized, reference, digest, err := normalizeExternalLoadDestination(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.TargetURL != "https://api.example.test/v1" || normalized.RuntimeImage != "locustio/locust:2.42.6" || normalized.Script != "from locust import HttpUser" {
		t.Fatalf("unexpected normalized destination %#v", normalized)
	}
	if !strings.HasPrefix(reference, "external:") || !strings.HasPrefix(digest, "sha256:") || strings.TrimPrefix(reference, "external:") != strings.TrimPrefix(digest, "sha256:") {
		t.Fatalf("external identity is inconsistent: %q %q", reference, digest)
	}
	_, secondReference, secondDigest, err := normalizeExternalLoadDestination(normalized)
	if err != nil || secondReference != reference || secondDigest != digest {
		t.Fatalf("external identity is not stable: %q %q %v", secondReference, secondDigest, err)
	}
}

func TestNormalizeExternalLoadDestinationRejectsUnsafeInputs(t *testing.T) {
	cases := []externalLoadDestinationInput{
		{TargetURL: "ftp://api.example.test", RuntimeImage: "locust:latest", Script: "import locust"},
		{TargetURL: "https://user:secret@api.example.test", RuntimeImage: "locust:latest", Script: "import locust"},
		{TargetURL: "https://api.example.test", RuntimeImage: "locust:latest", Script: "class User: pass"},
		{TargetURL: "https://api.example.test", RuntimeImage: "locust:latest", Script: "import locust", Parameters: map[string]string{"tenant": "a", "TENANT": "b"}},
	}
	for _, value := range cases {
		if _, _, _, err := normalizeExternalLoadDestination(value); err == nil {
			t.Fatalf("unsafe external destination accepted: %#v", value)
		}
	}
}

func TestExternalDestinationPipelineOrdersArtifactProducersBeforeConsumers(t *testing.T) {
	artifactProperty := func(artifactType string) map[string]any {
		return map[string]any{"type": "string", "format": "kubephos-artifact-ref", "x-kubephos-artifact-type": artifactType, "x-kubephos-artifact-version": "v1alpha1"}
	}
	manifest := func(id, capability string, inputs, outputs []string) plugins.Manifest {
		properties := map[string]any{}
		for _, input := range inputs {
			properties[strings.ToLower(input[:1])+input[1:]+"Ref"] = artifactProperty(input)
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": properties})
		value := plugins.Manifest{ID: id, Name: id, Version: "1.0.0", Schema: schema, Capabilities: []string{capability}}
		for _, input := range inputs {
			value.ArtifactInputs = append(value.ArtifactInputs, domain.ArtifactContract{Type: input, Version: "v1alpha1"})
		}
		for _, output := range outputs {
			value.ArtifactOutputs = append(value.ArtifactOutputs, domain.ArtifactContract{Type: output, Version: "v1alpha1"})
		}
		return value
	}
	prepare := manifest("external-target", "loads.external.prepare", []string{"ClusterConnection"}, []string{"ApplicationDeployment", "LoadScenarioSet"})
	load := manifest("load", "load.session.control", []string{"ClusterConnection", "ApplicationDeployment", "LoadScenarioSet"}, []string{"LoadSession"})
	metrics := manifest("metrics", "metrics.timeseries.collect", []string{"ClusterConnection", "ApplicationDeployment", "ObservabilityCapability", "LoadSession"}, []string{"TimeSeriesDataset"})
	server := &Server{registry: plugins.NewRegistry(manifestOnlyPlugin{prepare}, manifestOnlyPlugin{load}, manifestOnlyPlugin{metrics})}
	definition := experimentConfigurationDefinitionInput{
		DestinationMode:     "external",
		ExternalDestination: externalLoadDestinationInput{TargetURL: "https://api.example.test", RuntimeImage: "locust:latest", Script: "import locust"},
		Components: []experimentConfigurationComponentInput{
			{ID: "metrics", PluginID: "metrics", Capability: "metrics.timeseries.collect", Configuration: json.RawMessage(`{}`)},
			{ID: "load", PluginID: "load", Capability: "load.session.control", Configuration: json.RawMessage(`{}`)},
		},
	}
	pipeline, failure := server.externalDestinationPipeline(domain.ExperimentConfiguration{ApplicationRef: "external:test"}, definition, domain.Artifact{ID: "art_cluster"}, domain.Artifact{ID: "art_observability"})
	if failure != nil {
		t.Fatalf("pipeline generation failed: %#v", failure)
	}
	if len(pipeline.Stages) != 3 || pipeline.Stages[0].ID != "external-destination" || pipeline.Stages[1].ID != "capability-2" || pipeline.Stages[2].ID != "capability-1" {
		t.Fatalf("unexpected external pipeline order %#v", pipeline.Stages)
	}
	positions := map[string]int{}
	for index, stage := range pipeline.Stages {
		positions[stage.ID] = index
		for _, binding := range stage.Bindings {
			if source, ok := positions[binding.FromStage]; !ok || source >= index {
				t.Fatalf("stage %q has a non-earlier binding %#v", stage.ID, binding)
			}
		}
	}
	if pipeline.Result.Stage != "capability-1" || pipeline.Result.Type != "TimeSeriesDataset" {
		t.Fatalf("unexpected external pipeline result %#v", pipeline.Result)
	}
}

func TestManagedMachineCapacityProfiles(t *testing.T) {
	defaults := managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80}
	capacity := managedCapacityDefaults(managedMachineCapacity{Cores: 6}, defaults)
	if capacity.Cores != 6 || capacity.MemoryMiB != 8192 || capacity.DiskGiB != 80 || !validManagedCapacity(capacity, 60) {
		t.Fatalf("unexpected capacity defaults %#v", capacity)
	}
	profiles := appendManagedMachineProfiles(nil, 3, capacity)
	if len(profiles) != 3 || profiles[2] != capacity {
		t.Fatalf("unexpected machine profiles %#v", profiles)
	}
	if validManagedCapacity(capacity, 100) {
		t.Fatal("capacity smaller than the template disk was accepted")
	}
}

func TestDecodeApplicationPreviewIncludesVerifiedLoadScenarios(t *testing.T) {
	result := []byte(`{"manifestSet":{"metadata":{"digest":"sha256:manifest"},"spec":{"renderer":"plain-yaml","content":"kind: Deployment\n"}},"loadScenarioSet":{"metadata":{"digest":"sha256:scenarios"},"spec":{"scenarios":[{"id":"journey","engine":"locust","targetEndpoint":"http","runtimeImage":"locustio/locust:2.42.6","script":"from locust import HttpUser\n","scriptDigest":"sha256:script"}]}}}`)
	preview, err := decodeApplicationPreview(result, "app:dev.example@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if preview.ApplicationRef != "app:dev.example@1.0.0" || preview.Content == "" || len(preview.LoadScenarios) != 1 || preview.LoadScenarios[0].Script == "" {
		t.Fatalf("unexpected application preview %#v", preview)
	}
}

func TestDecodeApplicationPreviewRejectsUnverifiedLoadScenario(t *testing.T) {
	result := []byte(`{"manifestSet":{"metadata":{"digest":"sha256:manifest"},"spec":{"renderer":"plain-yaml","content":"kind: Deployment\n"}},"loadScenarioSet":{"metadata":{"digest":"sha256:scenarios"},"spec":{"scenarios":[{"id":"journey","engine":"locust","targetEndpoint":"http","runtimeImage":"locustio/locust:2.42.6","script":"from locust import HttpUser\n"}]}}}`)
	if _, err := decodeApplicationPreview(result, "app:dev.example@1.0.0"); err == nil {
		t.Fatal("expected invalid load scenario rejection")
	}
}

func TestValidateApplicationPreviewRejectsAnotherApplication(t *testing.T) {
	preview := applicationPreview{ApplicationRef: "app:dev.example@1.0.0", Digest: "sha256:manifest", Content: "kind: Deployment\n", LoadScenarioDigest: "sha256:scenarios"}
	if err := validateApplicationPreview(preview, "app:dev.other@1.0.0"); err == nil {
		t.Fatal("expected cached preview identity rejection")
	}
}

func TestResourceAccessPairsEndpointsAndProtectedCredentials(t *testing.T) {
	documents := []json.RawMessage{
		json.RawMessage(`{"kind":"RegistryEndpoint","metadata":{"name":"managed-registry"},"spec":{"host":"192.168.1.10","url":"https://192.168.1.10"}}`),
		json.RawMessage(`{"kind":"RegistryCredential","metadata":{"name":"harbor-admin","role":"management"},"spec":{"server":"192.168.1.10","username":"admin","password":"harbor-secret"}}`),
		json.RawMessage(`{"kind":"ObservabilityCapability","spec":{"endpoints":[{"name":"grafana","serviceName":"grafana-service","scheme":"http","nodePort":32000},{"name":"prometheus","serviceName":"prometheus-service","scheme":"http","nodePort":32090}]}}`),
		json.RawMessage(`{"kind":"ServiceCredential","metadata":{"name":"grafana-admin","role":"management"},"spec":{"service":"grafana-service","username":"admin","password":"grafana-secret"}}`),
		json.RawMessage(`{"kind":"ManagedPlatformCapability","spec":{"endpoints":[{"name":"kiali","service":"kiali","scheme":"http","nodePort":30001},{"name":"headlamp","service":"kubephos-headlamp","scheme":"http","nodePort":30003}]}}`),
		json.RawMessage(`{"kind":"ServiceCredential","metadata":{"name":"headlamp-admin","role":"cluster-admin"},"spec":{"service":"kubephos-headlamp","username":"token","password":"headlamp-secret"}}`),
	}
	items := resourceAccessFromDocuments(documents, "192.168.1.20")
	if len(items) != 5 {
		t.Fatalf("expected five access endpoints, got %#v", items)
	}
	byName := map[string]resourceAccessEndpoint{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if byName["Managed Registry"].Credentials[0].Password != "harbor-secret" || byName["Grafana"].Credentials[0].Password != "grafana-secret" {
		t.Fatalf("credentials were not paired with endpoints: %#v", items)
	}
	if byName["Prometheus"].URL != "http://192.168.1.20:32090" || len(byName["Prometheus"].Credentials) != 0 || byName["Kiali"].URL != "http://192.168.1.20:30001/kiali" {
		t.Fatalf("dashboard access is incorrect: %#v", items)
	}
	if byName["Headlamp"].URL != "http://192.168.1.20:30003" || byName["Headlamp"].Credentials[0].Name != "headlamp-admin" || byName["Headlamp"].Credentials[0].Role != "cluster-admin" || byName["Headlamp"].Credentials[0].Password != "headlamp-secret" {
		t.Fatalf("Headlamp access is incorrect: %#v", items)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"credentials":null`)) {
		t.Fatalf("credential-free endpoints must serialize an empty array: %s", encoded)
	}
}

func TestLegacyHeadlampAccessDocumentsPairEndpointAndToken(t *testing.T) {
	items := resourceAccessFromDocuments(headlampAccessDocuments("viewer-token"), "192.0.2.10")
	if len(items) != 1 || items[0].Name != "Headlamp" || items[0].URL != "http://192.0.2.10:30003" || len(items[0].Credentials) != 1 {
		t.Fatalf("unexpected Headlamp access %#v", items)
	}
	credential := items[0].Credentials[0]
	if credential.Name != "headlamp-admin" || credential.Role != "cluster-admin" || credential.Username != "token" || credential.Password != "viewer-token" {
		t.Fatalf("unexpected Headlamp credential %#v", credential)
	}
	if !hasHeadlampCredential(headlampAccessDocuments("viewer-token")) || hasHeadlampCredential(headlampAccessDocuments("")) {
		t.Fatal("Headlamp credential detection does not reject an empty token")
	}
}

func TestClusterConnectionKubeconfigRequiresExpectedKind(t *testing.T) {
	if value := clusterConnectionKubeconfig([]byte(`{"kind":"ClusterConnection","spec":{"kubeconfig":"  verified-config  "}}`)); value != "verified-config" {
		t.Fatalf("unexpected kubeconfig %q", value)
	}
	if value := clusterConnectionKubeconfig([]byte(`{"kind":"Other","spec":{"kubeconfig":"secret"}}`)); value != "" {
		t.Fatalf("accepted kubeconfig from another artifact kind: %q", value)
	}
}

func TestManagedAccessRevealIsAudited(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/managed-resources/cluster_one/access", strings.NewReader(`{}`))
	action, targetType, targetID, audited := auditTarget(request)
	if !audited || action != "managed-resource.access.reveal" || targetType != "managed-resource" || targetID != "cluster_one" {
		t.Fatalf("unexpected audit target %q %q %q %v", action, targetType, targetID, audited)
	}
}

func TestExperimentDeletionIsAudited(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/experiments/exp_one", nil)
	action, targetType, targetID, audited := auditTarget(request)
	if !audited || action != "experiment.delete" || targetType != "experiment" || targetID != "exp_one" {
		t.Fatalf("unexpected audit target %q %q %q %v", action, targetType, targetID, audited)
	}
}

func (unavailableRunner) Ready(context.Context) error {
	return errors.New("executor offline")
}
func (unavailableRunner) Run(context.Context, string, string, []byte, bool, plugins.Logger) ([]byte, []string, error) {
	return nil, nil, errors.New("executor offline")
}

type policyRunner struct {
	err error
}

type configurableRunner struct {
	profile domain.PluginRuntimeProfile
	err     error
}

func (r *configurableRunner) Ready(context.Context) error { return r.err }
func (r *configurableRunner) Run(context.Context, string, string, []byte, bool, plugins.Logger) ([]byte, []string, error) {
	return nil, nil, r.err
}
func (r *configurableRunner) State(context.Context) (string, string) {
	if r.err != nil {
		return "unavailable", r.err.Error()
	}
	return "healthy", "ready"
}
func (r *configurableRunner) Profile(context.Context) (domain.PluginRuntimeProfile, error) {
	return r.profile, r.err
}
func (r *configurableRunner) ValidateProfile(_ context.Context, profile domain.PluginRuntimeProfile) error {
	r.profile = profile
	return r.err
}
func (r *configurableRunner) ActivateProfile(_ context.Context, profile domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error) {
	r.profile = profile
	return profile, r.err
}
func (r *configurableRunner) DeactivateProfile(context.Context) error { return r.err }

func (p policyRunner) Ready(context.Context) error {
	return nil
}

func (p policyRunner) Run(context.Context, string, string, []byte, bool, plugins.Logger) ([]byte, []string, error) {
	return nil, nil, nil
}

func (p policyRunner) ValidateImage(string) error {
	return p.err
}

func TestRuntimeHealthDistinguishesDisabledAndUnavailable(t *testing.T) {
	disabled := &Server{}
	if state, _ := disabled.runtimeHealth(context.Background()); state != "disabled" {
		t.Fatalf("expected disabled runtime, got %s", state)
	}
	unavailable := &Server{runtime: unavailableRunner{}}
	if state, message := unavailable.runtimeHealth(context.Background()); state != "unavailable" || message != "executor offline" {
		t.Fatalf("unexpected runtime health: %s %s", state, message)
	}
}

func TestTerminalSessionLimits(t *testing.T) {
	server := &Server{terminalActive: map[string]int{}}
	for index := 0; index < 4; index++ {
		if !server.acquireTerminal("user-one") {
			t.Fatalf("session %d should be accepted", index+1)
		}
	}
	if server.acquireTerminal("user-one") {
		t.Fatal("fifth session for one user must be rejected")
	}
	server.releaseTerminal("user-one")
	if !server.acquireTerminal("user-one") {
		t.Fatal("released capacity should be reusable")
	}
}

func TestInspectPluginReturnsStaticPreviewWithoutExecutor(t *testing.T) {
	digest := strings.Repeat("e", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.preview
  name: Preview
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  permissions: [network.egress]
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
`
	payload, _ := json.Marshal(map[string]string{"descriptor": descriptor})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/inspect", strings.NewReader(string(payload)))
	response := httptest.NewRecorder()
	server := &Server{registry: plugins.NewRegistry()}
	server.inspectPlugin(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected preview, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Manifest          plugins.Manifest `json:"manifest"`
		ExecutorAvailable bool             `json:"executorAvailable"`
		PolicyAccepted    bool             `json:"policyAccepted"`
		PolicyMessage     string           `json:"policyMessage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Manifest.ID != "dev.example.preview" || body.ExecutorAvailable || body.PolicyAccepted || body.PolicyMessage == "" {
		t.Fatalf("unexpected preview: %#v", body)
	}
}

func TestInspectPluginReportsImagePolicy(t *testing.T) {
	digest := strings.Repeat("e", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.policy-preview
  name: Policy preview
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
`
	payload, _ := json.Marshal(map[string]string{"descriptor": descriptor})
	for name, runtime := range map[string]policyRunner{"accepted": {}, "rejected": {err: errors.New("registry denied")}} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/inspect", strings.NewReader(string(payload)))
			response := httptest.NewRecorder()
			server := &Server{registry: plugins.NewRegistry(), runtime: runtime}
			server.inspectPlugin(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("expected preview, got %d: %s", response.Code, response.Body.String())
			}
			var body struct {
				ExecutorAvailable bool   `json:"executorAvailable"`
				PolicyAccepted    bool   `json:"policyAccepted"`
				PolicyMessage     string `json:"policyMessage"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.ExecutorAvailable || body.PolicyAccepted != (runtime.err == nil) || body.PolicyMessage == "" {
				t.Fatalf("unexpected policy preview: %#v", body)
			}
		})
	}
}

func TestValidatePluginRuntimeReturnsHealthGateResult(t *testing.T) {
	profile := domain.PluginRuntimeProfile{EndpointArtifactID: "art_endpoint", CredentialArtifactID: "art_credential", Registries: []domain.PluginRuntimeRegistry{{EndpointArtifactID: "art_registry", CredentialArtifactID: "art_registry_credential"}}}
	payload, _ := json.Marshal(profile)
	for name, validationErr := range map[string]error{"healthy": nil, "rejected": errors.New("registry TLS failed")} {
		t.Run(name, func(t *testing.T) {
			runtime := &configurableRunner{err: validationErr}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/plugin-runtime/validate", strings.NewReader(string(payload)))
			response := httptest.NewRecorder()
			server := &Server{runtime: runtime}
			server.validatePluginRuntime(response, request)
			expected := http.StatusOK
			if validationErr != nil {
				expected = http.StatusUnprocessableEntity
			}
			if response.Code != expected {
				t.Fatalf("expected %d, got %d: %s", expected, response.Code, response.Body.String())
			}
		})
	}
}

func TestResolvedPlanHashIsDeterministic(t *testing.T) {
	manifest := plugins.Manifest{ID: "test", Version: "1.0.0"}
	spec := json.RawMessage(`{"value":1}`)
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "one", Name: "One", Input: json.RawMessage(`{}`)}}}
	first, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected stable hash, got %s and %s", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("expected SHA-256 hex, got %d characters", len(first))
	}
	manifest.Runtime.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	changed, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil || changed == first {
		t.Fatalf("plugin digest did not change hash: %q", changed)
	}
}

func TestArtifactFilenamePreservesOrDerivesExtension(t *testing.T) {
	if value := artifactFilename(domain.Artifact{Name: "comparison", MediaType: "image/svg+xml"}); value != "comparison.svg" {
		t.Fatalf("unexpected derived filename %q", value)
	}
	if value := artifactFilename(domain.Artifact{Name: "raw.csv", MediaType: "text/csv"}); value != "raw.csv" {
		t.Fatalf("unexpected preserved filename %q", value)
	}
}

func TestNormalizeAndValidateExperimentInput(t *testing.T) {
	input := createExperimentInput{
		WorkspaceID: " ws_dev ",
		Name:        " Scheduler comparison ",
		Variants: []createExperimentVariantInput{
			{Name: " Default ", TrialArtifactIDs: []string{" art_one ", "art_two"}},
			{Name: " Custom", Configuration: json.RawMessage(`{"weight":2}`), TrialArtifactIDs: []string{"art_three"}},
		},
	}
	if err := normalizeAndValidateExperimentInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.WorkspaceID != "ws_dev" || input.Name != "Scheduler comparison" || input.Variants[0].Name != "Default" || input.Variants[0].TrialArtifactIDs[0] != "art_one" {
		t.Fatalf("input was not normalized: %#v", input)
	}
	if string(input.Variants[0].Configuration) != "{}" {
		t.Fatalf("expected default configuration, got %s", input.Variants[0].Configuration)
	}
}

func TestNormalizeAndValidateExperimentInputRejectsInvalidComparisons(t *testing.T) {
	tests := []struct {
		name  string
		input createExperimentInput
	}{
		{name: "one variant", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one", TrialArtifactIDs: []string{"art"}}}}},
		{name: "duplicate names", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "same", TrialArtifactIDs: []string{"one"}}, {Name: "Same", TrialArtifactIDs: []string{"two"}}}}},
		{name: "no trials", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one"}, {Name: "two", TrialArtifactIDs: []string{"two"}}}}},
		{name: "non object configuration", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one", Configuration: json.RawMessage(`[]`), TrialArtifactIDs: []string{"one"}}, {Name: "two", TrialArtifactIDs: []string{"two"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := normalizeAndValidateExperimentInput(&test.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNormalizePipelineExperimentInput(t *testing.T) {
	input := createPipelineExperimentInput{
		WorkspaceID: " ws_dev ", Name: " Scheduler comparison ", Repetitions: 3,
		Variants: []createPipelineExperimentVariantInput{
			{Name: " Default ", PipelineID: " pipe_default "},
			{Name: " Secondary ", PipelineID: " pipe_secondary "},
		},
	}
	if err := normalizePipelineExperimentInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.WorkspaceID != "ws_dev" || input.Name != "Scheduler comparison" || input.Variants[1].PipelineID != "pipe_secondary" {
		t.Fatalf("input was not normalized: %#v", input)
	}
}

func TestNormalizePipelineExperimentInputRejectsUnsafeFanout(t *testing.T) {
	tests := []createPipelineExperimentInput{
		{WorkspaceID: "ws", Name: "one", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "only", PipelineID: "pipe_one"}}},
		{WorkspaceID: "ws", Name: "duplicate", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "same", PipelineID: "pipe_one"}, {Name: "Same", PipelineID: "pipe_two"}}},
		{WorkspaceID: "ws", Name: "pipeline", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "one", PipelineID: "pipe_one"}, {Name: "two", PipelineID: "pipe_one"}}},
		{WorkspaceID: "ws", Name: "fanout", Repetitions: 10, Variants: []createPipelineExperimentVariantInput{{Name: "one", PipelineID: "pipe_one"}, {Name: "two", PipelineID: "pipe_two"}, {Name: "three", PipelineID: "pipe_three"}, {Name: "four", PipelineID: "pipe_four"}, {Name: "five", PipelineID: "pipe_five"}}},
	}
	for _, input := range tests {
		if err := normalizePipelineExperimentInput(&input); err == nil {
			t.Fatalf("expected rejection for %#v", input)
		}
	}
}

func TestExperimentComponentSchemaRemovesRuntimeInputs(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","required":["cluster","mode"],"additionalProperties":false,"properties":{"cluster":{"type":"string","format":"kubephos-artifact-ref"},"mode":{"type":"string","enum":["safe","fast"]},"optional":{"type":"integer","default":2}}}`)
	filtered, err := experimentComponentSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := schema.Validate(filtered, json.RawMessage(`{"mode":"safe","optional":3}`)); err != nil || len(issues) != 0 {
		t.Fatalf("expected configurable fields to validate: %#v %v", issues, err)
	}
	if issues, err := schema.Validate(filtered, json.RawMessage(`{"cluster":"artifact","mode":"safe"}`)); err != nil || len(issues) == 0 {
		t.Fatalf("expected runtime field to be rejected: %#v %v", issues, err)
	}
}

func TestValidExperimentTargetsRejectsOverlap(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	allowed := map[string]bool{"frontend": true, "checkout": true, "email": true}
	if !validExperimentTargets([]string{"frontend", "checkout"}, []string{"email"}, pattern, allowed) {
		t.Fatal("expected distinct target selection to be valid")
	}
	if validExperimentTargets([]string{"frontend"}, []string{"frontend"}, pattern, allowed) {
		t.Fatal("expected overlapping target selection to be rejected")
	}
	if validExperimentTargets([]string{"unknown"}, nil, pattern, allowed) {
		t.Fatal("expected an unknown application component to be rejected")
	}
}

func TestPipelineResultSensitiveUsesResolvedOutput(t *testing.T) {
	pipeline := domain.Pipeline{
		Definition: domain.PipelineDefinition{Result: domain.PipelineOutput{Stage: "result", Output: "dataset"}},
		Resolution: domain.PipelineResolution{Stages: []domain.ResolvedPipelineStage{{ID: "result", Plan: domain.Plan{Steps: []domain.PlanStep{{Outputs: []domain.ArtifactOutput{{Name: "dataset"}}}}}}}},
	}
	if pipelineResultSensitive(pipeline) {
		t.Fatal("public result was classified as sensitive")
	}
	pipeline.Resolution.Stages[0].Plan.Steps[0].Outputs[0].Sensitive = true
	if !pipelineResultSensitive(pipeline) {
		t.Fatal("sensitive result was not detected")
	}
}

func TestNormalizeScheduledFor(t *testing.T) {
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	immediate, err := normalizeScheduledFor(nil, now)
	if err != nil || !immediate.Equal(now) {
		t.Fatalf("unexpected immediate schedule: %v %v", immediate, err)
	}
	future := now.Add(15 * time.Minute)
	scheduled, err := normalizeScheduledFor(&future, now)
	if err != nil || !scheduled.Equal(future) {
		t.Fatalf("unexpected future schedule: %v %v", scheduled, err)
	}
	past := now.Add(-time.Minute)
	tooFar := now.AddDate(1, 0, 1)
	if _, err := normalizeScheduledFor(&past, now); err == nil {
		t.Fatal("past schedule was accepted")
	}
	if _, err := normalizeScheduledFor(&tooFar, now); err == nil {
		t.Fatal("schedule beyond one year was accepted")
	}
}

type preflightPlugin struct {
	calls            int
	receivedResolved bool
	health           domain.HealthReport
}

type manifestOnlyPlugin struct{ manifest plugins.Manifest }

func (p manifestOnlyPlugin) Manifest() plugins.Manifest { return p.manifest }
func (manifestOnlyPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (manifestOnlyPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (manifestOnlyPlugin) Precheck(context.Context, domain.PlanStep, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{}, nil
}
func (manifestOnlyPlugin) Execute(context.Context, domain.PlanStep, plugins.Logger) (json.RawMessage, error) {
	return nil, nil
}
func (manifestOnlyPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{}, nil
}
func (manifestOnlyPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func (p *preflightPlugin) Manifest() plugins.Manifest { return plugins.Manifest{ID: "test"} }
func (p *preflightPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (p *preflightPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (p *preflightPlugin) Precheck(_ context.Context, step domain.PlanStep, _ plugins.Logger) (domain.HealthReport, error) {
	p.calls++
	p.receivedResolved = len(step.ResolvedInputs) > 0
	return p.health, nil
}
func (p *preflightPlugin) Execute(context.Context, domain.PlanStep, plugins.Logger) (json.RawMessage, error) {
	return nil, nil
}
func (p *preflightPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{}, nil
}
func (p *preflightPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func TestPreflightPlanRunsReachableChecksAndDefersArtifactConsumers(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "first"},
		{ID: "second", ArtifactInputs: []domain.ArtifactInput{{Name: "input"}}},
	}}
	issues := preflightPlan(context.Background(), plugin, plan, nil)
	if plugin.calls != 1 {
		t.Fatalf("expected one reachable preflight, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[0].Level != "info" || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanHydratesExistingArtifactInputs(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{{ID: "consumer", ArtifactInputs: []domain.ArtifactInput{{Name: "input", ArtifactID: "art_one"}}}}}
	hydrated := false
	issues := preflightPlan(context.Background(), plugin, plan, func(_ context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		hydrated = true
		step.ResolvedInputs = map[string]domain.ResolvedArtifact{"input": {ID: "art_one"}}
		return step, nil
	})
	if !hydrated || plugin.calls != 1 || !plugin.receivedResolved {
		t.Fatalf("expected hydrated preflight, got hydrated=%t calls=%d resolved=%t", hydrated, plugin.calls, plugin.receivedResolved)
	}
	if len(issues) != 1 || issues[0].Level != "info" || issues[0].Message != "Non-mutating preflight passed: ready" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanDefersStepsAfterInfrastructureEffects(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "create", Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "machine/1", Kind: "machine", Name: "one"}}},
		{ID: "verify-dependent-state"},
	}}
	issues := preflightPlan(context.Background(), plugin, plan, nil)
	if plugin.calls != 1 {
		t.Fatalf("expected only the first preflight to run, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanDefersStepsAfterGenericMutation(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "install", Mutating: true},
		{ID: "verify-dependent-state"},
	}}
	issues := preflightPlan(context.Background(), plugin, plan, nil)
	if plugin.calls != 1 {
		t.Fatalf("expected only the mutating step preflight to run, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanRejectsUnhealthyStep(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "unreachable"}}
	issues := preflightPlan(context.Background(), plugin, domain.Plan{Steps: []domain.PlanStep{{ID: "first"}}}, nil)
	if len(issues) != 1 || issues[0].Level != "error" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPlanEffectsRequireProvisionCapability(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test"}, plan); err == nil {
		t.Fatal("expected missing capability error")
	}
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	if err := validatePlanEffects(manifest, plan); err != nil {
		t.Fatal(err)
	}
}

func TestPlanEffectsRejectDuplicateCreates(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{
		{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "one"},
		{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "two"},
	}}}}
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	if err := validatePlanEffects(manifest, plan); err == nil {
		t.Fatal("expected duplicate resource create rejection")
	}
}

func TestPlanEffectsRejectIncompleteIdentity(t *testing.T) {
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000"}}}}}
	if err := validatePlanEffects(manifest, plan); err == nil {
		t.Fatal("expected incomplete identity error")
	}
}

func TestDeleteEffectsRequireDeprovisionCapability(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "delete", Name: "Delete", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "delete", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}, plan); err == nil {
		t.Fatal("expected missing deprovision capability error")
	}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.deprovision"}}, plan); err != nil {
		t.Fatal(err)
	}
}

func TestPlanEffectsRequireProviderIdentity(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Capabilities: []string{"infrastructure.provision"}}, plan); err == nil {
		t.Fatal("expected missing provider error")
	}
}

func TestCleanupPlanReversesSuccessfulMutableStepsAndDeleteEffects(t *testing.T) {
	source := domain.Operation{
		PluginID: "test",
		Plan: domain.Plan{PluginID: "test", Steps: []domain.PlanStep{
			{ID: "machines", Name: "Create machines", Input: json.RawMessage(`{"one":1}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "vm/1", Kind: "machine", Name: "one"}}},
			{ID: "cluster", Name: "Install cluster", Input: json.RawMessage(`{"two":2}`), Mutating: true, ArtifactInputs: []domain.ArtifactInput{{Name: "machines", Type: "MachineSet", Version: "v1", ArtifactID: "art_one"}}},
			{ID: "read", Name: "Read only", Input: json.RawMessage(`{}`)},
		}},
		Steps: []domain.OperationStep{{Status: domain.StepSucceeded}, {Status: domain.StepSucceeded}, {Status: domain.StepSucceeded}},
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "cleanup-cluster" || plan.Steps[1].ID != "cleanup-machines" {
		t.Fatalf("unexpected cleanup order %#v", plan.Steps)
	}
	if !plan.Steps[0].Cleanup || !plan.Steps[1].Cleanup || plan.Steps[1].Effects[0].Action != "delete" {
		t.Fatalf("unexpected cleanup plan %#v", plan)
	}
	if len(plan.Steps[0].Outputs) != 0 || len(plan.Steps[0].ArtifactInputs) != 1 {
		t.Fatalf("cleanup must retain inputs but not outputs %#v", plan.Steps[0])
	}
}

func TestCleanupPlanRejectsIncompleteHistory(t *testing.T) {
	_, err := cleanupPlan(domain.Operation{Plan: domain.Plan{Steps: []domain.PlanStep{{ID: "one"}}}})
	if err == nil {
		t.Fatal("expected incomplete source history rejection")
	}
}

func TestManagedCleanupAcceptsDeclaredBackwardCompatibility(t *testing.T) {
	source := domain.Operation{PluginVersion: "1.0.0", Status: domain.OperationSucceeded}
	manifest := plugins.Manifest{Version: "2.0.0", Capabilities: []string{"lifecycle.cleanup"}}
	if managedCleanupCompatible(manifest, source) {
		t.Fatal("expected a changed plugin without compatibility declaration to be rejected")
	}
	manifest.Capabilities = append(manifest.Capabilities, "lifecycle.cleanup.compatible")
	if !managedCleanupCompatible(manifest, source) {
		t.Fatal("expected a changed plugin with compatibility declaration to be accepted")
	}
	source.Status = domain.OperationRunning
	if managedCleanupCompatible(manifest, source) {
		t.Fatal("expected an incomplete source lifecycle to be rejected")
	}
}

func TestWithManagedPlatformStageUpgradesLegacyClusterDefinition(t *testing.T) {
	definition := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "kubernetes"}}}
	upgraded := withManagedPlatformStage(definition, "managed-platform")
	if len(upgraded.Stages) != 2 || upgraded.Stages[1].ID != "platform" || upgraded.Stages[1].PluginID != "managed-platform" || upgraded.Stages[1].Bindings[0].FromStage != "kubernetes" {
		t.Fatalf("unexpected managed platform stage %#v", upgraded.Stages)
	}
	unchanged := withManagedPlatformStage(upgraded, "other")
	if len(unchanged.Stages) != 2 || unchanged.Stages[1].PluginID != "managed-platform" {
		t.Fatalf("managed platform stage must not be duplicated %#v", unchanged.Stages)
	}
}

func TestCleanupPlanIncludesFailedMutableStepForRecovery(t *testing.T) {
	source := domain.Operation{
		PluginID: "test",
		Plan:     domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "vm/110", Kind: "machine", Name: "temporary"}}}}},
		Steps:    []domain.OperationStep{{Status: domain.StepFailed}},
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || !plan.Steps[0].Cleanup || plan.Steps[0].Effects[0].Action != "delete" {
		t.Fatalf("unexpected recovery plan %#v", plan)
	}
}
