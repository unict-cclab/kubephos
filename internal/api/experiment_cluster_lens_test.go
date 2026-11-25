package api

import (
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestClusterLensNamespaceLinks(t *testing.T) {
	for _, tc := range []struct{ name, spec, want string }{
		{"single", `{"addressStart":"192.0.2.10","components":["cluster-lens"]}`, "http://192.0.2.10:32088?namespace=shop"},
		{"mesh", `{"mode":"multi","members":[{"addressStart":"192.0.2.10","components":["prometheus"]},{"addressStart":"192.0.2.20","components":["cluster-lens"]}]}`, "http://192.0.2.20:32088?namespace=shop"},
		{"not installed", `{"addressStart":"192.0.2.10","components":["prometheus"]}`, ""},
		{"no address", `{"components":["cluster-lens"]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clusterLensURL(json.RawMessage(tc.spec), "shop"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClusterLensUsesOnlyTheSelectedExecution(t *testing.T) {
	experiment := domain.Experiment{Variants: []domain.ExperimentVariant{{Trials: []domain.ExperimentTrial{{ID: "active", Status: "running", PipelineRunID: "run-active"}, {ID: "previous", Status: "succeeded", PipelineRunID: "run-previous"}, {ID: "pending", Status: "queued"}}}}}
	for _, tc := range []struct {
		id, want string
		found    bool
	}{{"", "active", true}, {"previous", "previous", true}, {"pending", "pending", false}, {"another-experiment", "", false}} {
		trial, found := clusterLensTrial(experiment, tc.id)
		if found != tc.found || trial.ID != tc.want {
			t.Fatalf("%s: got %s, %v", tc.id, trial.ID, found)
		}
	}
}
