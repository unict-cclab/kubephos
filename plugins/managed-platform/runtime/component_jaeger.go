package managedplatform

import (
	"bytes"
	"context"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Jaeger stores distributed traces; with Istio, the mesh telemetry sends spans to it.
const (
	jaegerVersion = "1.53"
	jaegerPort    = 30002
)

var jaegerComponent = platformComponent{
	enabled: func(s selection) bool { return s.has(managedcomponents.Jaeger) },
	resources: []resourceRef{
		{"deployment/jaeger", observabilityNamespace}, {"service/jaeger", observabilityNamespace}, {"persistentvolumeclaim/jaeger-badger", observabilityNamespace},
	},
	install: func(ctx context.Context, run *componentRun) error {
		if err := run.log("info", "Installing managed distributed tracing"); err != nil {
			return err
		}
		if err := run.applyManifest(ctx, tracingManifest(run.marker, run.selected.istio())); err != nil {
			return fmt.Errorf("install Jaeger tracing: %w", err)
		}
		if err := run.rollout(ctx, "deployment/jaeger", observabilityNamespace); err != nil {
			return fmt.Errorf("Jaeger is not ready: %w", err)
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		if err := run.rollout(ctx, "deployment/jaeger", observabilityNamespace); err != nil {
			report := unhealthy("Jaeger is not ready: "+err.Error(), "jaeger", "unhealthy")
			return &report, nil
		}
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "get", "--raw=/api/v1/namespaces/kubephos-observability/services/http:jaeger:16686/proxy/api/services"); err != nil {
			report := unhealthy("Managed Jaeger query endpoint is unavailable: "+err.Error(), "jaegerEndpoint", "unhealthy")
			return &report, nil
		}
		run.checks["jaeger"] = jaegerVersion
		return nil, nil
	},
	// The mesh telemetry exists only while Istio runs; otherwise its namespace is gone.
	remove: func(ctx context.Context, run *componentRun) error {
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, tracingManifest(run.marker, run.selected.istio()), "delete", "-f", "-", "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
			return fmt.Errorf("remove Jaeger tracing: %w", err)
		}
		return nil
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("jaeger", jaegerVersion, observabilityNamespace)
		builder.endpoint("jaeger", observabilityNamespace, "jaeger", jaegerPort)
	},
}

// tracingManifest returns the Jaeger resources and, with Istio, the mesh telemetry targeting them.
func tracingManifest(marker string, istio bool) []byte {
	value := extendedObservabilityManifest(marker)
	if !istio {
		value = bytes.Split(value, []byte("---\napiVersion: telemetry.istio.io"))[0]
	}
	return value
}

func extendedObservabilityManifest(marker string) []byte {
	return []byte(fmt.Sprintf(extendedObservabilityManifestTemplate, marker, jaegerPort, marker, marker, jaegerVersion, marker))
}

const extendedObservabilityManifestTemplate = `apiVersion: v1
kind: Service
metadata:
  name: jaeger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: jaeger}
  ports:
  - {name: otlp-grpc, port: 4317, targetPort: 4317}
  - {name: otlp-http, port: 4318, targetPort: 4318}
  - {name: ui, port: 16686, targetPort: 16686, nodePort: %d}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: jaeger-badger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: shared-storage
  resources: {requests: {storage: 5Gi}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  replicas: 1
  selector: {matchLabels: {app.kubernetes.io/name: jaeger}}
  template:
    metadata: {labels: {app.kubernetes.io/name: jaeger, app.kubernetes.io/managed-by: kubephos}}
    spec:
      nodeSelector: {kubephos.dev/role: management}
      containers:
      - name: jaeger
        image: jaegertracing/all-in-one:%s
        imagePullPolicy: IfNotPresent
        env:
        - {name: COLLECTOR_OTLP_ENABLED, value: "true"}
        - {name: SPAN_STORAGE_TYPE, value: badger}
        - {name: BADGER_EPHEMERAL, value: "false"}
        - {name: BADGER_DIRECTORY_VALUE, value: /badger/data}
        - {name: BADGER_DIRECTORY_KEY, value: /badger/key}
        ports: [{containerPort: 4317}, {containerPort: 4318}, {containerPort: 16686}]
        volumeMounts: [{name: data, mountPath: /badger}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: jaeger-badger}}]
---
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: kubephos-tracing
  namespace: istio-system
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  tracing:
  - providers: [{name: jaeger}]
    randomSamplingPercentage: 1
`
