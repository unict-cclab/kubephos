package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

type ClusterPeer struct {
	Name                     string `json:"name"`
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
}

func clusterPeerInputs(peers []ClusterPeer) []domain.ArtifactInput {
	inputs := []domain.ArtifactInput{}
	for i, peer := range peers {
		inputs = append(inputs, domain.ArtifactInput{Name: fmt.Sprintf("peer-connection-%d", i), Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: peer.ClusterConnectionRef}, domain.ArtifactInput{Name: fmt.Sprintf("peer-deployment-%d", i), Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: peer.ApplicationDeploymentRef})
	}
	return inputs
}
func resolveRuntimeLoadTargets(ctx context.Context, runner commandRunner, step domain.PlanStep, cluster clusterConnection, deployment applicationDeployment, endpoint serviceEndpoint, spec Spec) ([]loadTarget, error) {
	if spec.ClusterName == "" {
		return resolveLoadTargets(ctx, runner, cluster.Spec.Kubeconfig, deployment.Spec.Namespace, endpoint, spec)
	}
	if cluster.Spec.Networking != "cilium" || spec.ClusterName != cluster.Metadata.Name || len(spec.Peers) < 1 {
		return nil, errors.New("multi cluster load requires verified Cilium gateways")
	}
	type target struct {
		name       string
		connection clusterConnection
		deployment applicationDeployment
		endpoint   serviceEndpoint
	}
	targets := []target{{spec.ClusterName, cluster, deployment, endpoint}}
	names := map[string]bool{spec.ClusterName: true}
	for i, peer := range spec.Peers {
		connectionValue, ok := step.ResolvedInputs[fmt.Sprintf("peer-connection-%d", i)]
		deploymentValue, hasDeployment := step.ResolvedInputs[fmt.Sprintf("peer-deployment-%d", i)]
		if !ok || !hasDeployment || connectionValue.ID != peer.ClusterConnectionRef || deploymentValue.ID != peer.ApplicationDeploymentRef {
			return nil, errors.New("verified peer load artifacts unavailable")
		}
		var connection clusterConnection
		var application applicationDeployment
		if json.Unmarshal(connectionValue.Value, &connection) != nil || json.Unmarshal(deploymentValue.Value, &application) != nil || connection.Metadata.Name != peer.Name || names[peer.Name] || connection.Spec.Networking != "cilium" || application.Spec.ApplicationRef != deployment.Spec.ApplicationRef || application.Spec.ClusterServer != connection.Spec.Server || application.Spec.Namespace != deployment.Spec.Namespace {
			return nil, errors.New("peer gateway identity does not match the environment")
		}
		names[peer.Name] = true
		if err := verifyNamespace(ctx, runner, connection.Spec.Kubeconfig, application); err != nil {
			return nil, err
		}
		var selected serviceEndpoint
		for _, candidate := range application.Spec.Endpoints {
			if candidate.ID == endpoint.ID {
				selected = candidate
			}
		}
		if selected.Service == "" {
			return nil, errors.New("peer application endpoint unavailable")
		}
		targets = append(targets, target{peer.Name, connection, application, selected})
	}
	requested := geographicZones(spec.GeographicSteps)
	for name := range requested {
		if !names[name] {
			return nil, fmt.Errorf("traffic distribution references unknown cluster %s", name)
		}
	}
	result := []loadTarget{}
	for _, target := range targets {
		if len(requested) > 0 && !requested[target.name] {
			continue
		}
		if !strings.HasPrefix(target.endpoint.Service, "cilium-gateway-") {
			return nil, errors.New("multi cluster traffic must use a Cilium gateway")
		}
		raw, err := runner.Run(ctx, target.connection.Spec.Kubeconfig, nil, "get", "service", target.endpoint.Service, "-n", target.deployment.Spec.Namespace, "-o", "json")
		if err != nil {
			return nil, err
		}
		var service serviceState
		if json.Unmarshal([]byte(raw), &service) != nil {
			return nil, errors.New("Cilium gateway service is invalid")
		}
		nodePort := 0
		for _, port := range service.Spec.Ports {
			if port.Port == target.endpoint.Port {
				nodePort = port.NodePort
			}
		}
		if nodePort == 0 {
			return nil, errors.New("Cilium gateway NodePort is unavailable")
		}
		server, err := url.Parse(target.connection.Spec.Server)
		if err != nil || server.Hostname() == "" {
			return nil, errors.New("cluster ingress address is invalid")
		}
		path := target.endpoint.Path
		if path == "" {
			path = "/"
		}
		result = append(result, loadTarget{URL: strings.ToLower(target.endpoint.Protocol) + "://" + net.JoinHostPort(server.Hostname(), fmt.Sprint(nodePort)) + path, Zone: target.name, Weight: 1})
	}
	if len(result) == 0 {
		return nil, errors.New("no cluster traffic destination selected")
	}
	return result, nil
}
