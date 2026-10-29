package mesh

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const meshValuesSecret = "kubephos-cilium-mesh-values"

// HelmReleaseValues decodes Helm's Secret storage without exposing values in logs.
func HelmReleaseValues(encoded string) (map[string]any, int, error) {
	outer, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, 0, errors.New("invalid Helm release encoding")
	}
	packed, err := base64.StdEncoding.DecodeString(string(outer))
	if err != nil {
		return nil, 0, errors.New("invalid Helm release payload")
	}
	reader, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, 0, errors.New("invalid Helm release compression")
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
	if err != nil || len(raw) > 16<<20 {
		return nil, 0, errors.New("invalid Helm release size")
	}
	var release struct {
		Config  map[string]any
		Version int
	}
	if json.Unmarshal(raw, &release) != nil || release.Config == nil {
		return nil, 0, errors.New("invalid Helm release values")
	}
	return release.Config, release.Version, nil
}

func expectedPeers(spec Spec, local string) []string {
	peers := []string{}
	for _, member := range spec.Members {
		if member.Name != local {
			peers = append(peers, member.Name)
		}
	}
	return peers
}

func meshValues(values map[string]any, peers []string) (map[string]any, error) {
	mesh, _ := values["clustermesh"].(map[string]any)
	config, _ := mesh["config"].(map[string]any)
	if config["enabled"] != true {
		return nil, errors.New("mesh configuration is not enabled")
	}
	actual := map[string]bool{}
	switch clusters := config["clusters"].(type) {
	case map[string]any:
		for name := range clusters {
			actual[name] = true
		}
	case []any:
		for _, entry := range clusters {
			m, _ := entry.(map[string]any)
			name, _ := m["name"].(string)
			if name == "" || actual[name] {
				return nil, errors.New("invalid mesh membership")
			}
			actual[name] = true
		}
	case nil:
	default:
		return nil, errors.New("invalid mesh membership")
	}
	if len(actual) != len(peers) {
		return nil, fmt.Errorf("expected %d remote clusters, found %d", len(peers), len(actual))
	}
	for _, peer := range peers {
		if !actual[peer] {
			return nil, fmt.Errorf("remote cluster %s is missing", peer)
		}
	}
	if mesh == nil {
		return nil, errors.New("mesh Helm values are missing")
	}
	result := map[string]any{"clustermesh": mesh}
	tls, _ := values["tls"].(map[string]any)
	if bundle, ok := tls["caBundle"]; ok {
		result["tls"] = map[string]any{"caBundle": bundle}
	}
	return result, nil
}

func currentMeshValues(ctx context.Context, runner Runner, config string) (map[string]any, error) {
	raw, err := runner.Kubectl(ctx, config, nil, "get", "secrets", "-n", "kube-system", "-l", "owner=helm,name=cilium,status=deployed", "-o", "json")
	if err != nil {
		return nil, errors.New("could not read the deployed Cilium Helm release")
	}
	var secrets struct {
		Items []struct{ Data map[string]string }
	}
	if json.Unmarshal([]byte(raw), &secrets) != nil || len(secrets.Items) != 1 {
		return nil, errors.New("deployed Cilium Helm release is unavailable")
	}
	values, _, err := HelmReleaseValues(secrets.Items[0].Data["release"])
	return values, err
}

