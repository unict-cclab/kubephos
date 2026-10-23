package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshSnapshotShowsClustersAndDirectionalMeasurements(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "node_latency") {
			t.Error("used node network metrics in multi cluster mode")
		}
		value := "12.5"
		if strings.Contains(query, "bandwidth") {
			value = "1048576"
		}
		if strings.Contains(query, "loss") {
			value = "2"
		}
		fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"link":"a>b"},"value":[1,%q]},{"metric":{"link":"b>a"},"value":[1,%q]}]}}`, value, value)
	}))
	defer source.Close()
	server := server{meshClusters: []string{"a", "b"}, clusterName: "a", prometheusURL: source.URL}
	snap := snapshot{Nodes: []nodeView{{Name: "old-node"}}, Pods: []podView{{Name: "service-0", Node: "old-node"}}}
	if err := server.applyMeshSnapshot(context.Background(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Mode != "multi" || len(snap.Nodes) != 2 || len(snap.NodeEdges) != 2 || snap.Pods[0].Node != "a" {
		t.Fatalf("invalid mesh view %+v", snap)
	}
	edge := snap.NodeEdges[0]
	if edge.Source != "a" || edge.Target != "b" || edge.Value != 12.5 || edge.Bandwidth == nil || *edge.Bandwidth != 1048576 || edge.PacketLoss == nil || *edge.PacketLoss != 2 {
		t.Fatalf("incorrect link %+v", edge)
	}
}
