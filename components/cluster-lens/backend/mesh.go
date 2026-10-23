package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func meshClusterNames(raw string) []string {
	var clusters []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(raw), &clusters) != nil {
		return nil
	}
	names := []string{}
	for _, cluster := range clusters {
		if cluster.Name != "" {
			names = append(names, cluster.Name)
		}
	}
	sort.Strings(names)
	return names
}
func (s *server) applyMeshSnapshot(ctx context.Context, snap *snapshot) error {
	if len(s.meshClusters) < 2 {
		return nil
	}
	snap.Mode = "multi"
	snap.Nodes = nil
	snap.NodeEdges = nil
	for _, name := range s.meshClusters {
		snap.Nodes = append(snap.Nodes, nodeView{Name: name, Zone: name, Role: "cluster", Ready: name == s.clusterName, Labels: map[string]string{"kubephos.dev/cluster": name}})
	}
	for i := range snap.Pods {
		snap.Pods[i].Node = s.clusterName
	}
	for i := range snap.GatewayTraffic {
		snap.GatewayTraffic[i].Node = s.clusterName
		snap.GatewayTraffic[i].Zone = s.clusterName
	}
	wrap := func(query string) string {
		return `label_join(` + query + `,"link",">","origin_cluster","destination_cluster")`
	}
	latency, err := s.readPrometheusValues(ctx, wrap(`1000 * sum by (origin_cluster,destination_cluster) (rate(cluster_network_latency_sum[1m])) / sum by (origin_cluster,destination_cluster) (rate(cluster_network_latency_count[1m]))`), "link")
	if err != nil {
		return err
	}
	bandwidth, err := s.readPrometheusValues(ctx, wrap(`avg by (origin_cluster,destination_cluster) (cluster_network_bandwidth_bytes_per_second)`), "link")
	if err != nil {
		return err
	}
	loss, err := s.readPrometheusValues(ctx, wrap(`100 * avg by (origin_cluster,destination_cluster) (cluster_network_packet_loss_ratio)`), "link")
	if err != nil {
		return err
	}
	nodes := []nodeView{}
	for _, name := range s.meshClusters {
		ready := name == s.clusterName
		for link := range latency {
			parts := strings.SplitN(link, ">", 2)
			if len(parts) == 2 && (parts[0] == name || parts[1] == name) {
				ready = true
			}
		}
		nodes = append(nodes, nodeView{Name: name, Zone: name, Role: "cluster", Ready: ready, Labels: map[string]string{"kubephos.dev/cluster": name}})
	}
	edges := []edgeView{}
	for link, value := range latency {
		parts := strings.SplitN(link, ">", 2)
		if len(parts) != 2 || parts[0] == parts[1] {
			continue
		}
		edge := edgeView{Source: parts[0], Target: parts[1], Kind: "network", Value: value, Label: formatMS(value)}
		if measured, ok := bandwidth[link]; ok {
			edge.Bandwidth = floatPointer(measured)
		}
		if measured, ok := loss[link]; ok {
			edge.PacketLoss = floatPointer(measured)
		}
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].Source+">"+edges[i].Target < edges[j].Source+">"+edges[j].Target })
	snap.Nodes = nodes
	snap.NodeEdges = edges
	for i := range snap.Pods {
		snap.Pods[i].Node = s.clusterName
	}
	for i := range snap.GatewayTraffic {
		snap.GatewayTraffic[i].Node = s.clusterName
		snap.GatewayTraffic[i].Zone = s.clusterName
	}
	if len(edges) == 0 {
		return fmt.Errorf("inter-cluster network probes have no current measurements")
	}
	return nil
}
