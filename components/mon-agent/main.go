package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultPrometheusURL = "http://prometheus-stack-kube-prom-prometheus.observability:9090"
	defaultPeriodSeconds = 30
	defaultPromQLRange   = "5m"
	queryTimeout         = 10 * time.Second
)

type config struct {
	PrometheusURL          string
	ScrapePeriod           time.Duration
	PromQLRange            string
	NamespaceLabelSelector string
}

type deploymentRef struct {
	Namespace string
	Name      string
	App       string
	Group     string
}

type agent struct {
	cfg  config
	kube kubernetes.Interface
	prom promv1.API
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	kube, err := newKubeClient()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}

	prom, err := newPromClient(cfg.PrometheusURL)
	if err != nil {
		log.Fatalf("prometheus client: %v", err)
	}

	a := &agent{cfg: cfg, kube: kube, prom: prom}
	log.Printf("mon-agent started prometheus=%s period=%s namespace_selector=%q range=%s",
		cfg.PrometheusURL, cfg.ScrapePeriod, cfg.NamespaceLabelSelector, cfg.PromQLRange)

	ticker := time.NewTicker(cfg.ScrapePeriod)
	defer ticker.Stop()

	for {
		start := time.Now()
		if err := a.runOnce(context.Background()); err != nil {
			log.Printf("scrape failed: %v", err)
		} else {
			log.Printf("scrape completed in %s", time.Since(start).Truncate(time.Millisecond))
		}

		<-ticker.C
	}
}

func loadConfig() (config, error) {
	period := defaultPeriodSeconds
	if raw := os.Getenv("SCRAPE_PERIOD_SECONDS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return config{}, fmt.Errorf("SCRAPE_PERIOD_SECONDS must be a positive integer")
		}
		period = parsed
	}

	promQLRange := envOr("PROMQL_RANGE", defaultPromQLRange)
	if !regexp.MustCompile(`^[0-9]+[smhdwy]$`).MatchString(promQLRange) {
		return config{}, fmt.Errorf("PROMQL_RANGE must be a Prometheus duration such as 30s, 5m, or 1h")
	}

	selector := strings.TrimSpace(os.Getenv("NAMESPACE_LABEL_SELECTOR"))
	if selector != "" {
		if _, err := labels.Parse(selector); err != nil {
			return config{}, fmt.Errorf("NAMESPACE_LABEL_SELECTOR: %w", err)
		}
	}

	return config{
		PrometheusURL:          envOr("PROMETHEUS_URL", defaultPrometheusURL),
		ScrapePeriod:           time.Duration(period) * time.Second,
		PromQLRange:            promQLRange,
		NamespaceLabelSelector: selector,
	}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func newPromClient(address string) (promv1.API, error) {
	client, err := promapi.NewClient(promapi.Config{Address: address})
	if err != nil {
		return nil, err
	}
	return promv1.NewAPI(client), nil
}

func newKubeClient() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				kubeconfig = filepath.Join(home, ".kube", "config")
			}
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}
	return kubernetes.NewForConfig(cfg)
}

func (a *agent) runOnce(ctx context.Context) error {
	namespaces, err := a.selectedNamespaces(ctx)
	if err != nil {
		return err
	}
	deployments, err := a.selectedDeployments(ctx, namespaces)
	if err != nil {
		return err
	}
	nodes, err := a.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	namespaceRegex := regexFor(namespaces)
	nodeAnnotations, err := a.nodeAnnotations(ctx, nodes.Items)
	if err != nil {
		log.Printf("node metrics: %v", err)
	}
	deploymentAnnotations, err := a.deploymentAnnotations(ctx, namespaceRegex, deployments)
	if err != nil {
		log.Printf("deployment metrics: %v", err)
	}

	for _, node := range nodes.Items {
		if annotations := nodeAnnotations[node.Name]; len(annotations) > 0 {
			if err := a.patchNode(ctx, &node, annotations); err != nil {
				log.Printf("patch node %s: %v", node.Name, err)
			}
		}
	}
	for _, dep := range deployments {
		key := deploymentKey(dep.Namespace, dep.Name)
		if annotations := deploymentAnnotations[key]; len(annotations) > 0 {
			current, err := a.kube.AppsV1().Deployments(dep.Namespace).Get(ctx, dep.Name, metav1.GetOptions{})
			if err != nil {
				log.Printf("get deployment %s/%s: %v", dep.Namespace, dep.Name, err)
				continue
			}
			if err := a.patchDeployment(ctx, current, annotations); err != nil {
				log.Printf("patch deployment %s/%s: %v", dep.Namespace, dep.Name, err)
			}
		}
	}

	return nil
}

