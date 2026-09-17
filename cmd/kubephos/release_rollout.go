package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

type rolloutClusterConnection struct {
	Kind string `json:"kind"`
	Spec struct {
		Kubeconfig string `json:"kubeconfig"`
	} `json:"spec"`
}

type rolloutDeployment struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func releaseClusterLensRollout(configValue config.Config) error {
	checking := len(os.Args) == 5 && os.Args[4] == "--check"
	if len(os.Args) != 4 && !checking || !strings.HasPrefix(os.Args[3], "cluster_") {
		return errors.New("usage: kubephos release cluster-lens-rollout <cluster-resource-id> [--check]")
	}
	clusterID := os.Args[3]
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 30*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	cluster, err := store.GetManagedResource(ctx, clusterID)
	if err != nil || cluster.Kind != "kubernetes-cluster" || cluster.Status != "ready" || cluster.PipelineRunID == "" {
		return errors.New("select a ready managed Kubernetes cluster")
	}
	var saved struct {
		HarborID string `json:"harborId"`
	}
	if json.Unmarshal(cluster.Spec, &saved) != nil || saved.HarborID == "" {
		return errors.New("the cluster does not reference a managed Harbor registry")
	}
	harbor, err := store.GetManagedResource(ctx, saved.HarborID)
	if err != nil || harbor.Kind != "harbor" || harbor.Status != "ready" || harbor.PipelineRunID == "" {
		return errors.New("the cluster Harbor registry is unavailable")
	}
	connectionArtifact, err := store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
	if err != nil || connectionArtifact.Type != "ClusterConnection" || !connectionArtifact.Sensitive {
		return errors.New("verified cluster access is unavailable")
	}
	endpointArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-endpoint")
	if err != nil || endpointArtifact.Type != "RegistryEndpoint" || endpointArtifact.Sensitive {
		return errors.New("verified Harbor endpoint is unavailable")
	}
	credentialArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-push-credential")
	if err != nil || credentialArtifact.Type != "RegistryCredential" || !credentialArtifact.Sensitive {
		return errors.New("verified Harbor identity is unavailable")
	}
	vault, err := secrets.Open(configValue.CredentialKeyFile)
	if err != nil {
		return err
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	connectionValue, err := readReleaseArtifact(ctx, artifactStore, vault, connectionArtifact)
	if err != nil {
		return err
	}
	endpointValue, err := readReleaseArtifact(ctx, artifactStore, vault, endpointArtifact)
	if err != nil {
		return err
	}
	credentialValue, err := readReleaseArtifact(ctx, artifactStore, vault, credentialArtifact)
	if err != nil {
		return err
	}
	var connection rolloutClusterConnection
	var endpoint releaseRegistryEndpoint
	var credential releaseRegistryCredential
	if json.Unmarshal(connectionValue, &connection) != nil || json.Unmarshal(endpointValue, &endpoint) != nil || json.Unmarshal(credentialValue, &credential) != nil || connection.Kind != "ClusterConnection" || connection.Spec.Kubeconfig == "" || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Host == "" || credential.Kind != "RegistryCredential" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") {
		return errors.New("cluster and registry release artifacts are incompatible")
	}
	temporary, err := os.MkdirTemp("", "kubephos-rollout-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	kubeconfigPath := filepath.Join(temporary, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte(connection.Spec.Kubeconfig), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temporary, "ca.crt"), []byte(endpoint.Spec.CABundle), 0600); err != nil {
		return err
	}
	authentication, _ := json.Marshal(map[string]any{"auths": map[string]any{endpoint.Spec.Host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(credential.Spec.Username + ":" + credential.Spec.Password))}}})
	authenticationPath := filepath.Join(temporary, "auth.json")
	if err := os.WriteFile(authenticationPath, authentication, 0600); err != nil {
		return err
	}
	tagImage := endpoint.Spec.Host + "/kubephos-dev/cluster-lens:v1.2.2"
	manifest, err := exec.CommandContext(ctx, "skopeo", "inspect", "--raw", "--authfile", authenticationPath, "--cert-dir", temporary, "docker://"+tagImage).Output()
	if err != nil {
		return fmt.Errorf("managed Cluster Lens v1.2.2 image is not available in Harbor: %w", err)
	}
	digest := sha256.Sum256(manifest)
	image := tagImage + "@sha256:" + hex.EncodeToString(digest[:])
	read, err := kubectlRelease(ctx, kubeconfigPath, "get", "deployment", "cluster-lens", "-n", "kubephos-observability", "-o", "json")
	if err != nil {
		return fmt.Errorf("Cluster Lens deployment is unavailable: %w", err)
	}
	var deployment rolloutDeployment
	if json.Unmarshal(read, &deployment) != nil || deployment.Metadata.Annotations["kubephos.dev/ownership-marker"] == "" || deployment.Spec.Template.Metadata.Labels["app.kubernetes.io/managed-by"] != "kubephos" || len(deployment.Spec.Template.Spec.Containers) != 1 || deployment.Spec.Template.Spec.Containers[0].Name != "cluster-lens" {
		return errors.New("Cluster Lens deployment does not satisfy the managed ownership contract")
	}
	current := deployment.Spec.Template.Spec.Containers[0].Image
	if current != "ghcr.io/unict-cclab/cluster-lens:v1.0.0" && current != image && !managedPreviousClusterLensImage(current, endpoint.Spec.Host) {
		return fmt.Errorf("refusing to replace unexpected Cluster Lens image %q", current)
	}
	if checking {
		fmt.Printf("PASS cluster=%s current=%s target=%s\n", cluster.Name, current, image)
		return nil
	}
	rollback := func(cause error) error {
		if current == image {
			return cause
		}
		_, rollbackErr := kubectlRelease(ctx, kubeconfigPath, "set", "image", "deployment/cluster-lens", "-n", "kubephos-observability", "cluster-lens="+current)
		if rollbackErr == nil {
			_, rollbackErr = kubectlRelease(ctx, kubeconfigPath, "rollout", "status", "deployment/cluster-lens", "-n", "kubephos-observability", "--timeout=3m")
		}
		return fmt.Errorf("Cluster Lens validation failed: %w; rollback: %v", cause, rollbackErr)
	}
	if current != image {
		if _, err := kubectlRelease(ctx, kubeconfigPath, "set", "image", "deployment/cluster-lens", "-n", "kubephos-observability", "cluster-lens="+image); err != nil {
			return err
		}
		if _, err := kubectlRelease(ctx, kubeconfigPath, "rollout", "status", "deployment/cluster-lens", "-n", "kubephos-observability", "--timeout=5m"); err != nil {
			return rollback(err)
		}
	}
	ready, err := kubectlRelease(ctx, kubeconfigPath, "get", "--raw=/api/v1/namespaces/kubephos-observability/services/http:cluster-lens:8088/proxy/api/snapshot")
	if err != nil {
		return rollback(fmt.Errorf("Cluster Lens API failed after rollout: %w", err))
	}
	var snapshot struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(ready, &snapshot) != nil || len(snapshot.Nodes) == 0 {
		return rollback(errors.New("Cluster Lens returned an empty or invalid cluster snapshot"))
	}
	stylesheet, err := kubectlRelease(ctx, kubeconfigPath, "get", "--raw=/api/v1/namespaces/kubephos-observability/services/http:cluster-lens:8088/proxy/styles.css")
	if err != nil || !strings.Contains(string(stylesheet), ".inspector-tabs") || !strings.Contains(string(stylesheet), ".selection-empty-icon") || !strings.Contains(string(stylesheet), "#graph.compact .pod-label") {
		return rollback(errors.New("Cluster Lens did not serve the updated frontend assets"))
	}
	details, _ := json.Marshal(map[string]any{"image": image, "nodes": len(snapshot.Nodes)})
	if err := store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "maintenance", Action: "managed.cluster-lens.release", TargetType: "kubernetes-cluster", TargetID: cluster.ID, Outcome: "succeeded", Details: details}); err != nil {
		return err
	}
	fmt.Printf("PASS cluster=%s image=%s nodes=%d\n", cluster.Name, image, len(snapshot.Nodes))
	return nil
}

func managedPreviousClusterLensImage(image, host string) bool {
	for _, version := range []string{"v1.1.0", "v1.2.0", "v1.2.1"} {
		prefix := host + "/kubephos-dev/cluster-lens:" + version + "@sha256:"
		if !strings.HasPrefix(image, prefix) {
			continue
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(image, prefix))
		return err == nil && len(digest) == sha256.Size
	}
	return false
}

func kubectlRelease(ctx context.Context, kubeconfigPath string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", kubeconfigPath}, arguments...)...)
	value, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl: %w: %s", err, strings.TrimSpace(string(value)))
	}
	return value, nil
}
