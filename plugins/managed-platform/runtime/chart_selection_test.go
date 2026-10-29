package managedplatform

import (
	"context"
	"fmt"
	"testing"
)

type selectedCharts struct{}

func (selectedCharts) Fetch(_ context.Context, chart chartSpec) ([]byte, error) {
	if chart.ID == "istio-base" || chart.ID == "istiod" || chart.ID == "kiali" {
		return nil, fmt.Errorf("unused service-mesh chart %s was downloaded", chart.ID)
	}
	return []byte(chart.ID), nil
}
func TestCiliumDownloadsOnlyChartsItInstalls(t *testing.T) {
	charts, err := (Plugin{Fetcher: selectedCharts{}}).fetchCharts(context.Background(), "cilium")
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != len(managedCharts)-3 {
		t.Fatalf("unexpected chart count: %d", len(charts))
	}
	for _, chart := range managedCharts {
		switch chart.ID {
		case "istio-base", "istiod", "kiali":
			if _, ok := charts[chart.ID]; ok {
				t.Fatal("downloaded unused mesh chart")
			}
		default:
			if len(charts[chart.ID]) == 0 {
				t.Fatalf("missing required chart %s", chart.ID)
			}
		}
	}
}
func TestSingleClusterKeepsServiceMeshChartDownloads(t *testing.T) {
	if _, err := (Plugin{Fetcher: selectedCharts{}}).fetchCharts(context.Background(), "flannel"); err == nil {
		t.Fatal("single-cluster mesh charts were skipped")
	}
}
