package api

import (
	"context"
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"strings"
	"testing"
)

func multiClusterFixture() managedKubernetesClusterInput {
	return managedKubernetesClusterInput{Name: "research", Mode: "multi", Members: []managedKubernetesClusterInput{{Name: "research-a", ClusterID: 1, PodCIDR: "10.64.0.0/16", ServiceCIDR: "10.128.0.0/16"}, {Name: "research-b", ClusterID: 2, PodCIDR: "10.65.0.0/16", ServiceCIDR: "10.129.0.0/16"}}}
}
func TestMultiClusterRejectsConflictingIdentitiesAndNetworks(t *testing.T) {
	if err := validateMultiClusterMembers(multiClusterFixture()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*managedKubernetesClusterInput){func(s *managedKubernetesClusterInput) { s.Members[1].Name = s.Members[0].Name }, func(s *managedKubernetesClusterInput) { s.Members[1].ClusterID = 1 }, func(s *managedKubernetesClusterInput) { s.Members[1].PodCIDR = "10.64.1.0/24" }, func(s *managedKubernetesClusterInput) { s.Members[1].ServiceCIDR = "10.64.0.0/16" }, func(s *managedKubernetesClusterInput) { s.Members[1].PodCIDR = "10.65.1.0/16" }, func(s *managedKubernetesClusterInput) { s.Members = s.Members[:1] }} {
		input := multiClusterFixture()
		change(&input)
		if err := validateMultiClusterMembers(input); err == nil {
			t.Fatalf("accepted invalid multi cluster input: %+v", input)
		}
	}
}
func TestPendingMultiClusterClaimsIncludeEveryMember(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(`{"mode":"multi","members":[{"baseVMID":100,"addressStart":"10.0.0.10"},{"baseVMID":200,"addressStart":"10.0.0.20"}]}`), &spec); err != nil {
		t.Fatal(err)
	}
	claims := provisioningClaimSpecs(spec)
	if len(claims) != 2 || intField(claims[1], "baseVMID") != 200 {
		t.Fatalf("members not reserved: %+v", claims)
	}
	raw, _ := json.Marshal(claims[0])
	if !strings.Contains(string(raw), "10.0.0.10") {
		t.Fatal("IP allocation lost")
	}
}

func TestMultiClusterExperimentsRequireExecutionSupport(t *testing.T) {
	server := &Server{}
	pipeline, failure := server.multiClusterExperimentPipeline(context.Background(), domain.ExperimentConfiguration{}, experimentConfigurationDefinitionInput{}, domain.ManagedResource{})
	if failure == nil || failure.code != "multicluster_execution_unavailable" || len(pipeline.Stages) != 0 {
		t.Fatalf("implicit application deployment without multi cluster execution support: %+v %+v", pipeline, failure)
	}
}
