package loadsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

type clusterTargetRunner struct{}

func (clusterTargetRunner) Run(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	if len(args) > 1 && args[1] == "namespace" {
		return "marker", nil
	}
	return `{"spec":{"ports":[{"port":80,"nodePort":31234}]}}`, nil
}
func TestMultiClusterTrafficTargetsVerifiedCiliumGateways(t *testing.T) {
	var cluster clusterConnection
	cluster.Metadata.Name = "cluster-a"
	cluster.Spec.Networking = "cilium"
	cluster.Spec.Server = "https://10.0.0.10:6443"
	cluster.Spec.Kubeconfig = "config-a"
	var deployment applicationDeployment
	deployment.Spec.Namespace = "experiment"
	deployment.Spec.ApplicationRef = "app:test"
	deployment.Metadata.OwnershipMarker = "marker"
	endpoint := serviceEndpoint{ID: "http", Service: "cilium-gateway-entry-http", Protocol: "http", Port: 80}
	deployment.Spec.Endpoints = []serviceEndpoint{endpoint}
	peerCluster := cluster
	peerCluster.Metadata.Name = "cluster-b"
	peerCluster.Spec.Server = "https://10.0.0.20:6443"
	peerCluster.Spec.Kubeconfig = "config-b"
	peerDeployment := deployment
	peerDeployment.Spec.ClusterServer = peerCluster.Spec.Server
	connectionRaw, _ := json.Marshal(peerCluster)
	deploymentRaw, _ := json.Marshal(peerDeployment)
	step := domain.PlanStep{ResolvedInputs: map[string]domain.ResolvedArtifact{"peer-connection-0": {ID: "art_peer_connection", Type: "ClusterConnection", Value: connectionRaw}, "peer-deployment-0": {ID: "art_peer_deployment", Type: "ApplicationDeployment", Value: deploymentRaw}}}
	spec := Spec{ClusterName: "cluster-a", Peers: []ClusterPeer{{Name: "cluster-b", ClusterConnectionRef: "art_peer_connection", ApplicationDeploymentRef: "art_peer_deployment"}}, GeographicSteps: []GeographicStep{{Type: "constant", DurationSeconds: 60, Weights: map[string]float64{"cluster-a": 3, "cluster-b": 1}}}}
	targets, err := resolveRuntimeLoadTargets(context.Background(), clusterTargetRunner{}, step, cluster, deployment, endpoint, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Zone != "cluster-a" || targets[1].Zone != "cluster-b" || !strings.Contains(targets[0].URL, "10.0.0.10:31234") || !strings.Contains(targets[1].URL, "10.0.0.20:31234") {
		t.Fatalf("incorrect targets: %+v", targets)
	}
	spec.GeographicSteps[0].Weights["unknown-cluster"] = 1
	if _, err := resolveRuntimeLoadTargets(context.Background(), clusterTargetRunner{}, step, cluster, deployment, endpoint, spec); err == nil {
		t.Fatal("accepted an unknown cluster in traffic weights")
	}
}
