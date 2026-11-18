package hpa

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/domain"
)

func TestValidateChecksBoundsAndOverrides(t *testing.T) {
	spec := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_application", TargetBindingRef: "art_binding", Settings: Settings{MinReplicas: 1, MaxReplicas: 10, CPUUtilization: 70}, TargetOverrides: `{"frontend":{"minReplicas":2,"maxReplicas":5,"cpuUtilization":50}}`}
	if report := (Plugin{}).Validate(context.Background(), invocation(t, spec)); !report.Valid {
		t.Fatalf("valid configuration rejected: %#v", report.Issues)
	}
	for name, change := range map[string]func(*Spec){
		"reference":   func(spec *Spec) { spec.TargetBindingRef = "binding" },
		"bounds":      func(spec *Spec) { spec.MaxReplicas = 0 },
		"utilization": func(spec *Spec) { spec.CPUUtilization = 0 },
		"override": func(spec *Spec) {
			spec.TargetOverrides = `{"frontend":{"minReplicas":3,"maxReplicas":2,"cpuUtilization":50}}`
		},
		"overrides": func(spec *Spec) { spec.TargetOverrides = `[]` },
	} {
		invalid := spec
		change(&invalid)
		if report := (Plugin{}).Validate(context.Background(), invocation(t, invalid)); report.Valid {
			t.Fatalf("%s: invalid configuration accepted", name)
		}
	}
}

