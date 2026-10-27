package nfscsi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedNFSVolumesAreIsolatedAcrossClustersAndPVCRecreation(t *testing.T) {
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"shared-storage"}`)
	var directories []string
	for range 2 {
		plan, err := (Plugin{}).Plan(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		var input stepInput
		if err = json.Unmarshal(plan.Steps[0].Input, &input); err != nil {
			t.Fatal(err)
		}
		raw, err := storageClassManifest(input, sharedStorageEndpoint{})
		if err != nil {
			t.Fatal(err)
		}
		var class struct {
			Parameters map[string]string `json:"parameters"`
		}
		if err = json.Unmarshal(raw, &class); err != nil {
			t.Fatal(err)
		}
		dir := class.Parameters["subDir"]
		if !strings.Contains(dir, input.Marker) || !strings.Contains(dir, "${pv.metadata.name}") {
			t.Fatalf("storage path must isolate installation and volume: %s", dir)
		}
		directories = append(directories, strings.ReplaceAll(dir, "${pv.metadata.name}", "pvc-same-test-id"))
		original := strings.ReplaceAll(dir, "${pv.metadata.name}", "pvc-original")
		recreated := strings.ReplaceAll(dir, "${pv.metadata.name}", "pvc-recreated")
		if original == recreated || strings.HasPrefix(original, recreated+"/") || strings.HasPrefix(recreated, original+"/") {
			t.Fatal("recreated PVC can alias or delete the original data directory")
		}
	}
	if directories[0] == directories[1] {
		t.Fatal("separate clusters resolve to the same NFS directory")
	}
}

func TestVerificationRejectsUnsafeSharedSubdirectory(t *testing.T) {
	input := stepInput{Spec: Spec{StorageClassName: "shared-storage"}, Marker: "owned-installation"}
	storage := sharedStorageEndpoint{}
	storage.Spec.Server = "10.10.0.12"
	storage.Spec.ExportPath = "/srv/kubephos"
	runner := &fakeRunner{installed: true, marker: input.Marker, subDir: "${pvc.metadata.namespace}/${pvc.metadata.name}"}
	if err := verifyManagedResources(context.Background(), runner, "config", input, storage); err == nil {
		t.Fatal("verification accepted a cross-cluster storage collision")
	}
	runner.subDir = volumeDirectory(input)
	if err := verifyManagedResources(context.Background(), runner, "config", input, storage); err != nil {
		t.Fatal(err)
	}
}
