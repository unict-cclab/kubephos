package metricscollector

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

type placementNode struct {
	Name    string `json:"name"`
	Zone    string `json:"zone,omitempty"`
	Cluster string `json:"cluster"`
}
type placementPod struct {
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Service string `json:"service"`
	Node    string `json:"node"`
	Cluster string `json:"cluster"`
}
type placementSnapshot struct {
	Observed  bool           `json:"observed"`
	Timestamp time.Time      `json:"timestamp"`
	Pods      []placementPod `json:"pods"`
}
type placementHistory struct {
	Status      string              `json:"status"`
	Source      string              `json:"source"`
	StepSeconds int                 `json:"stepSeconds"`
	Nodes       []placementNode     `json:"nodes"`
	Snapshots   []placementSnapshot `json:"snapshots"`
}

// Placement is optional evidence. Collection failure must never discard a run's metrics.
func (p Plugin) collectPlacement(ctx context.Context, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability, endpoint serviceEndpoint, start, end time.Time, resolution time.Duration) *placementHistory {
	if deployment.Spec.External {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	step := resolution
	if minimum := time.Duration((int64(end.Sub(start).Seconds())+1439)/1440) * time.Second; step < minimum {
		step = minimum
	}
	if step < time.Second {
		step = time.Second
	}
	h := &placementHistory{Status: "unavailable", Source: "kube-state-metrics", StepSeconds: int(step.Seconds()), Nodes: []placementNode{}, Snapshots: []placementSnapshot{}}
	read := func(query string) ([]metricSeries, error) {
		raw, err := p.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+rangeQueryPath(capability, endpoint, query, start, end, step))
		if err != nil {
			return nil, err
		}
		return decodeMatrix(raw, metricDefinition{ID: "placement", Unit: "count", Aggregation: "sum"}, start, end)
	}
	ns := strconv.Quote(deployment.Spec.Namespace)
	query := `max by (uid,pod,node,created_by_kind,created_by_name) (kube_pod_info{namespace=` + ns + `,node!=""})`
	if created := deployment.Spec.NamespaceCreatedAt; created != nil && !created.IsZero() {
		query += ` and on(uid) (max by(uid) (kube_pod_created{namespace=` + ns + `} >= ` + strconv.FormatInt(created.Unix(), 10) + `))`
	}
	query += ` and on(uid) (max by(uid) (kube_pod_status_phase{namespace=` + ns + `,phase=~"Pending|Running|Unknown"} == 1))`
	pods, err := read(query)
	if err != nil {
		return h
	}
	owners, err := read(`max by (replicaset,owner_name) (kube_replicaset_owner{namespace=` + ns + `,owner_kind="Deployment",owner_is_controller="true"})`)
	if err != nil {
		return h
	}
	name := deployment.Spec.ClusterName
	if name == "" {
		name = cluster.Metadata.Name
	}
	if name == "" {
		name = "Cluster"
	}
	*h = buildPlacement(pods, owners, deployment, start, end, step, name)
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if raw, err := p.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "nodes", "-o", "json"); err == nil && json.Unmarshal([]byte(raw), &nodes) == nil {
		zones := map[string]string{}
		known := map[string]bool{}
		for _, n := range h.Nodes {
			known[n.Name] = true
		}
		for _, n := range nodes.Items {
			zones[n.Metadata.Name] = n.Metadata.Labels["topology.kubernetes.io/zone"]
			if n.Metadata.Labels["kubephos.dev/role"] == "application" && !known[n.Metadata.Name] {
				h.Nodes = append(h.Nodes, placementNode{Name: n.Metadata.Name, Cluster: name})
				known[n.Metadata.Name] = true
			}
		}
		for i := range h.Nodes {
			h.Nodes[i].Zone = zones[h.Nodes[i].Name]
		}
	}
	sort.Slice(h.Nodes, func(i, j int) bool { return h.Nodes[i].Name < h.Nodes[j].Name })
	return h
}

