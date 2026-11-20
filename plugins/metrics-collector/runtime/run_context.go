package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// runContext records the conditions a run was measured under (spec.runContext).
// Every probe is best effort: a failure leaves its field absent and never
// fails the collection.
type runContext struct {
	LoadSession *runContextLoadSession `json:"loadSession,omitempty"`
	Prometheus  *runContextPrometheus  `json:"prometheus,omitempty"`
	Kubernetes  *runContextKubernetes  `json:"kubernetes,omitempty"`
	Istio       *runContextIstio       `json:"istio,omitempty"`
	Nodes       []runContextNode       `json:"nodes,omitempty"`
	// Other KubePhos load generators active during the window; an empty list
	// means none was seen, absence means the probe failed.
	ConcurrentLoad *[]concurrentLoad `json:"concurrentLoad,omitempty"`
}

type runContextLoadSession struct {
	WarmupSeconds   *int       `json:"warmupSeconds,omitempty"`
	WarmupStartedAt *time.Time `json:"warmupStartedAt,omitempty"`
	DrainSeconds    *int       `json:"drainSeconds,omitempty"`
	Clock           string     `json:"clock,omitempty"`
	Workers         int        `json:"workers,omitempty"`
}

type runContextPrometheus struct {
	Version               string `json:"version,omitempty"`
	ScrapeIntervalSeconds int    `json:"scrapeIntervalSeconds,omitempty"`
}

type runContextKubernetes struct {
	ServerVersion string `json:"serverVersion"`
}

type runContextIstio struct {
	Version string `json:"version"`
}

type runContextNode struct {
	Name             string `json:"name"`
	Cluster          string `json:"cluster,omitempty"`
	Zone             string `json:"zone,omitempty"`
	Region           string `json:"region,omitempty"`
	InstanceType     string `json:"instanceType,omitempty"`
	Kubelet          string `json:"kubelet,omitempty"`
	ContainerRuntime string `json:"containerRuntime,omitempty"`
	Kernel           string `json:"kernel,omitempty"`
}

type concurrentLoad struct {
	Namespace string `json:"namespace"`
	MaxPods   int    `json:"maxPods"`
}

type vectorSample struct {
	Labels map[string]string
	Value  float64
}

// locustSummary returns the LoadSession summary verbatim, or nil when absent.
func locustSummary(load loadSession) json.RawMessage {
	raw := strings.TrimSpace(string(load.Spec.LocustSummary))
	if raw == "" || raw == "null" {
		return nil
	}
	return load.Spec.LocustSummary
}

func loadSessionContext(load loadSession) *runContextLoadSession {
	value := runContextLoadSession{WarmupSeconds: load.Spec.WarmupSeconds, WarmupStartedAt: load.Spec.WarmupStartedAt, DrainSeconds: load.Spec.DrainSeconds, Clock: load.Spec.Clock, Workers: load.Spec.Workers}
	if value.Workers == 0 {
		value.Workers = load.Spec.Replicas
	}
	var summary struct {
		Workers int `json:"workers"`
	}
	if raw := locustSummary(load); raw != nil && json.Unmarshal(raw, &summary) == nil && summary.Workers > 0 {
		value.Workers = summary.Workers
	}
	if value == (runContextLoadSession{}) {
		return nil
	}
	return &value
}

// collectRunContext runs the read-only probes: Prometheus build info, the
// Kubernetes /version endpoint, istio_build, the node inventory and other
// load generators. It also returns node zones for workload labels.
func (plugin Plugin) collectRunContext(ctx context.Context, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability, endpoint serviceEndpoint, load loadSession, start, end time.Time, resolution time.Duration, scrapeInterval time.Duration, peerOnly bool, log func(string)) *runContext {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	value := &runContext{}
	if !peerOnly {
		value.LoadSession = loadSessionContext(load)
	}
	prometheus := runContextPrometheus{ScrapeIntervalSeconds: int(math.Ceil(scrapeInterval.Seconds()))}
	if raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+proxyBase(capability, endpoint)+"/api/v1/status/buildinfo"); err != nil {
		log("Prometheus build info unavailable: " + err.Error())
	} else {
		var response struct {
			Status string `json:"status"`
			Data   struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &response) == nil && response.Status == "success" {
			prometheus.Version = response.Data.Version
		}
	}
	if prometheus != (runContextPrometheus{}) {
		value.Prometheus = &prometheus
	}
	if raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw=/version"); err != nil {
		log("Kubernetes server version unavailable: " + err.Error())
	} else {
		var version struct {
			GitVersion string `json:"gitVersion"`
		}
		if json.Unmarshal([]byte(raw), &version) == nil && version.GitVersion != "" {
			value.Kubernetes = &runContextKubernetes{ServerVersion: version.GitVersion}
		}
	}
	lookback := strconv.FormatInt(int64(math.Max(end.Sub(start).Seconds(), resolution.Seconds())), 10) + "s"
	if samples, err := plugin.instantQuery(ctx, cluster, capability, endpoint, `count by (component, tag) (last_over_time(istio_build[`+lookback+`]))`, end); err != nil {
		log("Istio version unavailable: " + err.Error())
	} else if version := istioVersion(samples); version != "" {
		value.Istio = &runContextIstio{Version: version}
	}
	if raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "nodes", "-o", "json"); err != nil {
		log("Node inventory unavailable: " + err.Error())
	} else if nodes, err := decodeNodes(raw); err != nil {
		log("Node inventory unavailable: " + err.Error())
	} else {
		value.Nodes = nodes
	}
	query := `max by (namespace) (max_over_time((count by (namespace) (kube_pod_info{pod=~"kubephos-load-.*",namespace!=` + strconv.Quote(deployment.Spec.Namespace) + `}))[` + lookback + `:` + formatPromDuration(resolution) + `]))`
	if samples, err := plugin.instantQuery(ctx, cluster, capability, endpoint, query, end); err != nil {
		log("Concurrent load detection unavailable: " + err.Error())
	} else {
		others := []concurrentLoad{}
		for _, sample := range samples {
			if sample.Labels["namespace"] != "" && sample.Value > 0 {
				others = append(others, concurrentLoad{Namespace: sample.Labels["namespace"], MaxPods: int(math.Round(sample.Value))})
			}
		}
		sort.Slice(others, func(i, j int) bool { return others[i].Namespace < others[j].Namespace })
		value.ConcurrentLoad = &others
	}
	return value
}

