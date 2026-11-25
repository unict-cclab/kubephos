package metricscollector

import (
	"context"
	"math"
	"strconv"
	"time"
)

// podWorkload is the owning workload of a pod, resolved from kube-state-metrics.
type podWorkload struct {
	Workload, Kind, Node, Zone string
}

// Pod-scoped metrics labelled with their owning workload. Grouping by pod
// name prefixes misattributes pods (StatefulSet "cart-db-0" is not part of
// Deployment "cart") and loses DaemonSet pods, so ownership is read from
// kube_pod_owner and kube_replicaset_owner and joined here, in Go.
var podScopedMetrics = map[string]bool{"cpu.cores": true, "cpu.limit_cores": true, "cpu.request_cores": true, "memory.working_set": true, "pod.restarts": true}

// readyReplicaKinds maps the grouping label of ready-replica metrics to its kind.
var readyReplicaKinds = []struct{ label, kind string }{{"deployment", "Deployment"}, {"statefulset", "StatefulSet"}, {"daemonset", "DaemonSet"}}

// resolvePodWorkloads reads pod ownership and placement over the whole window
// (plus the rate lookback) with instant max_over_time queries. Best effort:
// a failed query leaves the affected labels unresolved.
func (plugin Plugin) resolvePodWorkloads(ctx context.Context, cluster clusterConnection, capability observabilityCapability, endpoint serviceEndpoint, namespace string, start, end time.Time, lookback time.Duration, zones map[string]string, log func(string)) map[string]podWorkload {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	selector := `{namespace=` + strconv.Quote(namespace)
	window := `[` + strconv.FormatInt(int64(math.Ceil((end.Sub(start)+lookback).Seconds())), 10) + `s]`
	owners, err := plugin.instantQuery(ctx, cluster, capability, endpoint, `max by (pod, owner_kind, owner_name, owner_is_controller) (max_over_time(kube_pod_owner`+selector+`}`+window+`))`, end)
	if err != nil {
		log("Pod owners unavailable: " + err.Error())
		return nil
	}
	replicaSets, err := plugin.instantQuery(ctx, cluster, capability, endpoint, `max by (replicaset, owner_kind, owner_name, owner_is_controller) (max_over_time(kube_replicaset_owner`+selector+`}`+window+`))`, end)
	if err != nil {
		log("ReplicaSet owners unavailable: " + err.Error())
	}
	nodes, err := plugin.instantQuery(ctx, cluster, capability, endpoint, `max by (pod, node) (max_over_time(kube_pod_info`+selector+`,node!=""}`+window+`))`, end)
	if err != nil {
		log("Pod nodes unavailable: " + err.Error())
	}
	return podWorkloads(owners, replicaSets, nodes, zones)
}

// ownerOf returns the single controller owner of each object. Objects with
// several distinct controllers in the window (a reused name) stay unresolved;
// "<none>" marks an object without owner.
func ownerOf(samples []vectorSample, nameLabel string) map[string][2]string {
	candidates := map[string]map[[2]string]bool{}
	for _, sample := range samples {
		name := sample.Labels[nameLabel]
		if name == "" || sample.Value <= 0 {
			continue
		}
		controller := sample.Labels["owner_is_controller"]
		kind, owner := sample.Labels["owner_kind"], sample.Labels["owner_name"]
		if controller != "true" && kind != "<none>" {
			continue
		}
		if candidates[name] == nil {
			candidates[name] = map[[2]string]bool{}
		}
		candidates[name][[2]string{kind, owner}] = true
	}
	result := map[string][2]string{}
	for name, owners := range candidates {
		if len(owners) > 1 {
			// A real controller wins over a "<none>" sample of the same name.
			delete(owners, [2]string{"<none>", "<none>"})
		}
		if len(owners) != 1 {
			continue
		}
		for owner := range owners {
			result[name] = owner
		}
	}
	return result
}

func podWorkloads(owners, replicaSets, nodes []vectorSample, zones map[string]string) map[string]podWorkload {
	podOwners := ownerOf(owners, "pod")
	replicaSetOwners := ownerOf(replicaSets, "replicaset")
	result := map[string]podWorkload{}
	for pod, owner := range podOwners {
		kind, name := owner[0], owner[1]
		switch kind {
		case "<none>":
			kind, name = "Pod", pod
		case "ReplicaSet":
			parent, ok := replicaSetOwners[name]
			if !ok {
				// The ReplicaSet's own owner is unknown: leave unresolved
				// rather than guess from its name.
				continue
			}
			if parent[0] != "<none>" {
				kind, name = parent[0], parent[1]
			}
		}
		if kind == "" || name == "" {
			continue
		}
		result[pod] = podWorkload{Workload: name, Kind: kind}
	}
	podNodes := map[string]map[string]bool{}
	for _, sample := range nodes {
		pod, node := sample.Labels["pod"], sample.Labels["node"]
		if pod == "" || node == "" || sample.Value <= 0 {
			continue
		}
		if podNodes[pod] == nil {
			podNodes[pod] = map[string]bool{}
		}
		podNodes[pod][node] = true
	}
	for pod, candidates := range podNodes {
		// A pod name that ran on several nodes in the window (a recreated
		// StatefulSet pod) has no single node.
		if len(candidates) != 1 {
			continue
		}
		for node := range candidates {
			value := result[pod]
			value.Node = node
			value.Zone = zones[node]
			result[pod] = value
		}
	}
	return result
}

// labelWorkloads adds workload, workload_kind, node and zone labels to
// pod-scoped series and workload/workload_kind to ready-replica series.
func labelWorkloads(series []metricSeries, pods map[string]podWorkload) {
	for i := range series {
		labels := series[i].Labels
		if labels == nil {
			continue
		}
		if podScopedMetrics[series[i].Metric] {
			owner, ok := pods[labels["pod"]]
			if !ok || labels["pod"] == "" {
				continue
			}
			for key, value := range map[string]string{"workload": owner.Workload, "workload_kind": owner.Kind, "node": owner.Node, "zone": owner.Zone} {
				if value != "" {
					labels[key] = value
				}
			}
			continue
		}
		switch series[i].Metric {
		case "deployment.ready_replicas", "statefulset.ready_replicas", "daemonset.ready_replicas":
			for _, candidate := range readyReplicaKinds {
				if name := labels[candidate.label]; name != "" {
					labels["workload"], labels["workload_kind"] = name, candidate.kind
					break
				}
			}
		}
	}
}

func nodeZones(nodes []runContextNode) map[string]string {
	zones := map[string]string{}
	for _, node := range nodes {
		if node.Zone != "" {
			zones[node.Name] = node.Zone
		}
	}
	return zones
}
