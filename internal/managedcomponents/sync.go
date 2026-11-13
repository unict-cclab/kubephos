package managedcomponents

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
)

type Endpoint struct {
	Kind string `json:"kind"`
	Spec struct {
		Host     string `json:"host"`
		CABundle string `json:"caBundle"`
	} `json:"spec"`
}
type Credential struct {
	Kind string `json:"kind"`
	Spec struct {
		Server   string   `json:"server"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		Project  string   `json:"project"`
		Scopes   []string `json:"scopes"`
	} `json:"spec"`
}
type Syncer struct {
	Run        func(context.Context, ...string) ([]byte, error)
	ArchiveDir string
}

// Ensure publishes missing release images before provisioning depends on them.
// Credentials never enter command arguments, logs, or persistent files.
func (syncer Syncer) Ensure(ctx context.Context, endpoint Endpoint, credential Credential, log func(string, string) error) error {
	project := ExistingProject(credential.Spec.Project)
	if endpoint.Kind != "RegistryEndpoint" || credential.Kind != "RegistryCredential" ||
		endpoint.Spec.Host == "" || credential.Spec.Server != endpoint.Spec.Host ||
		credential.Spec.Username == "" || credential.Spec.Password == "" ||
		!ValidProject(project) || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") {
		return errors.New("Managed registry endpoint or image publication identity is invalid.")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "kubephos-registry-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = os.WriteFile(filepath.Join(dir, "ca.crt"), []byte(endpoint.Spec.CABundle), 0600); err != nil {
		return err
	}
	auth, _ := json.Marshal(map[string]any{"auths": map[string]any{endpoint.Spec.Host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(credential.Spec.Username + ":" + credential.Spec.Password))}}})
	authfile := filepath.Join(dir, "auth.json")
	if err = os.WriteFile(authfile, auth, 0600); err != nil {
		return err
	}
	run := syncer.Run
	if run == nil {
		run = func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "skopeo", args...).CombinedOutput()
		}
	}
	archiveDir := syncer.ArchiveDir
	if archiveDir == "" {
		archiveDir = "/opt/kubephos/managed-images"
	}
	for _, component := range Catalog() {
		image := Image(endpoint.Spec.Host, component.Name, component.Version, project)
		inspect := []string{"inspect", "--raw", "--authfile", authfile, "--cert-dir", dir, "docker://" + image}
		output, inspectErr := run(ctx, inspect...)
		if inspectErr == nil {
			continue
		}
		lower := strings.ToLower(string(output))
		if !strings.Contains(lower, "manifest unknown") && !strings.Contains(lower, "name unknown") && !strings.Contains(lower, "not found") {
			return fmt.Errorf("Cannot verify managed image %s. Check Harbor connectivity and registry permissions: %w", component.Name, inspectErr)
		}
		if log != nil {
			if err = log("info", "Publishing required image "+image); err != nil {
				return err
			}
		}
		source := "docker://" + component.SourceImage
		archive := filepath.Join(archiveDir, component.Name+"-"+component.Version+".tar")
		if info, e := os.Stat(archive); e == nil && info.Mode().IsRegular() {
			source = "docker-archive:" + archive
		}
		digestFile := filepath.Join(dir, "digest")
		if _, err = run(ctx, "copy", "--retry-times", "3", "--dest-authfile", authfile, "--dest-cert-dir", dir, "--digestfile", digestFile, source, "docker://"+image); err != nil {
			return fmt.Errorf("Cannot publish required image %s:%s to Harbor: %w", component.Name, component.Version, err)
		}
		manifest, err := run(ctx, inspect...)
		if err != nil {
			return fmt.Errorf("Published image %s could not be verified: %w", component.Name, err)
		}
		digest, err := os.ReadFile(digestFile)
		if err != nil {
			return fmt.Errorf("Published image %s has no verified digest", component.Name)
		}
		hash := sha256.Sum256(manifest)
		if strings.TrimSpace(string(digest)) != "sha256:"+hex.EncodeToString(hash[:]) {
			return fmt.Errorf("Published image %s failed digest verification", component.Name)
		}
	}
	return nil
}
