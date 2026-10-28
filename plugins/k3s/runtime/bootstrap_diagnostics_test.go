package k3s

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failedBootstrapRunner struct {
	diagnostics int
	base        *fakeRunner
}

func (r *failedBootstrapRunner) Run(ctx context.Context, m machine, a machineAccess, command string) (string, error) {
	if command == bootstrapDiagnosticsCommand {
		r.diagnostics++
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "NRestarts=12\nActiveState=activating\nrootDiskUsage=8%\ncloudControllerRBAC=denied\nTOKEN=secret-value\nResult=secret-value?", nil
	}
	if strings.Contains(command, "sh -s - server") {
		return "", context.DeadlineExceeded
	}
	return r.base.Run(ctx, m, a, command)
}
func TestBootstrapFailureRetainsSafeDiagnosticsBeforeCleanup(t *testing.T) {
	runner := &failedBootstrapRunner{base: &fakeRunner{installed: map[string]string{}}}
	logs := []string{}
	_, err := (Plugin{Runner: runner}).Execute(context.Background(), bootstrapStep(t, 1), func(_ string, message string) error { logs = append(logs, message); return nil })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "RBAC denied") {
		t.Fatalf("missing failure cause: %v", err)
	}
	all := strings.Join(logs, "\n")
	if runner.diagnostics != 3 || !strings.Contains(all, "NRestarts=12") || strings.Contains(all, "secret-value") {
		t.Fatalf("incorrect safe diagnostics: %s", all)
	}
}
func TestVersionValidationRejectsMixedNodesAndUnplannedDowngrade(t *testing.T) {
	inv := clusterInventory{APIVersion: clusterAPIVersion, Kind: "ClusterInventory"}
	inv.Spec.Distribution = "k3s"
	inv.Spec.Version = "v1.36.0+k3s1"
	inv.Spec.Nodes = []clusterNode{{Name: "node", Ready: true, Version: inv.Spec.Version}}
	if validateInventory(inv, 1) == nil {
		t.Fatal("fresh bootstrap accepted old version")
	}
	if err := validateInventory(inv, 1, inv.Spec.Version); err != nil {
		t.Fatal(err)
	}
	inv.Spec.Nodes = append(inv.Spec.Nodes, clusterNode{Name: "other", Ready: true, Version: k3sVersion})
	if validateInventory(inv, 2, inv.Spec.Version) == nil {
		t.Fatal("mixed releases accepted")
	}
	if validResultVersion(Spec{}, "v1.36.0+k3s1") || validResultVersion(Spec{Reconcile: true}, "v1.37.0+k3s1") {
		t.Fatal("unexpected release accepted")
	}
}

func TestDiagnosticsSurviveExpiredBootstrapContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &failedBootstrapRunner{base: &fakeRunner{installed: map[string]string{}}}
	_, machines, access, err := resolve(bootstrapStep(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	detail := collectBootstrapDiagnostics(ctx, runner, machines, access, func(string, string) error { return nil })
	if runner.diagnostics != 3 || !strings.Contains(detail, "RBAC denied") {
		t.Fatal("expired bootstrap context prevented diagnostics")
	}
}