func buildPlacement(pods, owners []metricSeries, deployment applicationDeployment, start, end time.Time, step time.Duration, cluster string) placementHistory {
	h := placementHistory{Status: "available", Source: "kube-state-metrics", StepSeconds: int(step.Seconds()), Nodes: []placementNode{}, Snapshots: []placementSnapshot{}}
	allowed := map[string]bool{}
	for _, w := range deployment.Spec.Workloads {
		for _, trait := range w.Traits {
			if trait == "schedulable" {
				allowed[w.Name] = true
			}
		}
	}
	// Ownership is resolved at each sample, never inferred from pod name prefixes.
	ownerAt := map[string]string{}
	for _, s := range owners {
		for _, p := range s.Points {
			if p.Value > 0 {
				ownerAt[s.Labels["replicaset"]+"\x00"+p.Timestamp.Format(time.RFC3339Nano)] = s.Labels["owner_name"]
			}
		}
	}
	frames := map[int64]map[string]placementPod{}
	nodes := map[string]bool{}
	samples := 0
	for _, s := range pods {
		if s.Labels["uid"] == "" || s.Labels["pod"] == "" || s.Labels["node"] == "" {
			continue
		}
		for _, p := range s.Points {
			if p.Value <= 0 || p.Timestamp.Before(start) || p.Timestamp.After(end) {
				continue
			}
			service := ""
			switch s.Labels["created_by_kind"] {
			case "ReplicaSet":
				service = ownerAt[s.Labels["created_by_name"]+"\x00"+p.Timestamp.Format(time.RFC3339Nano)]
			case "StatefulSet", "Deployment":
				service = s.Labels["created_by_name"]
			}
			if !allowed[service] {
				continue
			}
			key := p.Timestamp.UnixNano()
			if frames[key] == nil {
				frames[key] = map[string]placementPod{}
			}
			frames[key][s.Labels["uid"]] = placementPod{UID: s.Labels["uid"], Name: s.Labels["pod"], Service: service, Node: s.Labels["node"], Cluster: cluster}
			nodes[s.Labels["node"]] = true
			samples++
			if samples > 500000 {
				return placementHistory{Status: "unavailable", Source: h.Source, StepSeconds: h.StepSeconds, Nodes: []placementNode{}, Snapshots: []placementSnapshot{}}
			}
		}
	}
	if len(frames) == 0 {
		h.Status = "unavailable"
		return h
	}
	for t := start; !t.After(end); t = t.Add(step) {
		frame := placementSnapshot{Timestamp: t, Pods: []placementPod{}, Observed: len(frames[t.UnixNano()]) > 0}
		for _, pod := range frames[t.UnixNano()] {
			frame.Pods = append(frame.Pods, pod)
		}
		sort.Slice(frame.Pods, func(i, j int) bool { return frame.Pods[i].UID < frame.Pods[j].UID })
		h.Snapshots = append(h.Snapshots, frame)
	}
	for node := range nodes {
		h.Nodes = append(h.Nodes, placementNode{Name: node, Cluster: cluster})
	}
	sort.Slice(h.Nodes, func(i, j int) bool { return h.Nodes[i].Name < h.Nodes[j].Name })
	return h
}

func mergePlacement(a, b *placementHistory) *placementHistory {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	// Different time grids cannot be presented as a synchronized replay.
	if a.Status != "available" || b.Status != "available" || a.StepSeconds != b.StepSeconds || len(a.Snapshots) != len(b.Snapshots) {
		return &placementHistory{Status: "unavailable", Source: "kube-state-metrics"}
	}
	for i := range a.Snapshots {
		if !a.Snapshots[i].Timestamp.Equal(b.Snapshots[i].Timestamp) {
			return &placementHistory{Status: "unavailable", Source: "kube-state-metrics"}
		}
	}
	a.Nodes = append(a.Nodes, b.Nodes...)
	for i := range a.Snapshots {
		a.Snapshots[i].Observed = a.Snapshots[i].Observed && b.Snapshots[i].Observed
		a.Snapshots[i].Pods = append(a.Snapshots[i].Pods, b.Snapshots[i].Pods...)
	}
	return a
}
