package managedplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Mentat measures node-to-node latency and bandwidth; Cluster Lens shows them. Both are
// core components, installed from the managed Harbor on every cluster.
const (
	mentatVersion      = managedcomponents.MentatVersion
	clusterLensVersion = managedcomponents.ClusterLensVersion
	clusterLensPort    = 32088
)

var supportComponent = platformComponent{
	enabled: func(selection) bool { return true },
	resources: []resourceRef{
		{"clusterrole/kubephos-mentat", ""}, {"clusterrolebinding/kubephos-mentat", ""}, {"clusterrole/kubephos-cluster-lens", ""}, {"clusterrolebinding/kubephos-cluster-lens", ""},
		{"serviceaccount/mentat", observabilityNamespace}, {"daemonset/mentat", observabilityNamespace}, {"podmonitor/mentat", observabilityNamespace},
		{"serviceaccount/cluster-lens", observabilityNamespace}, {"deployment/cluster-lens", observabilityNamespace}, {"service/cluster-lens", observabilityNamespace},
	},
	install: func(ctx context.Context, run *componentRun) error {
		support, err := supportManifest(run.marker, run.cluster.Spec.RegistryHost, run.cluster.Spec.RegistryProject)
		if err != nil {
			return err
		}
		if err := run.log("info", "Installing managed Mentat network measurements and Cluster Lens"); err != nil {
			return err
		}
		if err := run.applyManifest(ctx, support); err != nil {
			return fmt.Errorf("install managed support components: %w", err)
		}
		for _, workload := range []string{"daemonset/mentat", "deployment/cluster-lens"} {
			if err := run.rollout(ctx, workload, observabilityNamespace); err != nil {
				return fmt.Errorf("managed support component %s is not ready: %w", workload, err)
			}
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		for _, workload := range []string{"daemonset/mentat", "deployment/cluster-lens"} {
			if err := run.rollout(ctx, workload, observabilityNamespace); err != nil {
				report := unhealthy("Managed support component is not ready: "+err.Error(), workload, "unhealthy")
				return &report, nil
			}
		}
		latencyNodes, bandwidthNodes, err := waitForNetworkAnnotations(ctx, run.runner, run.kubeconfig, 90*time.Second)
		if err != nil {
			report := unhealthy(err.Error(), "clusterLensNetwork", "unhealthy")
			return &report, nil
		}
		run.checks["mentat"], run.checks["clusterLens"] = mentatVersion, clusterLensVersion
		run.checks["clusterLensLatencyNodes"], run.checks["clusterLensBandwidthNodes"] = fmt.Sprint(latencyNodes), fmt.Sprint(bandwidthNodes)
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		_, err := run.runner.Kubectl(ctx, run.kubeconfig, supportCleanupManifest(run.marker), "delete", "-f", "-", "--ignore-not-found", "--wait=true", "--timeout=5m")
		return err
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("mentat", mentatVersion, observabilityNamespace)
		builder.component("cluster-lens", clusterLensVersion, observabilityNamespace)
		builder.endpoint("cluster-lens", observabilityNamespace, "cluster-lens", clusterLensPort)
		builder.version("mentat." + mentatVersion)
		builder.version("lens." + clusterLensVersion)
	},
}

func supportManifest(marker, registryHost string, projects ...string) ([]byte, error) {
	if !validManagedRegistryHost(registryHost) {
		return nil, errors.New("managed Harbor address is missing from the verified cluster connection")
	}
	mentatImage := managedcomponents.Image(registryHost, managedcomponents.MentatName, mentatVersion, projects...)
	clusterLensImage := managedcomponents.Image(registryHost, managedcomponents.ClusterLensName, clusterLensVersion, projects...)
	return renderSupportManifest(marker, mentatImage, clusterLensImage), nil
}

func supportCleanupManifest(marker string) []byte {
	return renderSupportManifest(marker, "mentat", "cluster-lens")
}

func renderSupportManifest(marker, mentatImage, clusterLensImage string) []byte {
	return []byte(fmt.Sprintf(supportManifestTemplate, marker, marker, marker, marker, mentatImage, marker, marker, marker, marker, marker, clusterLensImage, marker, clusterLensPort))
}

const supportManifestTemplate = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: mentat
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubephos-mentat
  annotations: {kubephos.dev/ownership-marker: %q}
rules:
- apiGroups: [""]
  resources: [pods, nodes]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubephos-mentat
  annotations: {kubephos.dev/ownership-marker: %q}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: kubephos-mentat}
