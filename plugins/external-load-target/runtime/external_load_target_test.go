package externalloadtarget

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/schema"
)

func TestStaticManifestMatchesRuntime(t *testing.T) {
	value, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var static struct {
		Metadata struct {
			ID          string `yaml:"id"`
			Version     string `yaml:"version"`
			Description string `yaml:"description"`
		} `yaml:"metadata"`
		Spec struct {
			ConfigurationSchema any `yaml:"configurationSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(value, &static); err != nil {
		t.Fatal(err)
	}
	runtime := (Plugin{}).Manifest()
	if static.Metadata.ID != runtime.ID || static.Metadata.Version != runtime.Version || static.Metadata.Description != runtime.Description {
		t.Fatalf("static and runtime manifests differ:\nstatic=%#v\nruntime=%#v", static, runtime)
	}
	staticRaw, err := json.Marshal(static.Spec.ConfigurationSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.ValidateDefinition(staticRaw); err != nil {
		t.Fatalf("static configuration schema is not supported: %v", err)
	}
	if err := schema.ValidateDefinition(runtime.Schema); err != nil {
		t.Fatalf("runtime configuration schema is not supported: %v", err)
	}
	var staticSchema, runtimeSchema any
	if err := json.Unmarshal(staticRaw, &staticSchema); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(runtime.Schema, &runtimeSchema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(staticSchema, runtimeSchema) {
		t.Fatalf("static and runtime schemas differ:\nstatic=%s\nruntime=%s", staticRaw, runtime.Schema)
	}
}

func TestExternalLoadTargetLifecycle(t *testing.T) {
	spec := validSpec()
	raw, _ := json.Marshal(spec)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].Outputs) != 2 {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{"cluster-connection": {Value: clusterArtifact(t)}}
	runner := &externalTargetRunner{}
	plugin := Plugin{Runner: runner}
	if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed: %#v %v", health, err)
	}
	resultRaw, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), step, resultRaw, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed: %#v %v", health, err)
	}
	var value result
	if err := json.Unmarshal(resultRaw, &value); err != nil {
		t.Fatal(err)
	}
	if !value.ApplicationDeployment.Spec.External || value.ApplicationDeployment.Spec.Endpoints[0].URL != spec.TargetURL {
		t.Fatalf("unexpected external deployment %#v", value.ApplicationDeployment)
	}
	profile := value.LoadScenarioSet.Spec.Scenarios[0]
	if profile.ID != scenarioID || profile.Parameters["tenant"] != "demo" || profile.ScriptDigest == "" {
		t.Fatalf("unexpected load profile %#v", profile)
	}

	step.Cleanup = true
	if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed: %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, resultRaw, discardLog); err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), step, nil, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed: %#v %v", health, err)
	}
}

func TestExternalLoadTargetValidation(t *testing.T) {
	valid, _ := json.Marshal(validSpec())
	report := (Plugin{}).Validate(context.Background(), Invocation{Input: valid})
	if !report.Valid {
		t.Fatalf("valid destination rejected: %#v", report)
	}

	cases := []Spec{
		{ClusterConnectionRef: "art_cluster", TargetURL: "ftp://example.test", RuntimeImage: "locust:latest", Script: "from locust import HttpUser"},
		{ClusterConnectionRef: "art_cluster", TargetURL: "https://user:secret@example.test", RuntimeImage: "locust:latest", Script: "from locust import HttpUser"},
		{ClusterConnectionRef: "art_cluster", TargetURL: "https://example.test", RuntimeImage: "locust:latest", Script: "class User: pass"},
		{ClusterConnectionRef: "art_cluster", TargetURL: "https://example.test", RuntimeImage: "locust:latest", Script: "import locust", Parameters: map[string]string{"tenant": "a", "TENANT": "b"}},
	}
	for _, spec := range cases {
		raw, _ := json.Marshal(spec)
		if report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw}); report.Valid {
			t.Fatalf("invalid destination accepted: %#v", spec)
		}
	}
}

func validSpec() Spec {
	return Spec{ClusterConnectionRef: "art_cluster", TargetURL: "https://api.example.test/v1", RuntimeImage: "locustio/locust:2.42.6", Script: "from locust import HttpUser\n", Parameters: map[string]string{"tenant": "demo"}}
}

func clusterArtifact(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"apiVersion": artifactAPI,
		"kind":       "ClusterConnection",
		"metadata":   map[string]string{"name": "development", "version": "v1.36.0+k3s1"},
		"spec":       map[string]string{"distribution": "k3s", "server": "https://10.0.0.10:6443", "kubeconfig": "test-config"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func discardLog(string, string) error { return nil }

type externalTargetRunner struct {
	marker string
}

func (runner *externalTargetRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	switch {
	case command == "get --raw=/readyz":
		return "ok", nil
	case strings.HasPrefix(command, "auth can-i"):
		return "yes\n", nil
	case strings.HasPrefix(command, "get namespace "):
		return runner.marker, nil
	case command == "apply -f -":
		var namespace struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(stdin, &namespace); err != nil {
			return "", err
		}
		runner.marker = namespace.Metadata.Annotations[ownershipKey]
		return "created", nil
	case strings.HasPrefix(command, "delete namespace "):
		runner.marker = ""
		return "deleted", nil
	default:
		return "", errors.New("unexpected command: " + command)
	}
}
