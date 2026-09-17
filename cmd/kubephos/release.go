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

type releaseRegistryEndpoint struct {
	Kind string `json:"kind"`
	Spec struct {
		Host     string `json:"host"`
		CABundle string `json:"caBundle"`
	} `json:"spec"`
}

type releaseRegistryCredential struct {
	Kind string `json:"kind"`
	Spec struct {
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
		Project  string `json:"project"`
	} `json:"spec"`
}

func releaseMaintenance(configValue config.Config) error {
	if len(os.Args) >= 3 && os.Args[2] == "cluster-lens-rollout" {
		return releaseClusterLensRollout(configValue)
	}
	if len(os.Args) != 6 || os.Args[2] != "cluster-lens" {
		return errors.New("usage: kubephos release cluster-lens <harbor-resource-id> <docker-archive.tar> <version-tag>")
	}
	harborID, archivePath, versionTag := os.Args[3], os.Args[4], os.Args[5]
	if !strings.HasPrefix(harborID, "svc_") || !strings.HasPrefix(versionTag, "v") || strings.ContainsAny(versionTag, "/ :@") {
		return errors.New("release target or version tag is invalid")
	}
	archive, err := os.Stat(archivePath)
	if err != nil || !archive.Mode().IsRegular() || archive.Size() == 0 || archive.Size() > 512*1024*1024 {
		return errors.New("Docker image archive is unavailable or too large")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 30*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	harbor, err := store.GetManagedResource(ctx, harborID)
	if err != nil || harbor.Kind != "harbor" || harbor.Status != "ready" || harbor.PipelineRunID == "" {
		return errors.New("select a ready managed Harbor resource")
	}
	endpointArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-endpoint")
	if err != nil || endpointArtifact.Type != "RegistryEndpoint" || endpointArtifact.Sensitive {
		return errors.New("verified registry endpoint is unavailable")
	}
	credentialArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-push-credential")
	if err != nil || credentialArtifact.Type != "RegistryCredential" || !credentialArtifact.Sensitive {
		return errors.New("verified registry push identity is unavailable")
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	vault, err := secrets.Open(configValue.CredentialKeyFile)
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
	var endpoint releaseRegistryEndpoint
	var credential releaseRegistryCredential
	if json.Unmarshal(endpointValue, &endpoint) != nil || json.Unmarshal(credentialValue, &credential) != nil || endpoint.Kind != "RegistryEndpoint" || credential.Kind != "RegistryCredential" || endpoint.Spec.Host == "" || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || credential.Spec.Project != "kubephos-dev" {
		return errors.New("managed Harbor endpoint and push identity are incompatible")
	}
	temporary, err := os.MkdirTemp("", "kubephos-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	certificatePath := filepath.Join(temporary, "ca.crt")
	if err := os.WriteFile(certificatePath, []byte(endpoint.Spec.CABundle), 0600); err != nil {
		return err
	}
	authentication, _ := json.Marshal(map[string]any{"auths": map[string]any{endpoint.Spec.Host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(credential.Spec.Username + ":" + credential.Spec.Password))}}})
	authenticationPath := filepath.Join(temporary, "auth.json")
	if err := os.WriteFile(authenticationPath, authentication, 0600); err != nil {
		return err
	}
	image := endpoint.Spec.Host + "/kubephos-dev/cluster-lens:" + versionTag
	digestPath := filepath.Join(temporary, "digest")
	command := exec.CommandContext(ctx, "skopeo", "copy", "--dest-authfile", authenticationPath, "--dest-cert-dir", temporary, "--digestfile", digestPath, "docker-archive:"+archivePath, "docker://"+image)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("publish Cluster Lens: %w: %s", err, strings.TrimSpace(string(output)))
	}
	digest, err := os.ReadFile(digestPath)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(digest)), "sha256:") {
		return errors.New("published image digest was not returned")
	}
	inspect := exec.CommandContext(ctx, "skopeo", "inspect", "--raw", "--authfile", authenticationPath, "--cert-dir", temporary, "docker://"+image)
	manifest, err := inspect.Output()
	if err != nil {
		return fmt.Errorf("published image cannot be inspected: %w", err)
	}
	verified := sha256.Sum256(manifest)
	if "sha256:"+hex.EncodeToString(verified[:]) != strings.TrimSpace(string(digest)) {
		return errors.New("published image digest failed independent registry verification")
	}
	fmt.Printf("PASS %s@%s\n", image, strings.TrimSpace(string(digest)))
	return nil
}

func readReleaseArtifact(ctx context.Context, client *artifacts.Client, vault *secrets.Vault, artifact domain.Artifact) ([]byte, error) {
	value, err := client.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
	if err != nil {
		return nil, err
	}
	if artifact.Sensitive {
		value, err = vault.DecryptBound(artifact.EncryptionNonce, value, []byte(artifact.ID))
		if err != nil {
			return nil, err
		}
	}
	if err := artifacts.Verify(value, artifact.Digest, artifact.SizeBytes); err != nil {
		return nil, err
	}
	if !json.Valid(value) {
		return nil, errors.New("release artifact is not valid JSON")
	}
	return value, nil
}
