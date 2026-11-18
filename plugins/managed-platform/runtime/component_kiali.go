package managedplatform

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Kiali is the service mesh console; it reads the managed Prometheus.
const (
	kialiVersion = "2.8.0"
	kialiRelease = "kubephos-kiali"
	kialiPort    = 30001
)

var kialiComponent = platformComponent{
	enabled:  func(s selection) bool { return s.istio() && s.has(managedcomponents.Kiali) },
	charts:   []chartSpec{{ID: "kiali", URL: "https://kiali.org/helm-charts/kiali-server-2.8.0.tgz", Digest: "c4f1aa6530b75a3fed5950e1c8ab6a5c92733720a58dcdc123462401e468251b"}},
	releases: []releaseRef{{kialiRelease, istioNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		prometheus, err := run.managedPrometheus(ctx)
		if err != nil {
			return fmt.Errorf("discover managed Prometheus for Kiali: %w", err)
		}
		if err := run.helm(ctx, "kiali", kialiValues(prometheus), kialiRelease, istioNamespace, "10m"); err != nil {
			return fmt.Errorf("install Kiali: %w", err)
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		prometheus, err := run.managedPrometheus(ctx)
		if err != nil {
			report := unhealthy("Managed Prometheus discovery failed: "+err.Error(), "prometheusDiscovery", "unhealthy")
			return &report, nil
		}
		config, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "get", "configmap", "kiali", "-n", istioNamespace, "-o", "jsonpath={.data.config\\.yaml}")
		if err != nil {
			report := unhealthy("Kiali configuration is unavailable: "+err.Error(), "kialiPrometheus", "unhealthy")
			return &report, nil
		}
		if err := validateKialiPrometheusConfig(config, prometheus); err != nil {
			report := unhealthy(err.Error(), "kialiPrometheus", "invalid")
			return &report, nil
		}
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "exec", "deployment/kiali", "-n", istioNamespace, "--", "getent", "hosts", prometheus.Hostname()); err != nil {
			report := unhealthy("Kiali cannot resolve the managed Prometheus service: "+err.Error(), "kialiPrometheusDNS", "unhealthy")
			return &report, nil
		}
		run.checks["kiali"], run.checks["kialiPrometheus"] = kialiVersion, prometheus.Name
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		if run.selected.cilium() {
			return nil
		}
		return run.uninstall(ctx, kialiRelease, istioNamespace)
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("kiali", kialiVersion, istioNamespace)
		builder.endpoint("kiali", istioNamespace, "kiali", kialiPort)
	},
}

func kialiValues(prometheus prometheusService) []byte {
	return []byte(fmt.Sprintf(`auth:
  strategy: anonymous
deployment:
  image_version: v%s
  image_pull_policy: IfNotPresent
  node_selector: {kubephos.dev/role: management}
  service_type: NodePort
external_services:
  grafana:
    enabled: true
    internal_url: http://kubephos-observability-grafana.kubephos-observability.svc.cluster.local:80
  prometheus:
    url: %s
  tracing:
    enabled: true
    provider: jaeger
    internal_url: http://jaeger.kubephos-observability.svc.cluster.local:16686
    use_grpc: false
istio_namespace: istio-system
server:
  node_port: %d
  web_root: /kiali
`, kialiVersion, prometheus.URL(), kialiPort))
}

func validateKialiPrometheusConfig(raw string, prometheus prometheusService) error {
	var config struct {
		ExternalServices struct {
			Grafana struct {
				Enabled     bool   `yaml:"enabled"`
				InternalURL string `yaml:"internal_url"`
			} `yaml:"grafana"`
			Prometheus struct {
				URL string `yaml:"url"`
			} `yaml:"prometheus"`
			Tracing struct {
				Enabled     bool   `yaml:"enabled"`
				Provider    string `yaml:"provider"`
				InternalURL string `yaml:"internal_url"`
				UseGRPC     bool   `yaml:"use_grpc"`
			} `yaml:"tracing"`
		} `yaml:"external_services"`
	}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		return err
	}
	if config.ExternalServices.Prometheus.URL != prometheus.URL() {
		return fmt.Errorf("Kiali Prometheus URL is %q, expected %q", config.ExternalServices.Prometheus.URL, prometheus.URL())
	}
	if !config.ExternalServices.Grafana.Enabled || config.ExternalServices.Grafana.InternalURL != "http://kubephos-observability-grafana.kubephos-observability.svc.cluster.local:80" {
		return errors.New("Kiali managed Grafana integration is invalid")
	}
	if !config.ExternalServices.Tracing.Enabled || config.ExternalServices.Tracing.Provider != "jaeger" || config.ExternalServices.Tracing.InternalURL != "http://jaeger.kubephos-observability.svc.cluster.local:16686" || config.ExternalServices.Tracing.UseGRPC {
		return errors.New("Kiali managed Jaeger integration is invalid")
	}
	return nil
}
