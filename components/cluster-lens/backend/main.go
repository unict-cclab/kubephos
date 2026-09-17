package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultAddr      = "127.0.0.1:8088"
	defaultRefresh   = 2 * time.Second
	defaultStaticDir = "../frontend"
)

type snapshot struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Context     string     `json:"context"`
	Nodes       []nodeView `json:"nodes"`
	NodeEdges   []edgeView `json:"nodeEdges"`
	Pods        []podView  `json:"pods"`
	AppEdges    []edgeView `json:"appEdges"`
	Warnings    []string   `json:"warnings,omitempty"`
}

type nodeView struct {
	Name        string            `json:"name"`
	Zone        string            `json:"zone,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Ready       bool              `json:"ready"`
	Role        string            `json:"role,omitempty"`
	CPU         float64           `json:"cpu,omitempty"`
	Memory      float64           `json:"memory,omitempty"`
	Disk        float64           `json:"disk,omitempty"`
	Network     float64           `json:"network,omitempty"`
}

type podView struct {
	Namespace  string            `json:"namespace"`
	Name       string            `json:"name"`
	Node       string            `json:"node,omitempty"`
	Phase      string            `json:"phase,omitempty"`
	Group      string            `json:"group,omitempty"`
	App        string            `json:"app,omitempty"`
	Index      string            `json:"index,omitempty"`
	Owner      string            `json:"owner,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Metrics    map[string]string `json:"metrics,omitempty"`
	CreatedAt  string            `json:"createdAt,omitempty"`
	Containers []string          `json:"containers,omitempty"`
}

type edgeView struct {
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Kind       string   `json:"kind"`
	Value      float64  `json:"value,omitempty"`
	Bandwidth  *float64 `json:"bandwidth,omitempty"`
	PacketLoss *float64 `json:"packetLoss,omitempty"`
	Label      string   `json:"label,omitempty"`
}

type server struct {
	staticDir string
	refresh   time.Duration
	kube      kubernetes.Interface
	context   string
}

func main() {
	addr := envOr("CLUSTER_LENS_ADDR", defaultAddr)
	staticDir := envOr("CLUSTER_LENS_STATIC_DIR", defaultStaticDir)
	refresh := defaultRefresh
	if raw := strings.TrimSpace(os.Getenv("CLUSTER_LENS_REFRESH")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			log.Fatalf("CLUSTER_LENS_REFRESH must be a positive duration")
		}
		refresh = parsed
	}

	kube, contextName, err := newKubeClient()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}

	s := &server{staticDir: staticDir, refresh: refresh, kube: kube, context: contextName}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/snapshot", s.snapshot)
	mux.HandleFunc("/api/config", s.config)
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	log.Printf("cluster-lens listening on http://%s", addr)
	log.Printf("serving frontend from %s", filepath.Clean(staticDir))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *server) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"refreshMs": s.refresh.Milliseconds(),
	})
}

func (s *server) snapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := s.buildSnapshot(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, snap)
}