// PersistMeshConfiguration makes K3s reconciliation retain peers and their CA
// bundle. Values stay in a Secret; HelmChartConfig contains only its reference.
// Existing HelmChartConfig values and unrelated Secret references are preserved.
func PersistMeshConfiguration(ctx context.Context, runner Runner, config, name string, values map[string]any, peers []string) error {
	selected, err := meshValues(values, peers)
	if err != nil {
		return err
	}
	raw, err := runner.Kubectl(ctx, config, nil, "get", "helmchartconfig", "cilium", "-n", "kube-system", "--ignore-not-found", "-o", "json")
	if err != nil {
		return errors.New("could not read Cilium HelmChartConfig")
	}
	chartExists := strings.TrimSpace(raw) != ""
	object := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if json.Unmarshal([]byte(raw), &object) != nil {
			return errors.New("invalid Cilium HelmChartConfig")
		}
	}
	spec, _ := object["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	references, _ := spec["valuesSecrets"].([]any)
	kept := []any{}
	for _, value := range references {
		reference, _ := value.(map[string]any)
		if reference["name"] != meshValuesSecret {
			kept = append(kept, value)
		}
	}
	kept = append(kept, map[string]any{"name": meshValuesSecret, "keys": []string{"values.yaml"}, "ignoreUpdates": false})
	raw, err = runner.Kubectl(ctx, config, nil, "get", "secret", meshValuesSecret, "-n", "kube-system", "--ignore-not-found", "-o", "json")
	if err != nil {
		return errors.New("could not check managed mesh values ownership")
	}
	if strings.TrimSpace(raw) != "" {
		var existing struct {
			Metadata struct {
				Labels      map[string]string
				Annotations map[string]string
			}
		}
		if json.Unmarshal([]byte(raw), &existing) != nil || existing.Metadata.Labels["app.kubernetes.io/managed-by"] != "kubephos" || existing.Metadata.Annotations["kubephos.dev/mesh-name"] != name {
			return errors.New("mesh values Secret is owned by another configuration")
		}
	}
	encoded, err := yaml.Marshal(selected)
	if err != nil {
		return errors.New("could not encode mesh configuration")
	}
	secret, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": meshValuesSecret, "namespace": "kube-system", "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos"}, "annotations": map[string]string{"kubephos.dev/mesh-name": name}}, "type": "Opaque", "data": map[string]string{"values.yaml": base64.StdEncoding.EncodeToString(encoded)}})
	// Never include kubectl output here: admission errors can quote Secret data.
	if _, err = runner.Kubectl(ctx, config, secret, "apply", "--server-side", "--field-manager=kubephos-mesh", "-f", "-"); err != nil {
		return errors.New("could not persist the managed mesh values Secret")
	}
	chartConfig, _ := json.Marshal(map[string]any{"apiVersion": "helm.cattle.io/v1", "kind": "HelmChartConfig", "metadata": map[string]any{"name": "cilium", "namespace": "kube-system"}, "spec": map[string]any{"valuesSecrets": kept}})
	if chartExists {
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"valuesSecrets": kept}})
		_, err = runner.Kubectl(ctx, config, nil, "patch", "helmchartconfig", "cilium", "-n", "kube-system", "--type=merge", "-p", string(patch))
	} else {
		_, err = runner.Kubectl(ctx, config, chartConfig, "apply", "--server-side", "--field-manager=kubephos-mesh", "-f", "-")
	}
	if err != nil {
		return errors.New("could not reference managed mesh values in HelmChartConfig")
	}

	return nil
}

func persistCurrentMesh(ctx context.Context, runner Runner, spec Spec, configs map[string]string, disconnect bool) error {
	for _, member := range spec.Members {
		values, err := currentMeshValues(ctx, runner, configs[member.Name])
		if err != nil {
			return fmt.Errorf("read mesh settings on %s: %w", member.Name, err)
		}
		peers := expectedPeers(spec, member.Name)
		if disconnect {
			peers = nil
		}
		if err = PersistMeshConfiguration(ctx, runner, configs[member.Name], spec.Name, values, peers); err != nil {
			return fmt.Errorf("persist mesh settings on %s: %w", member.Name, err)
		}
	}
	return nil
}

func waitForMeshMembership(ctx context.Context, runner Runner, config string, peers []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		raw, err := runner.Kubectl(ctx, config, nil, "get", "secret", "cilium-clustermesh", "-n", "kube-system", "-o", "json")
		var secret struct{ Data map[string]string }
		ready := err == nil && json.Unmarshal([]byte(raw), &secret) == nil
		for _, peer := range peers {
			if secret.Data[peer] == "" {
				ready = false
			}
		}
		count := 0
		for key := range secret.Data {
			if !strings.Contains(key, ".") {
				count++
			}
		}
		if ready && count == len(peers) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("expected %d remote cluster configurations did not become available", len(peers))
		case <-time.After(3 * time.Second):
		}
	}
}
