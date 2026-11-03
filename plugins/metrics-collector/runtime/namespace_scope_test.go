package metricscollector

import (
	"strings"
	"testing"
	"time"
)

func TestReusedNamespaceQueriesExcludePreviousRunPods(t *testing.T) {
	var deployment applicationDeployment
	deployment.Spec.Namespace = "benchmark"
	created := time.Unix(1234567890, 0)
	deployment.Spec.NamespaceCreatedAt = &created
	for _, definition := range scopeReusableNamespaceMetrics(deploymentMetricDefinitions(deployment), deployment) {
		scoped := strings.HasPrefix(definition.ID, "load.") || strings.HasPrefix(definition.ID, "application.") || definition.ID == "cpu.cores" || definition.ID == "memory.working_set" || definition.ID == "pod.restarts"
		if scoped && (!strings.Contains(definition.Query, `kube_pod_created{namespace="benchmark"} >= 1234567890`) || !strings.Contains(definition.Query, "and on (pod)")) {
			t.Fatalf("unscoped metric %s: %s", definition.ID, definition.Query)
		}
		if definition.ID == "network.rtt" && strings.Contains(definition.Query, "kube_pod_created") {
			t.Fatal("cluster network metric incorrectly filtered by application pods")
		}
	}
	deployment.Spec.NamespaceCreatedAt = nil
	for _, definition := range scopeReusableNamespaceMetrics(deploymentMetricDefinitions(deployment), deployment) {
		if strings.Contains(definition.Query, "kube_pod_created") {
			t.Fatal("default query behavior changed")
		}
	}
}
