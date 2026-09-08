package applicationinspector

import (
	"context"
	"encoding/json"
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
	if len(plan.Steps) != 1 || len(plan.Steps[0].Outputs) != 3 {
		t.Fatalf("unexpected typed outputs: %#v", plan.Steps)
	}
	if plan.Steps[0].Outputs[0].Type != "ManifestSet" || plan.Steps[0].Outputs[1].Type != "WorkloadTargets" || plan.Steps[0].Outputs[2].Type != "ServiceEndpoints" {
		t.Fatalf("unexpected artifact types: %#v", plan.Steps[0].Outputs)
	}
}
