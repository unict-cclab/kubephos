package applicationinspector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestInspectManifestMatchesDeclaredInterface(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    metadata:
      labels:
        app: api
---
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  selector:
    app: api
  ports:
    - port: 8080
`)
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.Components = []catalog.Component{{ID: "api", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, Selector: map[string]string{"app": "api"}, Traits: []string{"scalable"}}}
	descriptor.Spec.Interface.Endpoints = []catalog.Endpoint{{ID: "http", Component: "api", Service: "api", Port: 8080, Protocol: "http"}}
	workloads, endpoints, err := inspectManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 1 || len(endpoints) != 1 {
		t.Fatalf("unexpected interface: %#v %#v", workloads, endpoints)
	}
}

func TestValidateRequiresResolvedCatalogEntry(t *testing.T) {
	input, _ := json.Marshal(specification{ApplicationRef: "app:dev.example.app@1.0.0"})
	report := (Plugin{}).Validate(t.Context(), Invocation{Input: input, Catalog: map[string]json.RawMessage{}})
	if report.Valid {
		t.Fatal("expected unresolved application to fail")
	}
}

func TestPartitionManifestSeparatesDeclaredLoadDriver(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    metadata:
      labels:
        app: api
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loadgenerator
spec:
  template:
    metadata:
      labels:
        app: loadgenerator
# package footer
`)
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.LoadDrivers = []catalog.LoadDriver{{ID: "default-load", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "loadgenerator"}, Selector: map[string]string{"app": "loadgenerator"}, TargetEndpoint: "http", Replicas: 2}}
	application, profiles, err := partitionManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(application), "loadgenerator") || !strings.Contains(string(application), "name: api") {
		t.Fatalf("unexpected application manifest: %s", application)
	}
	if len(profiles) != 1 || profiles[0].ID != "default-load" || profiles[0].Replicas != 2 || !strings.Contains(profiles[0].Manifest, "name: loadgenerator") {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}
	combined := appendManifestDocument(application, []byte(profiles[0].Manifest))
	verified, verifiedProfiles, err := partitionManifest(combined, descriptor)
	applicationIdentity, applicationErr := canonicalManifestDigest(application)
	verifiedIdentity, verifiedErr := canonicalManifestDigest(verified)
	if err != nil || applicationErr != nil || verifiedErr != nil || applicationIdentity != verifiedIdentity || len(verifiedProfiles) != 1 {
		t.Fatalf("partition is not stable: %v\napplication=%q\nverified=%q", err, application, verified)
	}
}

func TestPlanDeclaresTypedInterfaceArtifacts(t *testing.T) {
	descriptor := catalog.Descriptor{}
	descriptor.Metadata.ID = "dev.example.app"
	descriptor.Metadata.Version = "1.0.0"
	raw, _ := json.Marshal(descriptor)
	input, _ := json.Marshal(specification{ApplicationRef: "app:dev.example.app@1.0.0"})
	plan, err := (Plugin{}).Plan(context.Background(), Invocation{Input: input, Catalog: map[string]json.RawMessage{"app:dev.example.app@1.0.0": raw}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].Outputs) != 4 {
		t.Fatalf("unexpected typed outputs: %#v", plan.Steps)
	}
	if plan.Steps[0].Outputs[0].Type != "ManifestSet" || plan.Steps[0].Outputs[1].Type != "WorkloadTargets" || plan.Steps[0].Outputs[2].Type != "ServiceEndpoints" || plan.Steps[0].Outputs[3].Type != "LoadProfileSet" {
		t.Fatalf("unexpected artifact types: %#v", plan.Steps[0].Outputs)
	}
}