func (a *agent) selectedNamespaces(ctx context.Context) ([]string, error) {
	list, err := a.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: a.cfg.NamespaceLabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (a *agent) selectedDeployments(ctx context.Context, namespaces []string) ([]deploymentRef, error) {
	deployments := make([]deploymentRef, 0)
	for _, namespace := range namespaces {
		list, err := a.kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list deployments in namespace %s: %w", namespace, err)
		}
		for _, dep := range list.Items {
			labels := dep.GetLabels()
			appName := labels["app"]
			if appName == "" {
				appName = dep.Name
			}
			deployments = append(deployments, deploymentRef{
				Namespace: namespace,
				Name:      dep.Name,
				App:       appName,
				Group:     labels["group"],
			})
		}
	}
	return deployments, nil
}

func (a *agent) nodeAnnotations(ctx context.Context, nodes []corev1.Node) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	nodeNames := nodeNameIndex(nodes)

	cpu, err := a.queryVector(ctx, `1000 * sum by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[`+a.cfg.PromQLRange+`]))`)
	if err != nil {
		return out, fmt.Errorf("node cpu query: %w", err)
	}
	for _, sample := range cpu {
		node := nodeNameFromMetric(sample.Metric, nodeNames)
		if node != "" {
			put(out, node, "cpu-usage", formatFloat(float64(sample.Value)))
		}
	}

	mem, err := a.queryVector(ctx, `avg by (instance) (node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes)`)
	if err != nil {
		return out, fmt.Errorf("node memory query: %w", err)
	}
	for _, sample := range mem {
		node := nodeNameFromMetric(sample.Metric, nodeNames)
		if node != "" {
			put(out, node, "memory-usage", formatFloat(float64(sample.Value)))
		}
	}

	disk, err := a.queryVector(ctx, `sum by (instance) (rate(node_disk_read_bytes_total{device!~"^(ram|loop|fd|sr|dm-|md).*"}[`+a.cfg.PromQLRange+`]) + rate(node_disk_written_bytes_total{device!~"^(ram|loop|fd|sr|dm-|md).*"}[`+a.cfg.PromQLRange+`]))`)
	if err != nil {
		return out, fmt.Errorf("node disk bandwidth query: %w", err)
	}
	for _, sample := range disk {
		node := nodeNameFromMetric(sample.Metric, nodeNames)
		if node != "" {
			put(out, node, "disk-throughput", formatFloat(float64(sample.Value)))
		}
	}

	network, err := a.queryVector(ctx, `sum by (instance) (rate(node_network_receive_bytes_total{device!="lo"}[`+a.cfg.PromQLRange+`]) + rate(node_network_transmit_bytes_total{device!="lo"}[`+a.cfg.PromQLRange+`]))`)
	if err != nil {
		return out, fmt.Errorf("node network bandwidth query: %w", err)
	}
	for _, sample := range network {
		node := nodeNameFromMetric(sample.Metric, nodeNames)
		if node != "" {
			put(out, node, "network-throughput", formatFloat(float64(sample.Value)))
		}
	}

	latencyQuery := `1000 * sum by (origin_node, destination_node) (rate(node_latency_sum[` + a.cfg.PromQLRange + `])) / sum by (origin_node, destination_node) (rate(node_latency_count[` + a.cfg.PromQLRange + `]))`
	latencies, err := a.queryVector(ctx, latencyQuery)
	if err != nil {
		return out, fmt.Errorf("node latency query: %w", err)
	}
	for _, sample := range latencies {
		annotateNodePeerMetric(out, sample, "network-latency")
	}

	bandwidth, err := a.queryVector(ctx, `max by (origin_node, destination_node) (node_bandwidth_bytes_per_second)`)
	if err != nil {
		return out, fmt.Errorf("node bandwidth query: %w", err)
	}
	for _, sample := range bandwidth {
		annotateNodePeerMetric(out, sample, "network-bandwidth")
	}

	packetLoss, err := a.queryVector(ctx, packetLossQuery(a.cfg.PromQLRange))
	if err != nil {
		return out, fmt.Errorf("node packet loss query: %w", err)
	}
	for _, sample := range packetLoss {
		annotatePacketLoss(out, sample)
	}
	for node := range out {
		put(out, node, "network-latency."+node, "0")
		put(out, node, "packet-loss."+node, "0")
	}

	return out, nil
}

