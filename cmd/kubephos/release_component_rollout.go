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
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

type managedRolloutSpec struct {
	Name          string
	Version       string
	Resource      string
	Namespace     string
	Container     string
	LegacyImages  []string
	HealthTimeout time.Duration
}

type rolloutWorkload struct {
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
	Status struct {
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
	} `json:"status"`
}

type rolloutNodeList struct {
	Items []struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	} `json:"items"`
}

func releaseManagedComponentRollout(configValue config.Config) error {
	checking := len(os.Args) == 6 && os.Args[5] == "--check"
	if len(os.Args) != 5 && !checking || !strings.HasPrefix(os.Args[3], "cluster_") {
		return errors.New("usage: kubephos release component-rollout <cluster-resource-id> <mon-agent|mentat> [--check]")
	}
	spec, ok := managedRolloutDefinition(os.Args[4])
	if !ok {
		return errors.New("managed component must be mon-agent or mentat")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 30*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	cluster, connection, endpoint, credential, err := loadManagedRolloutTarget(ctx, configValue, store, os.Args[3])
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "kubephos-component-rollout-")
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
	tagImage := managedcomponents.Image(endpoint.Spec.Host, spec.Name, spec.Version)
	manifest, err := exec.CommandContext(ctx, "skopeo", "inspect", "--raw", "--authfile", authenticationPath, "--cert-dir", temporary, "docker://"+tagImage).Output()
	if err != nil {
		return fmt.Errorf("managed %s image %s is not available in Harbor: %w", spec.Name, spec.Version, err)
	}
	digest := sha256.Sum256(manifest)
	targetImage := tagImage + "@sha256:" + hex.EncodeToString(digest[:])
	current, err := readManagedWorkload(ctx, kubeconfigPath, spec)
	if err != nil {
		return err
	}
	if !managedWorkloadImageAllowed(current, targetImage, endpoint.Spec.Host, spec) {
		return fmt.Errorf("refusing to replace unexpected %s image %q", spec.Name, current)
	}
	if checking {
		if err := verifyManagedComponentHealth(ctx, kubeconfigPath, spec); err != nil {
			return err
		}
		fmt.Printf("PASS cluster=%s component=%s current=%s target=%s\n", cluster.Name, spec.Name, current, targetImage)
		return nil
	}
	rollback := func(cause error) error {
		if current == targetImage {
			return cause
		}
		_, rollbackErr := kubectlRelease(ctx, kubeconfigPath, "set", "image", spec.Resource, "-n", spec.Namespace, spec.Container+"="+current)
		if rollbackErr == nil {
			_, rollbackErr = kubectlRelease(ctx, kubeconfigPath, "rollout", "status", spec.Resource, "-n", spec.Namespace, "--timeout=5m")
		}
		return fmt.Errorf("%s validation failed: %w; rollback: %v", spec.Name, cause, rollbackErr)
	}
	if current != targetImage {
		if _, err := kubectlRelease(ctx, kubeconfigPath, "set", "image", spec.Resource, "-n", spec.Namespace, spec.Container+"="+targetImage); err != nil {
			return err
		}
		if _, err := kubectlRelease(ctx, kubeconfigPath, "rollout", "status", spec.Resource, "-n", spec.Namespace, "--timeout=5m"); err != nil {
			return rollback(err)
		}
	}
	if err := waitForManagedComponentHealth(ctx, kubeconfigPath, spec); err != nil {
		return rollback(err)
	}
	details, _ := json.Marshal(map[string]string{"component": spec.Name, "image": targetImage})
	if err := store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "maintenance", Action: "managed.component.release", TargetType: "kubernetes-cluster", TargetID: cluster.ID, Outcome: "succeeded", Details: details}); err != nil {
		return err
	}
	fmt.Printf("PASS cluster=%s component=%s image=%s\n", cluster.Name, spec.Name, targetImage)
	return nil
}