subjects: [{kind: ServiceAccount, name: mentat, namespace: kubephos-observability}]
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: mentat
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  selector: {matchLabels: {app.kubernetes.io/name: mentat}}
  template:
    metadata: {labels: {app: mentat, app.kubernetes.io/name: mentat, app.kubernetes.io/managed-by: kubephos}}
    spec:
      serviceAccountName: mentat
      tolerations: [{operator: Exists}]
      containers:
      - name: mentat
        image: %s
        imagePullPolicy: IfNotPresent
        ports: [{name: metrics, containerPort: 2112}, {name: bandwidth, containerPort: 2113, protocol: UDP}]
        env:
        - {name: SLEEP_SECONDS, value: "5"}
        - {name: NODE_NAME, valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
        - {name: POD_NAMESPACE, valueFrom: {fieldRef: {fieldPath: metadata.namespace}}}
        - {name: PING_ATTEMPTS, value: "5"}
        - {name: PING_TIMEOUT_SECONDS, value: "1"}
        - {name: BANDWIDTH_PORT, value: "2113"}
        - {name: BANDWIDTH_BYTES, value: "1048576"}
        - {name: BANDWIDTH_INTERVAL_SECONDS, value: "90"}
        - {name: BANDWIDTH_JITTER_SECONDS, value: "30"}
        - {name: BANDWIDTH_TIMEOUT_SECONDS, value: "30"}
---
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: mentat
  namespace: kubephos-observability
  labels: {release: kubephos-observability}
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  selector: {matchLabels: {app.kubernetes.io/name: mentat}}
  podMetricsEndpoints: [{port: metrics}]
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubephos-cluster-lens
  annotations: {kubephos.dev/ownership-marker: %q}
rules:
- apiGroups: [""]
  resources: [nodes, pods]
  verbs: [get, list, watch]
- apiGroups: [apps]
  resources: [deployments, replicasets, daemonsets, statefulsets]
  verbs: [get, list, watch]
- apiGroups: [metrics.k8s.io]
  resources: [nodes]
  verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubephos-cluster-lens
  annotations: {kubephos.dev/ownership-marker: %q}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: kubephos-cluster-lens}
subjects: [{kind: ServiceAccount, name: cluster-lens, namespace: kubephos-observability}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  replicas: 1
  selector: {matchLabels: {app.kubernetes.io/name: cluster-lens}}
  template:
    metadata: {labels: {app.kubernetes.io/name: cluster-lens, app.kubernetes.io/managed-by: kubephos}}
    spec:
      serviceAccountName: cluster-lens
      nodeSelector: {kubephos.dev/role: management}
      containers:
      - name: cluster-lens
        image: %s
        imagePullPolicy: IfNotPresent
        ports: [{name: http, containerPort: 8088}]
        env: [{name: CLUSTER_LENS_ADDR, value: ":8088"}, {name: CLUSTER_LENS_REFRESH, value: "2s"}, {name: CLUSTER_LENS_CONTEXT, value: in-cluster}, {name: CLUSTER_LENS_PROMETHEUS_URL, value: "http://kubephos-observability-kub-prometheus.kubephos-observability.svc:9090"}]
---
apiVersion: v1
kind: Service
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: cluster-lens}
  ports: [{name: http, port: 8088, targetPort: http, nodePort: %d}]
`

func networkAnnotationCoverage(raw string) (int, int, error) {
	var nodes nodeList
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return 0, 0, err
	}
	latency := 0
	bandwidth := 0
	for _, node := range nodes.Items {
		hasLatency := false
		hasBandwidth := false
		for name := range node.Metadata.Annotations {
			hasLatency = hasLatency || strings.HasPrefix(name, "network-latency.")
			hasBandwidth = hasBandwidth || strings.HasPrefix(name, "network-bandwidth.")
		}
		if hasLatency {
			latency++
		}
		if hasBandwidth {
			bandwidth++
		}
	}
	return latency, bandwidth, nil
}

func waitForNetworkAnnotations(ctx context.Context, runner clusterRunner, kubeconfig string, timeout time.Duration) (int, int, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "nodes", "-o", "json")
		if err == nil {
			latency, bandwidth, parseErr := networkAnnotationCoverage(raw)
			if parseErr == nil && latency > 0 && bandwidth > 0 {
				return latency, bandwidth, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		case <-deadline.C:
			return 0, 0, errors.New("Mentat metrics did not reach the node annotations consumed by Cluster Lens")
		case <-ticker.C:
		}
	}
}
