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
		scoped := strings.HasPrefix(definition.ID, "load.") || strings.HasPrefix(definition.ID, "application.") || definition.ID == "cpu.cores" || definition.ID == "cpu.limit_cores" || definition.ID == "cpu.request_cores" || definition.ID == "memory.working_set" || definition.ID == "pod.restarts"
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

func TestReusedNamespaceScopingKeepsLoadGeneratorExclusion(t *testing.T) {
	scope := ` and on (pod) (max by (pod) (kube_pod_created{namespace="benchmark"} >= 1234567890))`
	expected := map[string]string{
		"cpu.cores":                 `sum by (pod) ((rate(container_cpu_usage_seconds_total{namespace="benchmark",container!="",container!="POD",pod!~"kubephos-pod-scheduling-.*|kubephos-load-.*"}[1m])` + scope + `))`,
		"memory.working_set":        `sum by (pod) ((container_memory_working_set_bytes{namespace="benchmark",container!="",container!="POD",pod!~"kubephos-pod-scheduling-.*|kubephos-load-.*"}` + scope + `))`,
		"pod.restarts":              `sum by (pod) ((kube_pod_container_status_restarts_total{namespace="benchmark",pod!~"kubephos-pod-scheduling-.*|kubephos-load-.*"}` + scope + `))`,
		"deployment.ready_replicas": `max by (deployment) (kube_deployment_status_replicas_ready{namespace="benchmark",deployment!~"kubephos-pod-scheduling|kubephos-load-.*"})`,
	}
	created := time.Unix(1234567890, 0)
	for name, networking := range map[string]string{"istio": "", "cilium": "cilium"} {
		var deployment applicationDeployment
		deployment.Spec.Namespace = "benchmark"
		deployment.Spec.Networking = networking
		deployment.Spec.ClusterName = "cluster-a"
		deployment.Spec.NamespaceCreatedAt = &created
		assertApplicationResourceQueries(t, name, scopeReusableNamespaceMetrics(deploymentMetricDefinitions(deployment), deployment), expected)
		deployment.Spec.Endpoints = []struct {
			Protocol string `json:"protocol"`
		}{{Protocol: "tcp"}}
		native := map[string]string{}
		for id, query := range expected {
			native[id] = query
		}
		native["deployment.ready_replicas"] = `max by (statefulset) (kube_statefulset_status_replicas_ready{namespace="benchmark"})`
		assertApplicationResourceQueries(t, name+" native", scopeReusableNamespaceMetrics(deploymentMetricDefinitions(deployment), deployment), native)
	}
}
