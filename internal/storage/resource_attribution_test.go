package storage

import "testing"

func TestAttributionOnlyTracksCreationAndContentChanges(t *testing.T) {
	for _, test := range []struct {
		base, id, method, path string
		create, modify         bool
	}{
		{"/api/v1/experiment-configurations", "cfg_new", "POST", "/api/v1/experiment-configurations/old/clone", true, false},
		{"/api/v1/experiments", "exp", "POST", "/api/v1/experiments/start", true, false},
		{"/api/v1/experiments", "exp", "POST", "/api/v1/experiments/exp/cancel", false, false},
		{"/api/v1/experiment-configurations", "", "PUT", "/api/v1/experiment-configurations/cfg", false, true},
		{"/api/v1/users", "", "PUT", "/api/v1/users/user/clusters", false, true},
		{"/api/v1/kubernetes-clusters", "cluster", "POST", "/api/v1/kubernetes-clusters/cluster/recreate", false, true},
		{"/api/v1/catalog/models", "", "DELETE", "/api/v1/catalog/models/model", false, true},
		{"/api/v1/catalog/models", "model", "GET", "/api/v1/catalog/models/model", false, false},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			id, create, modify := attributionAction(test.base, test.id, test.method, test.path)
			if id == "" || create != test.create || modify != test.modify {
				t.Fatalf("got %q %v %v", id, create, modify)
			}
		})
	}
}