func (s *server) buildSnapshot(ctx context.Context) (snapshot, error) {
	var warnings []string

	nodes, err := s.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot{}, fmt.Errorf("list nodes: %w", err)
	}
	pods, err := s.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot{}, fmt.Errorf("list pods: %w", err)
	}
	deployments, err := s.kube.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("list deployments: %v", err))
		deployments = &appsv1.DeploymentList{}
	}
	replicaSets, err := s.kube.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("list replicasets: %v", err))
		replicaSets = &appsv1.ReplicaSetList{}
	}

	deploymentByKey := map[string]appsv1.Deployment{}
	for _, deployment := range deployments.Items {
		deploymentByKey[key(deployment.Namespace, deployment.Name)] = deployment
	}
	rsOwner := map[string]string{}
	for _, rs := range replicaSets.Items {
		for _, owner := range rs.OwnerReferences {
			if owner.Kind == "Deployment" {
				rsOwner[key(rs.Namespace, rs.Name)] = owner.Name
				break
			}
		}
	}

	snap := snapshot{
		GeneratedAt: time.Now(),
		Context:     s.context,
		Nodes:       make([]nodeView, 0, len(nodes.Items)),
		NodeEdges:   make([]edgeView, 0),
		Pods:        make([]podView, 0, len(pods.Items)),
		AppEdges:    make([]edgeView, 0),
		Warnings:    warnings,
	}

	for _, node := range nodes.Items {
		annotations := node.Annotations
		view := nodeView{
			Name:        node.Name,
			Zone:        node.Labels["topology.kubernetes.io/zone"],
			Labels:      node.Labels,
			Annotations: selectedNodeAnnotations(annotations),
			Ready:       nodeReady(node),
			Role:        nodeRole(node.Labels),
			CPU:         parseFloat(annotations["cpu-usage"]),
			Memory:      parseFloat(annotations["memory-usage"]),
			Disk:        parseFloat(annotationValue(annotations, "disk-throughput", "disk-bandwidth")),
			Network:     parseFloat(annotationValue(annotations, "network-throughput", "network-bandwidth")),
		}
		snap.Nodes = append(snap.Nodes, view)
		appendNodeEdges(&snap, node.Name, annotations)
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" {
			continue
		}
		owner := podOwnerDeployment(pod, rsOwner)
		metrics := map[string]string{}
		if owner != "" {
			if deployment, ok := deploymentByKey[key(pod.Namespace, owner)]; ok {
				metrics = selectedMetricAnnotations(deployment.Annotations)
				appendAppEdges(&snap, deployment)
			}
		}
		containers := make([]string, 0, len(pod.Spec.Containers))
		for _, container := range pod.Spec.Containers {
			containers = append(containers, container.Name)
		}
		sort.Strings(containers)

		labels := pod.Labels
		snap.Pods = append(snap.Pods, podView{
			Namespace:  pod.Namespace,
			Name:       pod.Name,
			Node:       pod.Spec.NodeName,
			Phase:      string(pod.Status.Phase),
			Group:      labels["group"],
			App:        labelOr(labels, "app", owner),
			Index:      labels["index"],
			Owner:      owner,
			Labels:     labels,
			Metrics:    metrics,
			CreatedAt:  pod.CreationTimestamp.Format(time.RFC3339),
			Containers: containers,
		})
	}

	sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].Name < snap.Nodes[j].Name })
	sort.Slice(snap.NodeEdges, func(i, j int) bool {
		if snap.NodeEdges[i].Source == snap.NodeEdges[j].Source {
			return snap.NodeEdges[i].Target < snap.NodeEdges[j].Target
		}
		return snap.NodeEdges[i].Source < snap.NodeEdges[j].Source
	})
	sort.Slice(snap.Pods, func(i, j int) bool {
		return key(snap.Pods[i].Namespace, snap.Pods[i].Name) < key(snap.Pods[j].Namespace, snap.Pods[j].Name)
	})

	snap.AppEdges = dedupeEdges(snap.AppEdges)
	return snap, nil
}

func appendAppEdges(snap *snapshot, deployment appsv1.Deployment) {
	app := labelOr(deployment.Labels, "app", deployment.Name)
	source := key(deployment.Namespace, app)
	for annotation, raw := range deployment.Annotations {
		if strings.HasPrefix(annotation, "rps.") {
			peer := strings.TrimPrefix(annotation, "rps.")
			value := parseFloat(raw)
			snap.AppEdges = append(snap.AppEdges, edgeView{
				Source: source,
				Target: key(deployment.Namespace, peer),
				Kind:   "rps",
				Value:  value,
				Label:  formatFloat(value) + " rps",
			})
		}
		if strings.HasPrefix(annotation, "traffic.") {
			peer := strings.TrimPrefix(annotation, "traffic.")
			value := parseFloat(raw)
			snap.AppEdges = append(snap.AppEdges, edgeView{
				Source: source,
				Target: key(deployment.Namespace, peer),
				Kind:   "traffic",
				Value:  value,
				Label:  formatBytes(value) + "/s",
			})
		}
	}
}

func podOwnerDeployment(pod corev1.Pod, rsOwner map[string]string) string {
	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "Deployment":
			return owner.Name
		case "ReplicaSet":
			if deployment := rsOwner[key(pod.Namespace, owner.Name)]; deployment != "" {
				return deployment
			}
			return owner.Name
		}
	}
	return ""
}

func nodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeRole(labels map[string]string) string {
	if labels["node-role.kubernetes.io/control-plane"] == "true" {
		return "control-plane"
	}
	if labels["nodepool"] != "" {
		return labels["nodepool"]
	}
	return "worker"
}