func annotateNodePeerMetric(out map[string]map[string]string, sample *model.Sample, prefix string) {
	origin := string(sample.Metric["origin_node"])
	destination := string(sample.Metric["destination_node"])
	if origin == "" || destination == "" {
		return
	}
	put(out, origin, prefix+"."+destination, formatFloat(float64(sample.Value)))
}

func packetLossQuery(promQLRange string) string {
	return `avg by (origin_node, destination_node) (avg_over_time(node_packet_loss_ratio[` + promQLRange + `]))`
}

func annotatePacketLoss(out map[string]map[string]string, sample *model.Sample) {
	origin := string(sample.Metric["origin_node"])
	destination := string(sample.Metric["destination_node"])
	if origin == "" || destination == "" {
		return
	}
	put(out, origin, "packet-loss."+destination, formatFloat(100*float64(sample.Value)))
}

func (a *agent) deploymentAnnotations(ctx context.Context, namespaceRegex string, deployments []deploymentRef) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	byAppGroup := map[string][]deploymentRef{}
	byWorkload := map[string]deploymentRef{}
	for _, dep := range deployments {
		byAppGroup[appGroupKey(dep.Namespace, dep.App, dep.Group)] = append(byAppGroup[appGroupKey(dep.Namespace, dep.App, dep.Group)], dep)
		byWorkload[workloadKey(dep.Namespace, dep.Name)] = dep
	}

	cpuQuery := `1000 * sum by (namespace, label_group, label_app) (` +
		`rate(container_cpu_usage_seconds_total{namespace=~"` + namespaceRegex + `",container!="",image!=""}[` + a.cfg.PromQLRange + `]) ` +
		`* on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~"` + namespaceRegex + `",label_app!=""})`
	cpu, err := a.queryVector(ctx, cpuQuery)
	if err != nil {
		return out, fmt.Errorf("deployment cpu query: %w", err)
	}
	for _, sample := range cpu {
		for _, dep := range byAppGroup[appGroupKey(string(sample.Metric["namespace"]), string(sample.Metric["label_app"]), string(sample.Metric["label_group"]))] {
			put(out, deploymentKey(dep.Namespace, dep.Name), "cpu-usage", formatFloat(float64(sample.Value)))
		}
	}

	memQuery := `sum by (namespace, label_group, label_app) (` +
		`container_memory_working_set_bytes{namespace=~"` + namespaceRegex + `",container!="",image!=""} ` +
		`* on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~"` + namespaceRegex + `",label_app!=""})`
	mem, err := a.queryVector(ctx, memQuery)
	if err != nil {
		return out, fmt.Errorf("deployment memory query: %w", err)
	}
	for _, sample := range mem {
		for _, dep := range byAppGroup[appGroupKey(string(sample.Metric["namespace"]), string(sample.Metric["label_app"]), string(sample.Metric["label_group"]))] {
			put(out, deploymentKey(dep.Namespace, dep.Name), "memory-usage", formatFloat(float64(sample.Value)))
		}
	}

	diskQuery := `sum by (namespace, label_group, label_app) ((` +
		`rate(container_fs_reads_bytes_total{namespace=~"` + namespaceRegex + `",container!="",image!=""}[` + a.cfg.PromQLRange + `]) + ` +
		`rate(container_fs_writes_bytes_total{namespace=~"` + namespaceRegex + `",container!="",image!=""}[` + a.cfg.PromQLRange + `])) ` +
		`* on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~"` + namespaceRegex + `",label_app!=""})`
	disk, err := a.queryVector(ctx, diskQuery)
	if err != nil {
		return out, fmt.Errorf("deployment disk bandwidth query: %w", err)
	}
	for _, sample := range disk {
		for _, dep := range byAppGroup[appGroupKey(string(sample.Metric["namespace"]), string(sample.Metric["label_app"]), string(sample.Metric["label_group"]))] {
			put(out, deploymentKey(dep.Namespace, dep.Name), "disk-throughput", formatFloat(float64(sample.Value)))
		}
	}

	networkQuery := `sum by (namespace, label_group, label_app) ((` +
		`rate(container_network_receive_bytes_total{namespace=~"` + namespaceRegex + `",pod!="",interface!="lo"}[` + a.cfg.PromQLRange + `]) + ` +
		`rate(container_network_transmit_bytes_total{namespace=~"` + namespaceRegex + `",pod!="",interface!="lo"}[` + a.cfg.PromQLRange + `])) ` +
		`* on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~"` + namespaceRegex + `",label_app!=""})`
	network, err := a.queryVector(ctx, networkQuery)
	if err != nil {
		return out, fmt.Errorf("deployment network bandwidth query: %w", err)
	}
	for _, sample := range network {
		for _, dep := range byAppGroup[appGroupKey(string(sample.Metric["namespace"]), string(sample.Metric["label_app"]), string(sample.Metric["label_group"]))] {
			put(out, deploymentKey(dep.Namespace, dep.Name), "network-throughput", formatFloat(float64(sample.Value)))
		}
	}

	rpsQuery := `sum by (source_workload_namespace, source_workload, destination_workload_namespace, destination_workload) (` +
		`rate(istio_requests_total{reporter="destination",destination_workload_namespace=~"` + namespaceRegex + `",source_workload!="unknown",destination_workload!="unknown"}[` + a.cfg.PromQLRange + `]))`
	rps, err := a.queryVector(ctx, rpsQuery)
	if err != nil {
		return out, fmt.Errorf("istio rps query: %w", err)
	}
	for _, sample := range rps {
		a.annotatePeerMetric(out, byWorkload, sample.Metric, "rps", float64(sample.Value))
	}

	trafficQuery := `sum by (source_workload_namespace, source_workload, destination_workload_namespace, destination_workload) (` +
		`rate(istio_request_bytes_sum{reporter="destination",destination_workload_namespace=~"` + namespaceRegex + `",source_workload!="unknown",destination_workload!="unknown"}[` + a.cfg.PromQLRange + `]) + ` +
		`rate(istio_response_bytes_sum{reporter="destination",destination_workload_namespace=~"` + namespaceRegex + `",source_workload!="unknown",destination_workload!="unknown"}[` + a.cfg.PromQLRange + `])) ` +
		`or sum by (source_workload_namespace, source_workload, destination_workload_namespace, destination_workload) (` +
		`rate(istio_tcp_sent_bytes_total{reporter="destination",destination_workload_namespace=~"` + namespaceRegex + `",source_workload!="unknown",destination_workload!="unknown"}[` + a.cfg.PromQLRange + `]) + ` +
		`rate(istio_tcp_received_bytes_total{reporter="destination",destination_workload_namespace=~"` + namespaceRegex + `",source_workload!="unknown",destination_workload!="unknown"}[` + a.cfg.PromQLRange + `]))`
	traffic, err := a.queryVector(ctx, trafficQuery)
	if err != nil {
		return out, fmt.Errorf("istio traffic query: %w", err)
	}
	for _, sample := range traffic {
		a.annotatePeerMetric(out, byWorkload, sample.Metric, "traffic", float64(sample.Value))
	}

	return out, nil
}

