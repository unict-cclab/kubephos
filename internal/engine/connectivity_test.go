package engine

import (
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestMultiClusterConnectivityDoesNotHideAFailedMember(t *testing.T) {
	report := connectivityReport(domain.ConnectivityTarget{Name: "east", Status: "unknown"}, domain.ConnectivityTarget{Name: "west", Status: "reachable"}, domain.ConnectivityTarget{Name: "cloud", Status: "unreachable", Message: "Could not connect to Kubernetes."})
	if report.Status != "unreachable" || len(report.Targets) != 3 || report.Targets[1].Status != "reachable" || report.CheckedAt.IsZero() {
		t.Fatalf("unexpected report: %+v", report)
	}
	if connectivityReport().Status != "unknown" {
		t.Fatal("missing endpoints must not look reachable")
	}
	if connectivityReport(domain.ConnectivityTarget{Name: "east", Status: "reachable"}).Status != "reachable" {
		t.Fatal("recovered cluster is still unavailable")
	}
}