func TestManifestCreatesOneOwnedAutoscalerPerTarget(t *testing.T) {
	input := stepInput{Spec: Spec{Settings: Settings{MinReplicas: 1, MaxReplicas: 10, CPUUtilization: 70}, TargetOverrides: `{"cart":{"minReplicas":2,"maxReplicas":4,"cpuUtilization":50}}`}, Name: "kubephos-hpa-test", Marker: "owner"}
	manifest, err := autoscalerManifest(input, application(), binding("frontend", "cart"))
	if err != nil {
		t.Fatal(err)
	}
	documents := strings.Split(string(manifest), "\n---\n")
	if len(documents) != 2 {
		t.Fatalf("expected one autoscaler per target: %s", manifest)
	}
	var cart struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			ScaleTargetRef map[string]string `json:"scaleTargetRef"`
			MinReplicas    int               `json:"minReplicas"`
			MaxReplicas    int               `json:"maxReplicas"`
			Metrics        []struct {
				Resource struct {
					Name   string `json:"name"`
					Target struct {
						AverageUtilization int `json:"averageUtilization"`
					} `json:"target"`
				} `json:"resource"`
			} `json:"metrics"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(documents[1]), &cart); err != nil {
		t.Fatal(err)
	}
	if cart.Kind != "HorizontalPodAutoscaler" || cart.Metadata.Name != "kubephos-hpa-test-cart" || cart.Metadata.Namespace != "shop" || cart.Metadata.Annotations[ownershipKey] != "owner" {
		t.Fatalf("unexpected metadata: %+v", cart)
	}
	if cart.Spec.ScaleTargetRef["name"] != "cart" || cart.Spec.MinReplicas != 2 || cart.Spec.MaxReplicas != 4 || cart.Spec.Metrics[0].Resource.Name != "cpu" || cart.Spec.Metrics[0].Resource.Target.AverageUtilization != 50 {
		t.Fatalf("override not applied: %+v", cart.Spec)
	}
	if !strings.Contains(documents[0], `"averageUtilization":70`) {
		t.Fatalf("spec settings not applied to targets without an override: %s", documents[0])
	}
	input.TargetOverrides = `{"redis":{"minReplicas":1,"maxReplicas":2,"cpuUtilization":50}}`
	if _, err := autoscalerManifest(input, application(), binding("frontend")); err == nil {
		t.Fatal("override of an unselected target accepted")
	}
}

func TestPrecheckRequiresTheMetricsAPI(t *testing.T) {
	runner := &fakeRunner{fail: "--raw=/apis/metrics.k8s.io/v1beta1"}
	report, err := (Plugin{Runner: runner}).Precheck(context.Background(), step(t, false), discard)
	if err != nil || report.Status != domain.HealthUnhealthy || report.Checks["metricsAPI"] != "unavailable" {
		t.Fatalf("missing metrics API passed precheck: %+v %v", report, err)
	}
	runner.fail = ""
	report, err = (Plugin{Runner: runner}).Precheck(context.Background(), step(t, false), discard)
	if err != nil || report.Status != domain.HealthHealthy || !runner.ran("apply --dry-run=server -f -") {
		t.Fatalf("precheck failed: %+v %v %v", report, err, runner.calls)
	}
}

func TestCleanupRefusesAutoscalersOwnedByAnotherRun(t *testing.T) {
	runner := &fakeRunner{marker: "someone-else"}
	report, err := (Plugin{Runner: runner}).Precheck(context.Background(), step(t, true), discard)
	if err != nil || report.Status != domain.HealthUnhealthy || report.Checks["ownership"] != "conflict" {
		t.Fatalf("foreign autoscaler accepted for cleanup: %+v %v", report, err)
	}
	runner.marker = ""
	report, err = (Plugin{Runner: runner}).Precheck(context.Background(), step(t, true), discard)
	if err != nil || report.Status != domain.HealthHealthy {
		t.Fatalf("absent autoscalers blocked cleanup: %+v %v", report, err)
	}
}

func TestDescribedManifestMatchesPackage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor struct {
		Metadata struct {
			ID      string `yaml:"id"`
			Version string `yaml:"version"`
		} `yaml:"metadata"`
		Spec struct {
			Schema       map[string]any `yaml:"configurationSchema"`
			Capabilities []string       `yaml:"capabilities"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	manifest := (Plugin{}).Manifest()
	var schema map[string]any
	if err := json.Unmarshal(manifest.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	packaged, _ := json.Marshal(descriptor.Spec.Schema)
	var normalized map[string]any
	_ = json.Unmarshal(packaged, &normalized)
	if descriptor.Metadata.ID != manifest.ID || descriptor.Metadata.Version != manifest.Version || !reflect.DeepEqual(descriptor.Spec.Capabilities, manifest.Capabilities) || !reflect.DeepEqual(normalized, schema) {
		t.Fatal("plugin.yaml and the described manifest differ")
	}
}

type fakeRunner struct {
	fail   string
	marker string
	calls  []string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	call := strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if runner.fail != "" && strings.Contains(call, runner.fail) {
		return "", errors.New("unavailable")
	}
	if strings.HasPrefix(call, "get horizontalpodautoscaler") {
		return runner.marker, nil
	}
	return "", nil
}

func (runner *fakeRunner) ran(call string) bool {
	for _, value := range runner.calls {
		if value == call {
			return true
		}
	}
	return false
}

func discard(string, string) error { return nil }

func invocation(t *testing.T, spec Spec) Invocation {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return Invocation{Input: raw}
}

func step(t *testing.T, cleanup bool) domain.PlanStep {
	t.Helper()
	input, _ := json.Marshal(stepInput{Spec: Spec{Settings: Settings{MinReplicas: 1, MaxReplicas: 10, CPUUtilization: 70}, TargetOverrides: "{}"}, Name: "kubephos-hpa-test", Marker: "owner"})
	cluster := clusterConnection{APIVersion: artifactAPI, Kind: "ClusterConnection"}
	cluster.Spec.Server, cluster.Spec.Kubeconfig = "https://cluster", "kubeconfig"
	resolved := map[string]any{"cluster-connection": cluster, "application-deployment": application(), "target-binding": binding("frontend")}
	inputs := map[string]domain.ResolvedArtifact{}
	for name, value := range resolved {
		raw, _ := json.Marshal(value)
		inputs[name] = domain.ResolvedArtifact{Value: raw}
	}
	return domain.PlanStep{Input: input, Cleanup: cleanup, ResolvedInputs: inputs}
}

func application() applicationDeployment {
	value := applicationDeployment{APIVersion: artifactAPI, Kind: "ApplicationDeployment"}
	value.Metadata.OwnershipMarker = "application"
	value.Spec.ClusterServer, value.Spec.Namespace = "https://cluster", "shop"
	return value
}

func binding(ids ...string) targetBinding {
	value := targetBinding{APIVersion: artifactAPI, Kind: "TargetBinding"}
	value.Spec.RequiredTrait = scalableTrait
	for _, id := range ids {
		value.Spec.Targets = append(value.Spec.Targets, workloadTarget{ID: id, APIVersion: "apps/v1", Kind: "Deployment", Name: id, Traits: []string{scalableTrait}})
	}
	return value
}
