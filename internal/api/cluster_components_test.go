package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClusterCreationRejectsInvalidComponentsBeforeProvisioning(t *testing.T) {
	for _, input := range []managedKubernetesClusterInput{
		{Name: "research", Components: []string{"kiali"}},
		{Name: "research", Networking: "cilium", Components: []string{"istio-sidecar"}},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/kubernetes-clusters", nil)
		if prepared := (&Server{}).prepareManagedCluster(response, request, input); prepared != nil {
			t.Fatalf("prepared invalid components %v", input.Components)
		}
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "invalid_components") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestClusterComponentCatalogEndpoint(t *testing.T) {
	response := httptest.NewRecorder()
	(&Server{}).listClusterComponents(response, httptest.NewRequest(http.MethodGet, "/api/v1/cluster-components", nil))
	var body struct {
		Items []struct {
			ID   string `json:"id"`
			Core bool   `json:"core"`
		} `json:"items"`
		Defaults map[string][]string `json:"defaults"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 || len(body.Defaults["flannel"]) != len(body.Items) || len(body.Defaults["cilium"]) >= len(body.Items) {
		t.Fatalf("catalog response = %s", response.Body.String())
	}
}