func managedRolloutDefinition(name string) (managedRolloutSpec, bool) {
	switch name {
	case managedcomponents.MonAgentName:
		return managedRolloutSpec{Name: name, Version: managedcomponents.MonAgentVersion, Resource: "deployment/kubephos-mon-agent", Namespace: "kubephos-observability", Container: "kubephos-mon-agent", LegacyImages: []string{"ghcr.io/unict-cclab/mon-agent:v0.0.7"}, HealthTimeout: 3 * time.Minute}, true
	case managedcomponents.MentatName:
		return managedRolloutSpec{Name: name, Version: managedcomponents.MentatVersion, Resource: "daemonset/mentat", Namespace: "kubephos-observability", Container: "mentat", LegacyImages: []string{"ghcr.io/unict-cclab/mentat:v0.3.3", "ghcr.io/unict-cclab/kubephos-mentat:v0.3.4"}, HealthTimeout: 3 * time.Minute}, true
	default:
		return managedRolloutSpec{}, false
	}
}

func loadManagedRolloutTarget(ctx context.Context, configValue config.Config, store *storage.Store, clusterID string) (domain.ManagedResource, rolloutClusterConnection, releaseRegistryEndpoint, releaseRegistryCredential, error) {
	cluster, err := store.GetManagedResource(ctx, clusterID)
	if err != nil || cluster.Kind != "kubernetes-cluster" || cluster.Status != "ready" || cluster.PipelineRunID == "" {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("select a ready managed Kubernetes cluster")
	}
	var saved struct {
		HarborID string `json:"harborId"`
	}
	if json.Unmarshal(cluster.Spec, &saved) != nil || saved.HarborID == "" {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("the cluster does not reference a managed Harbor registry")
	}
	harbor, err := store.GetManagedResource(ctx, saved.HarborID)
	if err != nil || harbor.Kind != "harbor" || harbor.Status != "ready" || harbor.PipelineRunID == "" {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("the cluster Harbor registry is unavailable")
	}
	connectionArtifact, err := store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
	if err != nil || connectionArtifact.Type != "ClusterConnection" || !connectionArtifact.Sensitive {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("verified cluster access is unavailable")
	}
	endpointArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-endpoint")
	if err != nil || endpointArtifact.Type != "RegistryEndpoint" || endpointArtifact.Sensitive {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("verified Harbor endpoint is unavailable")
	}
	credentialArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-push-credential")
	if err != nil || credentialArtifact.Type != "RegistryCredential" || !credentialArtifact.Sensitive {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("verified Harbor identity is unavailable")
	}
	vault, err := secrets.Open(configValue.CredentialKeyFile)
	if err != nil {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	connectionValue, err := readReleaseArtifact(ctx, artifactStore, vault, connectionArtifact)
	if err != nil {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	endpointValue, err := readReleaseArtifact(ctx, artifactStore, vault, endpointArtifact)
	if err != nil {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	credentialValue, err := readReleaseArtifact(ctx, artifactStore, vault, credentialArtifact)
	if err != nil {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	var connection rolloutClusterConnection
	var endpoint releaseRegistryEndpoint
	var credential releaseRegistryCredential
	if json.Unmarshal(connectionValue, &connection) != nil || json.Unmarshal(endpointValue, &endpoint) != nil || json.Unmarshal(credentialValue, &credential) != nil || connection.Kind != "ClusterConnection" || connection.Spec.Kubeconfig == "" || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Host == "" || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") || credential.Kind != "RegistryCredential" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" {
		return domain.ManagedResource{}, rolloutClusterConnection{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("cluster and registry release artifacts are incompatible")
	}
	return cluster, connection, endpoint, credential, nil
}

func readManagedWorkload(ctx context.Context, kubeconfigPath string, spec managedRolloutSpec) (string, error) {
	raw, err := kubectlRelease(ctx, kubeconfigPath, "get", spec.Resource, "-n", spec.Namespace, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("managed %s workload is unavailable: %w", spec.Name, err)
	}
	var workload rolloutWorkload
	if json.Unmarshal(raw, &workload) != nil || workload.Metadata.Annotations["kubephos.dev/ownership-marker"] == "" || workload.Spec.Template.Metadata.Labels["app.kubernetes.io/managed-by"] != "kubephos" {
		return "", fmt.Errorf("managed %s workload does not satisfy the ownership contract", spec.Name)
	}
	current := ""
	for _, container := range workload.Spec.Template.Spec.Containers {
		if container.Name == spec.Container {
			if current != "" {
				return "", fmt.Errorf("managed %s workload has a duplicate container", spec.Name)
			}
			current = container.Image
		}
	}
	if current == "" {
		return "", fmt.Errorf("managed %s container is unavailable", spec.Name)
	}
	return current, nil
}

func managedWorkloadImageAllowed(current, target, host string, spec managedRolloutSpec) bool {
	if current == target || current == managedcomponents.Image(host, spec.Name, spec.Version) {
		return true
	}
	for _, image := range spec.LegacyImages {
		if current == image {
			return true
		}
		if separator := strings.LastIndex(image, ":"); separator >= 0 {
			managedLegacy := managedcomponents.Image(host, spec.Name, image[separator+1:])
			if current == managedLegacy || validPinnedManagedImage(current, managedLegacy) {
				return true
			}
		}
	}
	return validPinnedManagedImage(current, managedcomponents.Image(host, spec.Name, spec.Version))
}

func validPinnedManagedImage(current, image string) bool {
	prefix := image + "@sha256:"
	if !strings.HasPrefix(current, prefix) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(current, prefix))
	return err == nil && len(decoded) == sha256.Size
}

func waitForManagedComponentHealth(ctx context.Context, kubeconfigPath string, spec managedRolloutSpec) error {
	deadline := time.Now().Add(spec.HealthTimeout)
	var last error
	for {
		last = verifyManagedComponentHealth(ctx, kubeconfigPath, spec)
		if last == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func verifyManagedComponentHealth(ctx context.Context, kubeconfigPath string, spec managedRolloutSpec) error {
	if spec.Name == managedcomponents.MentatName {
		raw, err := kubectlRelease(ctx, kubeconfigPath, "get", spec.Resource, "-n", spec.Namespace, "-o", "json")
		if err != nil {
			return err
		}
		var workload rolloutWorkload
		if json.Unmarshal(raw, &workload) != nil || workload.Status.DesiredNumberScheduled == 0 || workload.Status.NumberReady != workload.Status.DesiredNumberScheduled {
			return errors.New("Mentat is not ready on every node")
		}
	}
	raw, err := kubectlRelease(ctx, kubeconfigPath, "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	var nodes rolloutNodeList
	if json.Unmarshal(raw, &nodes) != nil || len(nodes.Items) == 0 {
		return errors.New("Kubernetes node inventory is empty or invalid")
	}
	for _, node := range nodes.Items {
		if spec.Name == managedcomponents.MonAgentName {
			for _, key := range []string{"cpu-usage", "memory-usage", "disk-throughput", "network-throughput"} {
				if strings.TrimSpace(node.Metadata.Annotations[key]) == "" {
					return fmt.Errorf("node %s is missing resource annotation %s", node.Metadata.Name, key)
				}
			}
			continue
		}
		latency := false
		bandwidth := false
		for key, value := range node.Metadata.Annotations {
			latency = latency || strings.HasPrefix(key, "network-latency.") && strings.TrimSpace(value) != ""
			bandwidth = bandwidth || strings.HasPrefix(key, "network-bandwidth.") && strings.TrimSpace(value) != ""
		}
		if !latency || !bandwidth {
			return fmt.Errorf("node %s is missing Mentat network measurements", node.Metadata.Name)
		}
	}
	return nil
}
