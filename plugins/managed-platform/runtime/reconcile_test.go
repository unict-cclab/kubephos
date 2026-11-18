package managedplatform

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/managedcomponents"
)

type recordingRunner struct{ commands *[]string }

func (runner recordingRunner) Kubectl(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	*runner.commands = append(*runner.commands, "kubectl "+strings.Join(args, " "))
	return "", nil
}

func (runner recordingRunner) Helm(_ context.Context, _ string, _ []byte, _ []byte, args ...string) (string, error) {
	*runner.commands = append(*runner.commands, "helm "+strings.Join(args, " "))
	return "", nil
}

// removeUnselected runs the reconcile removal for a selection and returns the commands it issued.
func removeUnselected(t *testing.T, selected selection) string {
	t.Helper()
	commands := []string{}
	run := &componentRun{runner: recordingRunner{&commands}, kubeconfig: "config", marker: "marker", selected: selected, log: func(string, string) error { return nil }}
	if err := removeComponents(context.Background(), run, func(component platformComponent) bool { return !component.enabled(selected) }); err != nil {
		t.Fatal(err)
	}
	return strings.Join(commands, "\n")
}

func TestReconcileRemovesOnlyDeselectedComponents(t *testing.T) {
	joined := removeUnselected(t, newSelection(Spec{Components: []string{managedcomponents.IstioSidecar, managedcomponents.Logs}}, ""))
	for _, removed := range []string{"uninstall " + headlampRelease, "uninstall " + cpaRelease, "uninstall " + chaosRelease, "delete namespace " + chaosNamespace, "uninstall " + kialiRelease} {
		if !strings.Contains(joined, removed) {
			t.Fatalf("%q not removed:\n%s", removed, joined)
		}
	}
	for _, kept := range []string{"uninstall " + lokiRelease, "uninstall " + alloyRelease, "uninstall " + istiodRelease, "delete namespace " + istioNamespace, "kubephos-cluster-lens"} {
		if strings.Contains(joined, kept) {
			t.Fatalf("%q removed although selected:\n%s", kept, joined)
		}
	}
}

func TestReconcileRemovesIstioWhenNoDataPlaneIsSelected(t *testing.T) {
	joined := removeUnselected(t, newSelection(Spec{Components: []string{}}, ""))
	for _, removed := range []string{"uninstall " + ztunnelRelease, "uninstall " + istiodRelease, "delete namespace " + istioNamespace} {
		if !strings.Contains(joined, removed) {
			t.Fatalf("%q not removed:\n%s", removed, joined)
		}
	}
	if strings.Contains(removeUnselected(t, newSelection(Spec{Components: []string{}}, "cilium")), istioNamespace) {
		t.Fatal("Cilium reconcile touched the Istio namespace")
	}
}

func TestReconcilePlanKeepsTheOwnershipMarker(t *testing.T) {
	marker := strings.Repeat("a", 32)
	raw, _ := json.Marshal(Spec{ClusterConnectionRef: "art_cluster", Components: []string{managedcomponents.ChaosMesh}, Reconcile: true, OwnershipMarker: marker})
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var input stepInput
	if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil || input.Marker != marker {
		t.Fatalf("marker = %q, %v", input.Marker, err)
	}
	if len(plan.Steps[0].Outputs) != 1 {
		t.Fatalf("Headlamp credential output without Headlamp: %+v", plan.Steps[0].Outputs)
	}
	if report := (Plugin{}).Validate(context.Background(), Invocation{Input: json.RawMessage(`{"clusterConnectionRef":"art_cluster","reconcile":true}`)}); report.Valid {
		t.Fatal("reconcile without marker accepted")
	}
	if !slices.Contains(newSelection(Spec{}, "").namespaces(), istioNamespace) {
		t.Fatal("legacy selection lost the Istio namespace")
	}
}
