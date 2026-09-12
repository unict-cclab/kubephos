package applicationinspector

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestOnlineBoutiquePackageContract(t *testing.T) {
	if os.Getenv("KUBEPHOS_INTEGRATION") != "1" {
		t.Skip("integration test disabled")
	}
	applications, err := catalog.LoadDirectory("../../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(applications[0].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	content, err := fetchPackage(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := materializeManifest(content.Manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "name: loadgenerator") || !strings.Contains(string(manifest), "name: node-proxy") {
		t.Fatal("materialized application contains the upstream load generator or is missing the node proxy")
	}
	workloads, endpoints, err := inspectManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := buildLoadScenarios(content.Scripts, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 12 || len(endpoints) != 2 || len(scenarios) != 1 || scenarios[0].TargetEndpoint != "node-proxy-http" || !strings.Contains(scenarios[0].Script, "class WebsiteUser") {
		t.Fatalf("unexpected materialized contract: workloads=%d endpoints=%d scenarios=%d", len(workloads), len(endpoints), len(scenarios))
	}
}