func selectedAnnotations(annotations map[string]string, names ...string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		if value := annotations[name]; value != "" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendNodeEdges(snap *snapshot, source string, annotations map[string]string) {
	edges := map[string]*edgeView{}
	for annotation, raw := range annotations {
		prefix := ""
		kind := ""
		switch {
		case strings.HasPrefix(annotation, "network-latency."):
			prefix = "network-latency."
			kind = "latency"
		case strings.HasPrefix(annotation, "network-bandwidth."):
			prefix = "network-bandwidth."
			kind = "bandwidth"
		case strings.HasPrefix(annotation, "packet-loss."):
			prefix = "packet-loss."
			kind = "packet-loss"
		default:
			continue
		}

		target := strings.TrimPrefix(annotation, prefix)
		if target == "" || target == source {
			continue
		}
		edge := edges[target]
		if edge == nil {
			edge = &edgeView{Source: source, Target: target, Kind: "network"}
			edges[target] = edge
		}
		value := parseFloat(raw)
		switch kind {
		case "latency":
			edge.Value = value
			edge.Label = formatMS(value)
		case "bandwidth":
			edge.Bandwidth = floatPointer(value)
		case "packet-loss":
			edge.PacketLoss = floatPointer(value)
		}
	}

	for _, edge := range edges {
		snap.NodeEdges = append(snap.NodeEdges, *edge)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

func annotationValue(annotations map[string]string, name, legacyName string) string {
	if value := annotations[name]; value != "" {
		return value
	}
	return annotations[legacyName]
}

func selectedNodeAnnotations(annotations map[string]string) map[string]string {
	out := selectedAnnotations(annotations, "cpu-usage", "memory-usage")
	if out == nil {
		out = map[string]string{}
	}
	for _, metric := range []struct {
		name   string
		legacy string
	}{
		{name: "disk-throughput", legacy: "disk-bandwidth"},
		{name: "network-throughput", legacy: "network-bandwidth"},
	} {
		if value := annotationValue(annotations, metric.name, metric.legacy); value != "" {
			out[metric.name] = value
		}
	}
	for name, value := range annotations {
		if strings.HasPrefix(name, "network-latency.") ||
			strings.HasPrefix(name, "network-bandwidth.") ||
			strings.HasPrefix(name, "packet-loss.") {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func selectedMetricAnnotations(annotations map[string]string) map[string]string {
	out := selectedAnnotations(annotations, "cpu-usage", "memory-usage")
	if out == nil {
		out = map[string]string{}
	}
	for _, metric := range []struct {
		name   string
		legacy string
	}{
		{name: "disk-throughput", legacy: "disk-bandwidth"},
		{name: "network-throughput", legacy: "network-bandwidth"},
	} {
		if value := annotationValue(annotations, metric.name, metric.legacy); value != "" {
			out[metric.name] = value
		}
	}
	for name, value := range annotations {
		if strings.HasPrefix(name, "rps.") || strings.HasPrefix(name, "traffic.") {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupeEdges(edges []edgeView) []edgeView {
	seen := map[string]edgeView{}
	for _, edge := range edges {
		seen[edge.Kind+"|"+edge.Source+"|"+edge.Target] = edge
	}
	out := make([]edgeView, 0, len(seen))
	for _, edge := range seen {
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source == out[j].Source {
			if out[i].Target == out[j].Target {
				return out[i].Kind < out[j].Kind
			}
			return out[i].Target < out[j].Target
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func newKubeClient() (kubernetes.Interface, string, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		kube, err := kubernetes.NewForConfig(cfg)
		return kube, envOr("CLUSTER_LENS_CONTEXT", "in-cluster"), err
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig := strings.TrimSpace(os.Getenv("KUBECONFIG")); kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	cfg, err = clientConfig.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, "", err
	}
	contextName := envOr("CLUSTER_LENS_CONTEXT", rawConfig.CurrentContext)
	if contextName == "" {
		contextName = "kubeconfig"
	}
	kube, err := kubernetes.NewForConfig(cfg)
	return kube, contextName, err
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func key(namespace, name string) string {
	return namespace + "/" + name
}

func labelOr(labels map[string]string, name, fallback string) string {
	if labels[name] != "" {
		return labels[name]
	}
	return fallback
}

func parseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(raw, 64)
	return value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func formatMS(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64) + " ms"
}

func formatBytes(value float64) string {
	const unit = 1024
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	next := value
	idx := 0
	for next >= unit && idx < len(units)-1 {
		next /= unit
		idx++
	}
	if idx == 0 {
		return strconv.FormatFloat(next, 'f', 0, 64) + " " + units[idx]
	}
	return strconv.FormatFloat(next, 'f', 1, 64) + " " + units[idx]
}
