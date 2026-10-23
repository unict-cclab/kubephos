package main

import "testing"

func TestMeshPeersAreClassifiedByActualPodNetworks(t *testing.T) {
	clusters, err := meshClusters(`[{"name":"a","podCIDR":"10.64.0.0/16"},{"name":"b","podCIDR":"10.65.0.0/16"}]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ ip, cluster string }{{"10.64.3.7", "a"}, {"10.65.4.8", "b"}, {"192.168.1.10", ""}, {"invalid", ""}} {
		if got := meshPeerCluster(test.ip, clusters); got != test.cluster {
			t.Fatalf("%s mapped to %s", test.ip, got)
		}
	}
}
func TestMeshProbeConfigurationRejectsInvalidNetworks(t *testing.T) {
	for _, raw := range []string{`[]`, `[{"name":"a","podCIDR":"bad"},{"name":"b","podCIDR":"10.65.0.0/16"}]`} {
		if _, err := meshClusters(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
