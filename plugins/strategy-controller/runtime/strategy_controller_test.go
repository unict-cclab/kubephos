package strategycontroller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const testImage = "harbor.example.test/kubephos/strategy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestStrategyValidation(t *testing.T) {
	base := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_application", TargetBindingRef: "art_binding", Image: testImage}
	descheduler := base
	descheduler.ConfigFile = "apiVersion: descheduler/v1alpha2\nkind: DeschedulerPolicy\nprofiles: []\n"
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

func TestGeneratedResourcesUseImmutableImageAndTargets(t *testing.T) {
	input := stepInput{Spec: Spec{Image: testImage, ConfigFile: "kind: DeschedulerPolicy", IntervalSeconds: 60, MinReplicas: 2, MaxReplicas: 8, Parameters: `{"plugin":"polaris"}`, TargetOverrides: `{"frontend":{"intervalSeconds":9,"minReplicas":3,"maxReplicas":12,"parameters":"{\"plugin\":\"sophos\"}"}}`}, Name: "kubephos-test", Marker: "owner"}
	application := applicationDeployment{}
	application.Spec.Namespace = "kubephos-application"
	binding := targetBinding{}
	binding.Spec.Targets = []workloadTarget{{ID: "frontend", APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend"}}
	descheduler := string(deschedulerManifest(input))
	if !strings.Contains(descheduler, testImage) || !strings.Contains(descheduler, "--descheduling-interval=60s") || !strings.Contains(descheduler, "owner") {
		t.Fatalf("unexpected descheduler manifest %s", descheduler)
	}
	autoscaler, err := autoscalerManifest(input, application, binding)
	if err != nil {
		t.Fatal(err)
	}
	text := string(autoscaler)
	for _, expected := range []string{testImage, "CustomPodAutoscaler", "frontend", "config.json", `\"minReplicas\":3`, `"value":"9000"`, "owner"} {
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