func (a *agent) annotatePeerMetric(out map[string]map[string]string, byWorkload map[string]deploymentRef, metric model.Metric, prefix string, value float64) {
	sourceKey := workloadKey(string(metric["source_workload_namespace"]), string(metric["source_workload"]))
	destinationKey := workloadKey(string(metric["destination_workload_namespace"]), string(metric["destination_workload"]))

	source, sourceSelected := byWorkload[sourceKey]
	destination, destinationSelected := byWorkload[destinationKey]
	if destinationSelected {
		peer := string(metric["source_workload"])
		put(out, deploymentKey(destination.Namespace, destination.Name), prefix+"."+peer, formatFloat(value))
	}
	if sourceSelected {
		peer := string(metric["destination_workload"])
		put(out, deploymentKey(source.Namespace, source.Name), prefix+"."+peer, formatFloat(value))
	}
}

func (a *agent) queryVector(ctx context.Context, query string) (model.Vector, error) {
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	value, warnings, err := a.prom.Query(qctx, query, time.Now())
	if len(warnings) > 0 {
		log.Printf("prometheus warnings for %q: %v", query, warnings)
	}
	if err != nil {
		return nil, err
	}
	vector, ok := value.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("expected vector result, got %T", value)
	}
	return vector, nil
}

func (a *agent) patchNode(ctx context.Context, node *corev1.Node, annotations map[string]string) error {
	merged := mergeAnnotations(node.GetAnnotations(), annotations)
	if annotationsEqual(node.GetAnnotations(), merged) {
		return nil
	}
	patch, err := annotationPatch(merged)
	if err != nil {
		return err
	}
	_, err = a.kube.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (a *agent) patchDeployment(ctx context.Context, dep *appsv1.Deployment, annotations map[string]string) error {
	merged := mergeAnnotations(dep.GetAnnotations(), annotations)
	if annotationsEqual(dep.GetAnnotations(), merged) {
		return nil
	}
	patch, err := annotationPatch(merged)
	if err != nil {
		return err
	}
	_, err = a.kube.AppsV1().Deployments(dep.Namespace).Patch(ctx, dep.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func annotationPatch(annotations map[string]string) ([]byte, error) {
	values := make(map[string]any, len(annotations)+2)
	for key, value := range annotations {
		values[key] = value
	}
	values["disk-bandwidth"] = nil
	values["network-bandwidth"] = nil
	return json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": values,
		},
	})
}

