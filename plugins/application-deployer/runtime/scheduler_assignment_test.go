package applicationdeployer

import (
	"strings"
	"testing"
)

func TestAssignSchedulerBeforeWorkloadCreation(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
spec:
  template:
    spec:
      containers:
        - name: frontend
          image: example/frontend
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: database
spec:
  template:
    spec:
      containers:
        - name: database
          image: example/database
`
	workloads := []workloadTarget{
		{ID: "frontend", Kind: "Deployment", Name: "frontend", Traits: []string{"schedulable"}},
		{ID: "database", Kind: "StatefulSet", Name: "database", Traits: []string{"schedulable"}},
	}
	value, err := assignScheduler(manifest, workloads, "kubephos-run-test", []string{"frontend"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(value, "schedulerName: kubephos-run-test") != 1 {
		t.Fatalf("unexpected scheduler assignments: %s", value)
	}
	if !strings.Contains(value, "name: database") {
		t.Fatalf("unselected workload was removed: %s", value)
	}
}

func TestAssignApplicationPlacementPreservesSelectors(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
spec:
  template:
    spec:
      nodeSelector:
        disk: fast
      containers:
        - name: frontend
          image: example/frontend
`
	workloads := []workloadTarget{{ID: "frontend", Kind: "Deployment", Name: "frontend", Traits: []string{"schedulable"}}}
	value, err := assignApplicationPlacement(manifest, workloads)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, "kubephos.dev/role: application") || !strings.Contains(value, "disk: fast") {
		t.Fatalf("application placement is incomplete: %s", value)
	}
}

func TestAssignSchedulerRejectsUnknownSelection(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
spec:
  template:
    spec:
      containers:
        - name: frontend
          image: example/frontend
`
	workloads := []workloadTarget{{ID: "frontend", Kind: "Deployment", Name: "frontend", Traits: []string{"schedulable"}}}
	if _, err := assignScheduler(manifest, workloads, "kubephos-run-test", []string{"missing"}, nil); err == nil {
		t.Fatal("unknown scheduler target was accepted")
	}
}
