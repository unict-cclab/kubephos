package metricscollector

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPlacementTracksUIDsOwnersAndMissingSamples(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	step := 15 * time.Second
	deployment := applicationDeployment{}
	deployment.Spec.Workloads = append(deployment.Spec.Workloads, struct {
		Name   string   `json:"name"`
		Traits []string `json:"traits"`
	}{Name: "api", Traits: []string{"schedulable"}})
	series := func(labels map[string]string, offsets ...int) metricSeries {
		s := metricSeries{Labels: labels}
		for _, v := range offsets {
			s.Points = append(s.Points, metricPoint{Timestamp: start.Add(time.Duration(v) * time.Second), Value: 1})
		}
		return s
	}
	owners := []metricSeries{series(map[string]string{"replicaset": "api-old", "owner_name": "api"}, 0), series(map[string]string{"replicaset": "api-new", "owner_name": "api"}, 30)}
	pods := []metricSeries{
		series(map[string]string{"uid": "old", "pod": "api-old-a", "node": "node-a", "created_by_kind": "ReplicaSet", "created_by_name": "api-old"}, 0),
		series(map[string]string{"uid": "new", "pod": "api-new-b", "node": "node-b", "created_by_kind": "ReplicaSet", "created_by_name": "api-new"}, 30),
		series(map[string]string{"uid": "foreign", "pod": "api-not-ours", "node": "node-a", "created_by_kind": "ReplicaSet", "created_by_name": "foreign"}, 0, 15, 30),
	}
	h := buildPlacement(pods, owners, deployment, start, start.Add(30*time.Second), step, "west")
	if h.Status != "available" || len(h.Snapshots) != 3 || len(h.Nodes) != 2 {
		t.Fatalf("unexpected history: %+v", h)
	}
	if h.Snapshots[0].Pods[0].UID != "old" || h.Snapshots[2].Pods[0].UID != "new" {
		t.Fatal("replacement UIDs were not preserved")
	}
	if h.Snapshots[1].Observed || len(h.Snapshots[1].Pods) != 0 {
		t.Fatal("missing samples must not carry forward or invent placement")
	}
	if h.Snapshots[2].Pods[0].Node != "node-b" || h.Snapshots[2].Pods[0].Cluster != "west" {
		t.Fatal("incorrect placement identity")
	}
}
func TestPlacementEmptyHistoryIsUnavailable(t *testing.T) {
	start := time.Unix(1000, 0)
	h := buildPlacement(nil, nil, applicationDeployment{}, start, start.Add(time.Minute), time.Second, "west")
	if h.Status != "unavailable" {
		t.Fatal("empty history must not be fabricated")
	}
}
func TestMergePlacementSeparatesClustersAndRejectsDifferentGrids(t *testing.T) {
	start := time.Unix(1000, 0)
	makeHistory := func(cluster string) *placementHistory {
		return &placementHistory{Status: "available", StepSeconds: 15, Nodes: []placementNode{{Name: "node", Cluster: cluster}}, Snapshots: []placementSnapshot{{Timestamp: start, Observed: true, Pods: []placementPod{{UID: cluster, Node: "node", Cluster: cluster}}}}}
	}
	a := mergePlacement(makeHistory("a"), makeHistory("b"))
	if len(a.Nodes) != 2 || len(a.Snapshots[0].Pods) != 2 {
		t.Fatal("clusters collapsed")
	}
	b := makeHistory("b")
	b.Snapshots[0].Timestamp = start.Add(time.Second)
	if mergePlacement(makeHistory("a"), b).Status != "unavailable" {
		t.Fatal("incompatible time grids must not be merged")
	}
}

type placementUnavailableRunner struct{ queries []string }

func (r *placementUnavailableRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	r.queries = append(r.queries, strings.Join(args, " "))
	return "", errors.New("metrics unavailable")
}
func TestPlacementFailureIsOptionalAndScopesReusedNamespaces(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	r := &placementUnavailableRunner{}
	p := Plugin{Runner: r}
	deployment := applicationDeployment{}
	deployment.Spec.Namespace = "app"
	deployment.Spec.NamespaceCreatedAt = &start
	h := p.collectPlacement(context.Background(), clusterConnection{}, deployment, observabilityCapability{}, serviceEndpoint{}, start, start.Add(time.Hour), time.Second)
	if h.Status != "unavailable" || h.StepSeconds < 3 {
		t.Fatal("failure or resolution bound lost")
	}
	query, err := url.QueryUnescape(r.queries[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`namespace="app"`, `kube_pod_created`, `>= 1000`, `kube_pod_status_phase`} {
		if !strings.Contains(query, expected) {
			t.Fatalf("missing scope %s", expected)
		}
	}
	deployment.Spec.External = true
	if p.collectPlacement(context.Background(), clusterConnection{}, deployment, observabilityCapability{}, serviceEndpoint{}, start, start.Add(time.Minute), time.Second) != nil {
		t.Fatal("external destination must not collect application placement")
	}
}
