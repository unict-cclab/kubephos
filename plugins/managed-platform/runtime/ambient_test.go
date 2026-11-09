package managedplatform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

// ambientCluster records the commands the ambient plugin sends to a fake cluster.
type ambientCluster struct {
	ambient    bool
	gatewayAPI bool
	kubectl    []string
	helm       []string
	values     map[string]string
}

func (cluster *ambientCluster) Kubectl(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	cluster.kubectl = append(cluster.kubectl, command)
	switch {
	case strings.HasPrefix(command, "get deployment istiod") && strings.Contains(command, "PILOT_ENABLE_AMBIENT"):
		if cluster.ambient {
			return "true", nil
		}
		return "", nil
	case strings.HasPrefix(command, "get daemonset"):
		if cluster.ambient {
			return "", nil
		}
		return "", errors.New("not found")
	case command == "get customresourcedefinition "+gatewayAPICRD:
		if cluster.gatewayAPI {
			return "", nil
		}
		return "", errors.New("not found")
	}
	return "", nil
}

func (cluster *ambientCluster) Helm(_ context.Context, _ string, _, values []byte, args ...string) (string, error) {
	cluster.helm = append(cluster.helm, args[2])
	cluster.values[args[2]] = string(values)
	return "", nil
}

type fakeAmbientCharts struct{}

func (fakeAmbientCharts) Fetch(_ context.Context, chart chartSpec) ([]byte, error) {
	return []byte(chart.ID), nil
}

func ambientStep() domain.PlanStep {
	return domain.PlanStep{
		Input: json.RawMessage(`{"clusterConnectionRef":"art_cluster"}`),
		ResolvedInputs: map[string]domain.ResolvedArtifact{
			"cluster-connection": {Value: json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ClusterConnection","metadata":{"name":"cluster","version":"1"},"spec":{"distribution":"k3s","server":"https://cluster.example.test","kubeconfig":"config"}}`)},
		},
	}
}

func silent(string, string) error { return nil }

func TestAmbientInstallsTheLayerOnce(t *testing.T) {
	cluster := &ambientCluster{values: map[string]string{}}
	plugin := AmbientPlugin{Runner: cluster, Fetcher: fakeAmbientCharts{}}
	if _, err := plugin.Execute(context.Background(), ambientStep(), silent); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cluster.helm, ",") != istiodRelease+","+istioCNIRelease+","+ztunnelRelease {
		t.Fatalf("unexpected release order: %v", cluster.helm)
	}
	if values := cluster.values[istiodRelease]; !strings.Contains(values, "profile: ambient") || !strings.Contains(values, "kubephos.dev/role: management") {
		t.Fatalf("istiod lost its managed settings or the ambient profile: %s", values)
	}
	if values := cluster.values[istioCNIRelease]; !strings.Contains(values, "platform: k3s") || !strings.Contains(values, "variant: \"\"") {
		t.Fatalf("istio-cni does not use the k3s paths and mirrored images: %s", values)
	}
	if !strings.Contains(strings.Join(cluster.kubectl, "\n"), "apply --server-side -f -") {
		t.Fatal("Gateway API CRDs were not installed on a cluster without them")
	}

	installed := &ambientCluster{ambient: true, gatewayAPI: true, values: map[string]string{}}
	if _, err := (AmbientPlugin{Runner: installed, Fetcher: fakeAmbientCharts{}}).Execute(context.Background(), ambientStep(), silent); err != nil {
		t.Fatal(err)
	}
	if len(installed.helm) != 0 {
		t.Fatalf("an enabled cluster was reinstalled: %v", installed.helm)
	}
}

func TestAmbientKeepsExistingGatewayAPI(t *testing.T) {
	cluster := &ambientCluster{gatewayAPI: true, values: map[string]string{}}
	if _, err := (AmbientPlugin{Runner: cluster, Fetcher: fakeAmbientCharts{}}).Execute(context.Background(), ambientStep(), silent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cluster.kubectl, "\n"), "apply --server-side") {
		t.Fatal("existing Gateway API CRDs were overwritten")
	}
}

func TestAmbientRefusesCiliumClusters(t *testing.T) {
	step := ambientStep()
	step.ResolvedInputs["cluster-connection"] = domain.ResolvedArtifact{Value: json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ClusterConnection","metadata":{"name":"cluster","version":"1"},"spec":{"networking":"cilium","distribution":"k3s","server":"https://cluster.example.test","kubeconfig":"config"}}`)}
	report, err := (AmbientPlugin{Runner: &ambientCluster{values: map[string]string{}}}).Precheck(context.Background(), step, silent)
	if err != nil || report.Status == domain.HealthHealthy {
		t.Fatalf("Cilium cluster accepted: %+v %v", report, err)
	}
}

func TestAmbientChartsArePinned(t *testing.T) {
	ids := map[string]bool{}
	for _, chart := range ambientChartSet() {
		if len(chart.Digest) != 64 {
			t.Fatalf("chart %s is not pinned", chart.ID)
		}
		ids[chart.ID] = true
	}
	for _, id := range []string{"istiod", "istio-cni", "ztunnel", "gateway-api"} {
		if !ids[id] {
			t.Fatalf("ambient chart %s is missing", id)
		}
	}
}
