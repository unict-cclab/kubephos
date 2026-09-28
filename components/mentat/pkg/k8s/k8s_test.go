package k8s

import "testing"

func TestPeerFromPodUsesPodIP(t *testing.T) {
	peer, ok := peerFromPod("node-a", "10.42.1.8")
	if !ok {
		t.Fatal("peerFromPod() rejected a complete peer")
	}
	if peer.Hostname != "node-a" || peer.Ip != "10.42.1.8" {
		t.Fatalf("peerFromPod() = %#v, want node name mapped to Pod IP", peer)
	}
}

func TestPeerFromPodRejectsIncompletePeers(t *testing.T) {
	for _, test := range []struct {
		nodeName string
		podIP    string
	}{
		{nodeName: "node-a"},
		{podIP: "10.42.1.8"},
	} {
		if peer, ok := peerFromPod(test.nodeName, test.podIP); ok {
			t.Fatalf("peerFromPod(%q, %q) = %#v, want rejected", test.nodeName, test.podIP, peer)
		}
	}
}