func mergeAnnotations(current, updates map[string]string) map[string]string {
	merged := make(map[string]string, len(current)+len(updates))
	for key, value := range current {
		if key == "disk-bandwidth" || key == "network-bandwidth" {
			continue
		}
		merged[key] = value
	}
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func annotationsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func put(values map[string]map[string]string, objectName, annotation, value string) {
	if values[objectName] == nil {
		values[objectName] = map[string]string{}
	}
	values[objectName][annotation] = value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func nodeNameIndex(nodes []corev1.Node) map[string]string {
	values := make(map[string]string, len(nodes)*3)
	for _, node := range nodes {
		values[node.Name] = node.Name
		for _, address := range node.Status.Addresses {
			if address.Address != "" {
				values[address.Address] = node.Name
			}
		}
	}
	return values
}

func nodeNameFromMetric(metric model.Metric, nodeNames map[string]string) string {
	for _, label := range []model.LabelName{"node", "node_name", "nodename", "instance"} {
		value := string(metric[label])
		if value == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		} else if strings.Count(value, ":") == 1 {
			value = strings.SplitN(value, ":", 2)[0]
		}
		if nodeName := nodeNames[value]; nodeName != "" {
			return nodeName
		}
	}
	return ""
}

func regexFor(values []string) string {
	if len(values) == 0 {
		return "$^"
	}
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		escaped = append(escaped, regexp.QuoteMeta(value))
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

func appGroupKey(namespace, app, group string) string {
	return namespace + "/" + group + "/" + app
}

func workloadKey(namespace, workload string) string {
	return namespace + "/" + workload
}

func deploymentKey(namespace, name string) string {
	return namespace + "/" + name
}
