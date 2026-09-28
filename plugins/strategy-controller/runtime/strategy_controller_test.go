package strategycontroller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

const testImage = "ghcr.io/example/strategy:v1.2.0"
const testDeschedulerPolicy = `apiVersion: descheduler/v1alpha2
kind: DeschedulerPolicy
profiles:
  - name: network-aware
    pluginConfig:
      - name: NetworkAware
        args:
          minPodAgeSeconds: 10
    plugins:
      deschedule:
        enabled:
          - NetworkAware
`

func TestStrategyValidation(t *testing.T) {
	base := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_application", TargetBindingRef: "art_binding", Image: testImage}
	descheduler := base
	descheduler.ConfigFile = testDeschedulerPolicy
	descheduler.IntervalSeconds = 60
	if report := (Plugin{Kind: "descheduler"}).Validate(context.Background(), Invocation{Input: marshal(t, descheduler)}); !report.Valid {
		t.Fatalf("descheduler validation failed %#v", report.Issues)
	}
	autoscaler := base
	autoscaler.IntervalSeconds = 15
	autoscaler.MinReplicas = 1
	autoscaler.MaxReplicas = 10
	autoscaler.Parameters = `{"plugin":"sophos","targetResponseTimeMillis":250}`
	autoscaler.TargetOverrides = `{}`
	if report := (Plugin{Kind: "autoscaler"}).Validate(context.Background(), Invocation{Input: marshal(t, autoscaler)}); !report.Valid {
		t.Fatalf("autoscaler validation failed %#v", report.Issues)
	}
	autoscaler.MaxReplicas = 0
	if report := (Plugin{Kind: "autoscaler"}).Validate(context.Background(), Invocation{Input: marshal(t, autoscaler)}); report.Valid {
		t.Fatal("invalid replica bounds were accepted")
	}
}

func TestGeneratedResourcesUseProxyImageAndTargets(t *testing.T) {
	input := stepInput{Spec: Spec{Image: testImage, ConfigFile: testDeschedulerPolicy, IntervalSeconds: 60, MinReplicas: 2, MaxReplicas: 8, Parameters: `{"plugin":"polaris"}`, TargetOverrides: `{"frontend":{"intervalSeconds":9,"minReplicas":3,"maxReplicas":12,"parameters":"{\"plugin\":\"sophos\"}"}}`}, Name: "kubephos-test", Namespace: "kubephos-control-test", Marker: "owner"}
	application := applicationDeployment{}
	application.Spec.Namespace = "kubephos-application"
	binding := targetBinding{}
	binding.Spec.Targets = []workloadTarget{{ID: "frontend", APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend"}}
	deschedulerManifest, err := deschedulerManifest(input, application, binding)
	if err != nil {
		t.Fatal(err)
	}
	descheduler := string(deschedulerManifest)
	if !strings.Contains(descheduler, testImage) || !strings.Contains(descheduler, `"command":["/bin/descheduler"]`) || !strings.Contains(descheduler, "--descheduling-interval=60s") || !strings.Contains(descheduler, "owner") || !strings.Contains(descheduler, applicationComponentLabel) || !strings.Contains(descheduler, applicationNamespaceLabel) || !strings.Contains(descheduler, "kubephos-application") {
		t.Fatalf("unexpected descheduler manifest %s", descheduler)
	}
	if strings.Contains(descheduler, `"kind":"Namespace"`) || !strings.Contains(descheduler, `"namespace":"kubephos-control-test"`) {
		t.Fatalf("descheduler must use, but not own, the shared control namespace: %s", descheduler)
	}
	autoscaler, err := autoscalerManifest(input, application, binding)
	if err != nil {
		t.Fatal(err)
	}
	text := string(autoscaler)
	for _, expected := range []string{testImage, "CustomPodAutoscaler", "ServiceAccount", "ClusterRoleBinding", "cluster-admin", `"serviceAccountName":"kubephos-test"`, "frontend", "config.json", `\"minReplicas\":3`, `"value":"9000"`, "owner"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("autoscaler manifest is missing %s: %s", expected, text)
		}
	}
}

func TestAutoscalerRejectsOverrideForUnselectedTarget(t *testing.T) {
	input := stepInput{Spec: Spec{Image: testImage, IntervalSeconds: 15, MinReplicas: 1, MaxReplicas: 8, Parameters: `{}`, TargetOverrides: `{"checkout":{"intervalSeconds":10,"minReplicas":2,"maxReplicas":5,"parameters":"{}"}}`}}
	binding := targetBinding{}
	binding.Spec.Targets = []workloadTarget{{ID: "frontend"}}
	if _, err := autoscalerManifest(input, applicationDeployment{}, binding); err == nil || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("expected unselected override error, got %v", err)
	}
}

func marshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type precheckRunner struct {
	commands  []string
	manifests [][]byte
}

func (runner *precheckRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	runner.commands = append(runner.commands, strings.Join(args, " "))
	runner.manifests = append(runner.manifests, append([]byte(nil), stdin...))
	if len(args) > 1 && args[0] == "get" && args[1] == "namespace" && strings.Contains(strings.Join(args, " "), "ownership-marker") {
		return "application-owner", nil
	}
	return "accepted", nil
}

func TestDeschedulerPrecheckValidatesSharedControlNamespace(t *testing.T) {
	input := stepInput{Spec: Spec{Image: testImage, ConfigFile: testDeschedulerPolicy, IntervalSeconds: 60}, Name: "kubephos-descheduler-test", Namespace: "kubephos-control-test", Marker: "owner"}
	cluster := clusterConnection{APIVersion: artifactAPI, Kind: "ClusterConnection"}
	cluster.Spec.Server = "https://cluster.example"
	cluster.Spec.Kubeconfig = "kubeconfig"
	application := applicationDeployment{APIVersion: artifactAPI, Kind: "ApplicationDeployment"}
	application.Metadata.OwnershipMarker = "application-owner"
	application.Spec.ClusterServer = cluster.Spec.Server
	application.Spec.Namespace = "application-test"
	application.Spec.ControlNamespace = input.Namespace
	binding := targetBinding{APIVersion: artifactAPI, Kind: "TargetBinding"}
	binding.Spec.RequiredTrait = "deschedulable"
	binding.Spec.Targets = []workloadTarget{{ID: "frontend", APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend", Traits: []string{"deschedulable"}}}
	step := domain.PlanStep{Input: marshal(t, input), ResolvedInputs: map[string]domain.ResolvedArtifact{
		"cluster-connection":     {Value: marshal(t, cluster)},
		"application-deployment": {Value: marshal(t, application)},
		"target-binding":         {Value: marshal(t, binding)},
	}}
	runner := &precheckRunner{}
	report, err := (Plugin{Kind: "descheduler", Runner: runner}).Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || report.Status != domain.HealthHealthy {
		t.Fatalf("unexpected precheck result %#v: %v", report, err)
	}
	if len(runner.commands) != 4 || runner.commands[1] != "get namespace kubephos-control-test --ignore-not-found -o jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}" || runner.commands[2] != "apply --dry-run=server -f -" || runner.commands[3] != "apply --dry-run=client --validate=true -f -" {
		t.Fatalf("unexpected commands %#v", runner.commands)
	}
	foundation := string(runner.manifests[2])
	if strings.Contains(foundation, `"kind":"Namespace"`) || !strings.Contains(foundation, `"kind":"ClusterRoleBinding"`) || strings.Contains(foundation, `{"apiVersion":"v1","kind":"ServiceAccount"`) || strings.Contains(foundation, `"kind":"Deployment"`) {
		t.Fatalf("unexpected server-side admission manifest %s", foundation)
	}
	if !strings.Contains(string(runner.manifests[3]), `"kind":"Deployment"`) {
		t.Fatal("client-side validation did not receive the complete manifest")
	}
}

func TestDeschedulerPlanUsesExecutionControlNamespace(t *testing.T) {
	plan, err := (Plugin{Kind: "descheduler"}).Plan(context.Background(), marshal(t, Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_application", TargetBindingRef: "art_binding", Image: testImage, ConfigFile: testDeschedulerPolicy, IntervalSeconds: 60}))
	if err != nil {
		t.Fatal(err)
	}
	var input stepInput
	if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input.Namespace != "kubephos-control-"+domain.RuntimeExecutionIDToken || input.Name != "kubephos-descheduler-"+domain.RuntimeExecutionIDToken || input.Marker != domain.RuntimeExecutionIDToken {
		t.Fatalf("unexpected descheduler identity %#v", input)
	}
}

func TestKubectlRunnerUsesTemporaryKubeconfigFile(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "kubectl")
	content := "#!/bin/sh\n[ \"$1\" = \"--kubeconfig\" ] || exit 10\n[ -f \"$2\" ] || exit 11\n[ \"$(cat \"$2\")\" = \"verified-kubeconfig\" ] || exit 12\n[ \"$3\" = \"get\" ] || exit 13\n[ \"$4\" = \"--raw=/readyz\" ] || exit 14\n[ -z \"$KUBECONFIG\" ] || exit 15\nprintf ready\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("KUBECONFIG", "")
	output, err := (kubectlRunner{}).Run(context.Background(), "verified-kubeconfig", nil, "get", "--raw=/readyz")
	if err != nil || output != "ready" {
		t.Fatalf("unexpected kubectl result %q: %v", output, err)
	}
}
