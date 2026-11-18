package managedplatform

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/plugins"
)

// platformComponent is one component the managed platform installs. Everything specific
// to it lives in its component_*.go file; Execute, Verify, reconcile and Cleanup only walk
// platformComponents in order. Its catalog entry is in internal/managedcomponents.
type platformComponent struct {
	// enabled reports whether the selection installs the component.
	enabled func(selection) bool
	// namespace is a platform namespace the component owns, created before installing.
	namespace string
	charts    []chartSpec
	// releases are the Helm releases Verify expects for the component.
	releases []releaseRef
	// resources must be absent before a first installation.
	resources []resourceRef
	install   func(context.Context, *componentRun) error
	// verify returns a report only when the component is unhealthy.
	verify func(context.Context, *componentRun) (*domain.HealthReport, error)
	// remove uninstalls the component when a reconcile deselects it and during Cleanup.
	remove func(context.Context, *componentRun) error
	// publish adds the component and its endpoints to the platform capability.
	publish func(*capabilityBuilder)
}

// platformComponents in installation order; removal runs in reverse.
var platformComponents = []platformComponent{
	istioComponent,
	chaosComponent,
	cpaComponent,
	supportComponent,
	logsComponent,
	jaegerComponent,
	kialiComponent,
	headlampComponent,
}

type releaseRef struct{ name, namespace string }

type resourceRef struct{ name, namespace string }

// componentRun carries what a component step needs on one cluster.
type componentRun struct {
	runner     clusterRunner
	cluster    clusterConnection
	kubeconfig string
	marker     string
	selected   selection
	reconcile  bool
	charts     map[string][]byte
	log        plugins.Logger
	result     *result
	checks     map[string]string
	prometheus *prometheusService
}

// helm installs or upgrades a release from one of the downloaded charts.
func (run *componentRun) helm(ctx context.Context, chartID string, values []byte, release, namespace, timeout string, extra ...string) error {
	args := append([]string{"upgrade", "--install", release, "@chart", "--namespace", namespace, "--values", "@values", "--wait", "--timeout", timeout, "--atomic"}, extra...)
	_, err := run.runner.Helm(ctx, run.kubeconfig, run.charts[chartID], values, args...)
	return err
}

func (run *componentRun) uninstall(ctx context.Context, release, namespace string) error {
	if _, err := run.runner.Helm(ctx, run.kubeconfig, nil, nil, "uninstall", release, "--namespace", namespace, "--wait", "--timeout", "10m", "--ignore-not-found"); err != nil {
		return fmt.Errorf("remove %s: %w", release, err)
	}
	return nil
}

func (run *componentRun) deleteNamespace(ctx context.Context, namespace string) error {
	if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
		return fmt.Errorf("remove namespace %s: %w", namespace, err)
	}
	return nil
}

func (run *componentRun) rollout(ctx context.Context, workload, namespace string) error {
	_, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "rollout", "status", workload, "-n", namespace, "--timeout=5m")
	return err
}

// applyManifest validates a manifest server-side before applying it.
func (run *componentRun) applyManifest(ctx context.Context, manifest []byte) error {
	if _, err := run.runner.Kubectl(ctx, run.kubeconfig, manifest, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return fmt.Errorf("admission: %w", err)
	}
	_, err := run.runner.Kubectl(ctx, run.kubeconfig, manifest, "apply", "-f", "-")
	return err
}

// managedPrometheus discovers the observability Prometheus once per run.
func (run *componentRun) managedPrometheus(ctx context.Context) (prometheusService, error) {
	if run.prometheus != nil {
		return *run.prometheus, nil
	}
	service, err := discoverPrometheusService(ctx, run.runner, run.kubeconfig)
	if err != nil {
		return prometheusService{}, err
	}
	run.prometheus = &service
	return service, nil
}

// capabilityBuilder collects what the installed components publish.
type capabilityBuilder struct {
	components []managedComponent
	endpoints  []managedEndpoint
	versions   []string
}

func (builder *capabilityBuilder) component(name, version, namespace string) {
	builder.components = append(builder.components, managedComponent{Name: name, Version: version, Namespace: namespace, Status: "ready"})
}

func (builder *capabilityBuilder) endpoint(name, namespace, service string, nodePort int) {
	builder.endpoints = append(builder.endpoints, managedEndpoint{Name: name, Namespace: namespace, Service: service, Scheme: "http", NodePort: nodePort})
}

func (builder *capabilityBuilder) version(value string) {
	builder.versions = append(builder.versions, value)
}

var ownershipMarkerPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// selection is the set of components a step installs. Steps saved before component
// selection carry no list and install the full set for their network.
type selection struct {
	ids        map[string]bool
	networking string
}

func newSelection(spec Spec, networking string) selection {
	values := spec.Components
	if values == nil {
		values = managedcomponents.DefaultClusterComponents(networking)
	}
	ids := map[string]bool{}
	for _, id := range values {
		ids[id] = true
	}
	return selection{ids: ids, networking: networking}
}

func (s selection) has(id string) bool { return s.ids[id] }
func (s selection) cilium() bool       { return s.networking == "cilium" }
func (s selection) istio() bool {
	return !s.cilium() && (s.has(managedcomponents.IstioSidecar) || s.has(managedcomponents.IstioAmbient))
}

func (s selection) components() []platformComponent {
	values := []platformComponent{}
	for _, component := range platformComponents {
		if component.enabled(s) {
			values = append(values, component)
		}
	}
	return values
}

// namespaces lists the platform namespaces the selection owns; kubephos-system is always kept.
func (s selection) namespaces() []string {
	values := []string{}
	for _, component := range s.components() {
		if component.namespace != "" {
			values = append(values, component.namespace)
		}
	}
	return append(values, systemNamespace)
}

func (s selection) charts() []chartSpec {
	values := []chartSpec{}
	for _, component := range s.components() {
		values = append(values, component.charts...)
	}
	return values
}

func (s selection) releases() []releaseRef {
	values := []releaseRef{}
	for _, component := range s.components() {
		values = append(values, component.releases...)
	}
	return values
}

func (s selection) resources() []resourceRef {
	values := []resourceRef{}
	for _, component := range s.components() {
		values = append(values, component.resources...)
	}
	return values
}

func (s selection) capability(server string) managedPlatformCapability {
	builder := &capabilityBuilder{}
	for _, component := range s.components() {
		component.publish(builder)
	}
	if s.cilium() {
		builder.component("cilium", "1.20.3", "kube-system")
		builder.endpoint("hubble", "kube-system", "hubble-ui", 31235)
		builder.versions = append([]string{"cilium.1.20.3"}, builder.versions...)
	}
	return managedPlatformCapability{
		APIVersion: artifactAPI, Kind: "ManagedPlatformCapability",
		Metadata: managedPlatformCapabilityMetadata{Name: "managed-platform", Version: strings.Join(builder.versions, "+")},
		Spec:     managedPlatformCapabilitySpec{ClusterServer: server, Components: builder.components, Endpoints: builder.endpoints},
	}
}

// summary names the installed optional components for validation messages.
func (s selection) summary() string {
	names := []string{}
	for _, component := range managedcomponents.ClusterCatalog() {
		if !component.Core && s.has(component.ID) && !slices.Contains(component.UnsupportedNetworking, s.networking) {
			names = append(names, component.Title)
		}
	}
	if len(names) == 0 {
		return "only the core measurement services"
	}
	return strings.Join(names, ", ")
}
