package applicationinspector

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestBuiltInApplicationPackageContracts(t *testing.T) {
	if os.Getenv("KUBEPHOS_INTEGRATION") != "1" {
		t.Skip("integration test disabled")
	}
	applications, err := catalog.LoadDirectory("../../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	expectedWorkloads := map[string]int{"dev.kubephos.bank-of-anthos": 9, "dev.kubephos.bookinfo": 7, "dev.kubephos.online-boutique": 12, "dev.kubephos.train-ticket": 64}
	for _, application := range applications {
		application := application
		t.Run(application.ID, func(t *testing.T) {
			var descriptor catalog.Descriptor
			if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
				t.Fatal(err)
			}
			content, err := fetchPackage(context.Background(), descriptor)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := applyOverlays(content.Manifest, descriptor.Spec.Overlays, descriptor.Spec.Defaults)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = applyStorageClassMappings(manifest, descriptor.Spec.StorageClassMappings)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = applyImageMappings(manifest, descriptor.Spec.ImageMappings)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = materializeManifest(manifest, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(manifest), "name: loadgenerator") || !strings.Contains(string(manifest), "name: node-proxy") || strings.Contains(string(manifest), "storageClassName: nfs") || strings.Contains(string(manifest), "image: mongo\n") {
				t.Fatal("materialized application contains an upstream load generator or is missing the node proxy")
			}
			workloads, endpoints, err := inspectManifest(manifest, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			scenarios, err := buildLoadScenarios(content.Scripts, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if len(workloads) != expectedWorkloads[application.ID] || len(endpoints) != 2 || len(scenarios) != 1 || scenarios[0].TargetEndpoint != "node-proxy-http" || !strings.Contains(scenarios[0].Script, "class WebsiteUser") {
				t.Fatalf("unexpected materialized contract: workloads=%d endpoints=%d scenarios=%d", len(workloads), len(endpoints), len(scenarios))
			}
			if os.Getenv("KUBEPHOS_ADMISSION") == "1" {
				command := exec.CommandContext(context.Background(), "kubectl", "apply", "--dry-run=server", "--namespace", "default", "-f", "-")
				command.Stdin = bytes.NewReader(manifest)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("server-side admission failed: %v: %s", err, strings.TrimSpace(string(output)))
				}
			}
		})
	}
}
