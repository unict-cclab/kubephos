package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type ClusterPeer struct {
	Name                       string `json:"name"`
	ClusterConnectionRef       string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef   string `json:"applicationDeploymentRef"`
	ObservabilityCapabilityRef string `json:"observabilityCapabilityRef"`
}

func peerInputs(peers []ClusterPeer) []domain.ArtifactInput {
	result := []domain.ArtifactInput{}
	for i, peer := range peers {
		for _, input := range []struct{ name, kind, ref string }{{"cluster-connection", "ClusterConnection", peer.ClusterConnectionRef}, {"application-deployment", "ApplicationDeployment", peer.ApplicationDeploymentRef}, {"observability-capability", "ObservabilityCapability", peer.ObservabilityCapabilityRef}} {
			result = append(result, domain.ArtifactInput{Name: fmt.Sprintf("peer-%d-%s", i, input.name), Type: input.kind, Version: "v1alpha1", ArtifactID: input.ref})
		}
	}
	return result
}
func metricPeerStep(step domain.PlanStep, spec Spec, index int) (domain.PlanStep, error) {
	peer := spec.Peers[index]
	spec.Peers = nil
	spec.PeerOnly = true
	spec.ClusterName = peer.Name
	spec.ClusterConnectionRef = peer.ClusterConnectionRef
	spec.ApplicationDeploymentRef = peer.ApplicationDeploymentRef
	spec.ObservabilityCapabilityRef = peer.ObservabilityCapabilityRef
	copy := step
	copy.Input, _ = json.Marshal(spec)
	copy.ResolvedInputs = map[string]domain.ResolvedArtifact{}
	for _, key := range []string{"cluster-connection", "application-deployment", "observability-capability"} {
		value, ok := step.ResolvedInputs[fmt.Sprintf("peer-%d-%s", index, key)]
		if !ok {
			return copy, errors.New("verified peer metrics artifact unavailable")
		}
		copy.ResolvedInputs[key] = value
	}
	copy.ResolvedInputs["load-session"] = step.ResolvedInputs["load-session"]
	_, cluster, deployment, _, _, err := resolve(copy)
	if err != nil {
		return copy, err
	}
	if cluster.Metadata.Name != peer.Name || deployment.Spec.Networking != "cilium" {
		return copy, errors.New("peer metrics cluster identity is invalid")
	}
	return copy, nil
}
func (p Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	report, err := p.precheckSingle(ctx, step, log)
	if err != nil || report.Status != domain.HealthHealthy {
		return report, err
	}
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return domain.HealthReport{}, err
	}
	for i := range spec.Peers {
		peer, err := metricPeerStep(step, spec, i)
		if err != nil {
			return unhealthy(err.Error(), "peer", "invalid"), nil
		}
		report, err = p.precheckSingle(ctx, peer, log)
		if err != nil || report.Status != domain.HealthHealthy {
			return report, err
		}
	}
	return report, nil
}
func (p Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return nil, err
	}
	raw, err := p.executeSingle(ctx, step, log)
	if err != nil || len(spec.Peers) == 0 {
		return raw, err
	}
	var result result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	dataset := &result.Dataset
	labelCluster(dataset.Spec.Series, spec.ClusterName)
	summaries := [][]controlMetricSummary{dataset.Spec.ControlMetrics}
	names := []string{spec.ClusterName}
	for i, peer := range spec.Peers {
		peerStep, err := metricPeerStep(step, spec, i)
		if err != nil {
			return nil, err
		}
		raw, err := p.executeSingle(ctx, peerStep, log)
		if err != nil {
			return nil, fmt.Errorf("collect cluster %s: %w", peer.Name, err)
		}
		var value struct {
			Dataset timeSeriesDataset `json:"dataset"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		labelCluster(value.Dataset.Spec.Series, peer.Name)
		dataset.Spec.Series = append(dataset.Spec.Series, value.Dataset.Spec.Series...)
		summaries = append(summaries, value.Dataset.Spec.ControlMetrics)
		names = append(names, peer.Name)
		if value.Dataset.Spec.Start.Before(dataset.Spec.Start) {
			dataset.Spec.Start = value.Dataset.Spec.Start
		}
	}
	dataset.Spec.ControlMetrics = mergeControlSummaries(summaries, names)
	unavailable := map[string]bool{}
	for _, summary := range dataset.Spec.ControlMetrics {
		if summary.Status != "available" {
			unavailable[summary.Metric] = true
		}
	}
	series := []metricSeries{}
	ids := map[string]bool{}
	samples := 0
	for _, value := range dataset.Spec.Series {
		if unavailable[value.Metric] {
			continue
		}
		series = append(series, value)
		ids[value.Metric] = true
		samples += len(value.Points)
	}
	dataset.Spec.Series = series
	dataset.Spec.Summary = datasetSummary{Metrics: len(ids), Series: len(series), Samples: samples}
	dataset.Spec.Clusters = names
	return json.Marshal(result)
}
func labelCluster(series []metricSeries, name string) {
	for i := range series {
		if series[i].Labels == nil {
			series[i].Labels = map[string]string{}
		}
		if !strings.HasPrefix(series[i].Metric, "load.") && !strings.HasPrefix(series[i].Metric, "redis.") {
			series[i].Labels["cluster"] = name
		}
	}
}
func mergeControlSummaries(groups [][]controlMetricSummary, names []string) []controlMetricSummary {
	byMetric := map[string][]controlMetricSummary{}
	order := []string{}
	for _, group := range groups {
		for _, summary := range group {
			if _, ok := byMetric[summary.Metric]; !ok {
				order = append(order, summary.Metric)
			}
			byMetric[summary.Metric] = append(byMetric[summary.Metric], summary)
		}
	}
	result := []controlMetricSummary{}
	for _, metric := range order {
		summaries := byMetric[metric]
		merged := summaries[0]
		merged.Scope = "experiment"
		merged.Source = "multi-cluster"
		count, total, weighted := 0.0, 0.0, 0.0
		available := false
		missing := false
		for _, summary := range summaries {
			if summary.Status == "unavailable" {
				missing = true
			}
			if summary.Status != "available" {
				continue
			}
			available = true
			if summary.Count != nil {
				count += *summary.Count
				if summary.Mean != nil {
					weighted += *summary.Count * *summary.Mean
				}
			}
			if summary.Total != nil {
				total += *summary.Total
			}
		}
		if missing || len(summaries) != len(groups) {
			merged.Status = "unavailable"
			merged.Count = nil
			merged.Mean = nil
			merged.Total = nil
			merged.Reason = "Measurements are unavailable in one or more clusters."
		} else if available {
			merged.Status = "available"
			merged.Reason = ""
			if metric == "descheduler.evictions" {
				merged.Total = &total
				merged.Count = nil
				merged.Mean = nil
			} else {
				mean := weighted / count
				merged.Count = &count
				merged.Mean = &mean
				merged.Total = nil
			}
		}
		result = append(result, merged)
	}
	return result
}
func ciliumMetricDefinitions(deployment applicationDeployment, peerOnly bool) []metricDefinition {
	var base []metricDefinition
	if nativeTCPDeployment(deployment) {
		base = nativeMetricDefinitions(deployment.Spec.Namespace)
	} else {
		base = metricDefinitions(deployment.Spec.Namespace, "")
	}
	result := []metricDefinition{}
	for _, definition := range base {
		if strings.HasPrefix(definition.ID, "application.") || peerOnly && (strings.HasPrefix(definition.ID, "load.") || strings.HasPrefix(definition.ID, "redis.")) {
			continue
		}

		selector := "{measurement_cluster=" + strconv.Quote(deployment.Spec.ClusterName) + "}"
		switch definition.ID {
		case "network.rtt":
			definition.Query = `1000 * sum by (origin_cluster,destination_cluster) (rate(cluster_network_latency_sum` + selector + `[1m])) / sum by (origin_cluster,destination_cluster) (rate(cluster_network_latency_count` + selector + `[1m]))`
		case "network.packet_loss":
			definition.Query = `100 * avg by (origin_cluster,destination_cluster) (avg_over_time(cluster_network_packet_loss_ratio` + selector + `[1m]))`
		case "network.bandwidth":
			definition.Query = `avg by (origin_cluster,destination_cluster) (cluster_network_bandwidth_bytes_per_second` + selector + `)`
		}

		result = append(result, definition)
	}
	return result
}