// istioVersion prefers the control plane (pilot) tag, then the most common proxy tag.
func istioVersion(samples []vectorSample) string {
	best, bestCount, bestPilot := "", 0.0, false
	for _, sample := range samples {
		tag := sample.Labels["tag"]
		if tag == "" || tag == "unknown" {
			continue
		}
		pilot := sample.Labels["component"] == "pilot"
		if pilot && !bestPilot || pilot == bestPilot && (sample.Value > bestCount || sample.Value == bestCount && tag < best) {
			best, bestCount, bestPilot = tag, sample.Value, pilot
		}
	}
	return best
}

func decodeNodes(raw string) ([]runContextNode, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				NodeInfo struct {
					KubeletVersion          string `json:"kubeletVersion"`
					ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
					KernelVersion           string `json:"kernelVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	label := func(labels map[string]string, keys ...string) string {
		for _, key := range keys {
			if labels[key] != "" {
				return labels[key]
			}
		}
		return ""
	}
	nodes := make([]runContextNode, 0, len(list.Items))
	for _, item := range list.Items {
		labels := item.Metadata.Labels
		nodes = append(nodes, runContextNode{Name: item.Metadata.Name,
			Zone:             label(labels, "topology.kubernetes.io/zone", "failure-domain.beta.kubernetes.io/zone"),
			Region:           label(labels, "topology.kubernetes.io/region", "failure-domain.beta.kubernetes.io/region"),
			InstanceType:     label(labels, "node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"),
			Kubelet:          item.Status.NodeInfo.KubeletVersion,
			ContainerRuntime: item.Status.NodeInfo.ContainerRuntimeVersion,
			Kernel:           item.Status.NodeInfo.KernelVersion})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes, nil
}

// mergeRunContexts keeps the primary cluster's versions and load session and
// adds every cluster's nodes and concurrent load generators.
func mergeRunContexts(contexts []*runContext, names []string) *runContext {
	if len(contexts) == 0 || contexts[0] == nil {
		return nil
	}
	merged := *contexts[0]
	merged.Nodes = nil
	var others *[]concurrentLoad
	maximum := map[string]int{}
	for index, value := range contexts {
		if value == nil {
			continue
		}
		for _, node := range value.Nodes {
			node.Cluster = names[index]
			merged.Nodes = append(merged.Nodes, node)
		}
		if value.ConcurrentLoad != nil {
			if others == nil {
				others = &[]concurrentLoad{}
			}
			for _, item := range *value.ConcurrentLoad {
				if item.MaxPods > maximum[item.Namespace] {
					maximum[item.Namespace] = item.MaxPods
				}
			}
		}
	}
	if others != nil {
		for namespace, pods := range maximum {
			*others = append(*others, concurrentLoad{Namespace: namespace, MaxPods: pods})
		}
		sort.Slice(*others, func(i, j int) bool { return (*others)[i].Namespace < (*others)[j].Namespace })
	}
	merged.ConcurrentLoad = others
	return &merged
}

func instantQueryPath(capability observabilityCapability, endpoint serviceEndpoint, query string, at time.Time) string {
	values := url.Values{}
	values.Set("query", query)
	values.Set("time", strconv.FormatInt(at.Unix(), 10))
	return proxyBase(capability, endpoint) + "/api/v1/query?" + values.Encode()
}

func (plugin Plugin) instantQuery(ctx context.Context, cluster clusterConnection, capability observabilityCapability, endpoint serviceEndpoint, query string, at time.Time) ([]vectorSample, error) {
	raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+instantQueryPath(capability, endpoint, query, at))
	if err != nil {
		return nil, err
	}
	return decodeVector(raw)
}

func decodeVector(raw string) ([]vectorSample, error) {
	var response struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, err
	}
	if response.Status != "success" {
		return nil, errors.New("metrics API " + response.ErrorType + ": " + response.Error)
	}
	if response.Data.ResultType != "vector" {
		return nil, errors.New("metrics API returned " + response.Data.ResultType + " instead of vector")
	}
	samples := make([]vectorSample, 0, len(response.Data.Result))
	for _, item := range response.Data.Result {
		if len(item.Value) != 2 {
			return nil, errors.New("metrics API returned a malformed sample")
		}
		var encoded string
		if err := json.Unmarshal(item.Value[1], &encoded); err != nil {
			return nil, errors.New("metrics API returned an invalid value")
		}
		value, err := strconv.ParseFloat(encoded, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		samples = append(samples, vectorSample{Labels: item.Metric, Value: value})
	}
	return samples, nil
}
