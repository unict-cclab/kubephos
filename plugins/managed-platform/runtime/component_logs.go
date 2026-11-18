package managedplatform

import (
	"context"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Loki stores the cluster and pipeline logs that Alloy collects, with Kubernetes events.
const (
	lokiVersion  = "6.41.1"
	alloyVersion = "1.4.0"
	lokiRelease  = "kubephos-loki"
	alloyRelease = "kubephos-alloy"
	lokiPort     = 32099
)

var logsComponent = platformComponent{
	enabled: func(s selection) bool { return s.has(managedcomponents.Logs) },
	charts: []chartSpec{
		{ID: "loki", URL: "https://github.com/grafana/helm-charts/releases/download/helm-loki-6.41.1/loki-6.41.1.tgz", Digest: "27aa43d3a2792b79d94920feedf6a661ec7a17dedfb3bbd23d6e8e54b4c2d187"},
		{ID: "alloy", URL: "https://github.com/grafana/helm-charts/releases/download/alloy-1.4.0/alloy-1.4.0.tgz", Digest: "b1adbb6344301b8353c9d5d39bb5de196e3735441ca1742913d60922a96bffcb"},
	},
	releases: []releaseRef{{lokiRelease, observabilityNamespace}, {alloyRelease, observabilityNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		if err := run.log("info", "Installing managed logs and Kubernetes event collection"); err != nil {
			return err
		}
		if err := run.helm(ctx, "loki", lokiValues(), lokiRelease, observabilityNamespace, "20m"); err != nil {
			return fmt.Errorf("install Loki: %w", err)
		}
		if err := run.helm(ctx, "alloy", alloyValues(), alloyRelease, observabilityNamespace, "10m"); err != nil {
			return fmt.Errorf("install Alloy: %w", err)
		}
		return nil
	},
	verify: func(_ context.Context, run *componentRun) (*domain.HealthReport, error) {
		run.checks["loki"], run.checks["alloy"] = lokiVersion, alloyVersion
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		for _, release := range []string{alloyRelease, lokiRelease} {
			if err := run.uninstall(ctx, release, observabilityNamespace); err != nil {
				return err
			}
		}
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "delete", "persistentvolumeclaim", "-n", observabilityNamespace, "-l", "app.kubernetes.io/instance="+lokiRelease, "--wait=true", "--timeout=5m"); err != nil {
			return fmt.Errorf("remove Loki storage: %w", err)
		}
		return nil
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("loki", lokiVersion, observabilityNamespace)
		builder.component("alloy", alloyVersion, observabilityNamespace)
		builder.endpoint("loki", observabilityNamespace, "kubephos-loki-gateway", lokiPort)
	},
}

func lokiValues() []byte {
	return []byte(fmt.Sprintf(`deploymentMode: SingleBinary
loki:
  auth_enabled: false
  commonConfig:
    replication_factor: 1
  storage:
    type: filesystem
  schemaConfig:
    configs:
    - from: "2024-04-01"
      store: tsdb
      object_store: filesystem
      schema: v13
      index: {prefix: loki_index_, period: 24h}
  limits_config:
    retention_period: 168h
  compactor:
    retention_enabled: true
    delete_request_store: filesystem
singleBinary:
  replicas: 1
  nodeSelector: {kubephos.dev/role: management}
  persistence: {enabled: true, storageClass: shared-storage, size: 10Gi}
read: {replicas: 0}
write: {replicas: 0}
backend: {replicas: 0}
gateway:
  enabled: true
  nodeSelector: {kubephos.dev/role: management}
  service: {type: NodePort, nodePort: %d}
chunksCache: {enabled: false}
resultsCache: {enabled: false}
lokiCanary: {enabled: false}
test: {enabled: false}
minio: {enabled: false}
`, lokiPort))
}

func alloyValues() []byte {
	return []byte(`controller:
  type: deployment
  replicas: 1
  nodeSelector: {kubephos.dev/role: management}
alloy:
  configMap:
    create: true
    content: |-
      discovery.kubernetes "pods" {
        role = "pod"
      }
      discovery.relabel "pod_logs" {
        targets = discovery.kubernetes.pods.targets
        rule {
          source_labels = ["__meta_kubernetes_namespace"]
          target_label = "namespace"
        }
        rule {
          source_labels = ["__meta_kubernetes_pod_name"]
          target_label = "pod"
        }
        rule {
          source_labels = ["__meta_kubernetes_pod_container_name"]
          target_label = "container"
        }
      }
      loki.source.kubernetes "pod_logs" {
        targets = discovery.relabel.pod_logs.output
        forward_to = [loki.write.local.receiver]
      }
      loki.source.kubernetes_events "events" {
        job_name = "kubernetes-events"
        log_format = "json"
        forward_to = [loki.write.local.receiver]
      }
      loki.write "local" {
        endpoint {
          url = "http://kubephos-loki-gateway.kubephos-observability.svc.cluster.local/loki/api/v1/push"
        }
      }
`)
}
