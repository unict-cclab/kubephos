package api

import "testing"

func TestUserMutationAllowed(t *testing.T) {
	allowed := []string{
		"/api/v1/experiment-configurations",
		"/api/v1/experiment-configurations/configuration-1/clone",
		"/api/v1/experiment-instances",
		"/api/v1/experiment-suites",
		"/api/v1/experiments/experiment-1/cancel",
		"/api/v1/catalog/applications/application-1/1.0.0/resolve",
	}
	for _, path := range allowed {
		if !userMutationAllowed(path) {
			t.Fatalf("expected %q to be allowed", path)
		}
	}
	denied := []string{
		"/api/v1/catalog/applications",
		"/api/v1/catalog/applications/application-1/1.0.0",
		"/api/v1/kubernetes-clusters/cluster-1/recreate",
		"/api/v1/users",
	}
	for _, path := range denied {
		if userMutationAllowed(path) {
			t.Fatalf("expected %q to be denied", path)
		}
	}
}
