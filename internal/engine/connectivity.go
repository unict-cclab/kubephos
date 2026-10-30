package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	probe "kubephos.dev/kubephos/internal/connectivity"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
)

func (w *Worker) runConnectivityMonitor(ctx context.Context) {
	for ctx.Err() == nil {
		w.checkConnectivity(ctx)
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (w *Worker) checkConnectivity(ctx context.Context) {
	connections, err := w.store.ListProviderConnections(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("list connectivity targets", "error", err)
		}
		return
	}
	jobs := []func(){}
	enqueue := func(connection bool, resourceID string, check func(context.Context) domain.Connectivity) {
		jobs = append(jobs, func() {
			lease := id.New("probe")
			claimed, err := w.store.ClaimConnectivityCheck(ctx, connection, resourceID, lease)
			if err != nil || !claimed {
				if err != nil && ctx.Err() == nil {
					slog.Error("claim connectivity check", "resource", resourceID, "error", err)
				}
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			report := check(checkCtx)
			cancel()
			if ctx.Err() != nil {
				return
			}
			saveCtx, saveCancel := context.WithTimeout(ctx, 5*time.Second)
			defer saveCancel()
			if err = w.store.SaveConnectivity(saveCtx, connection, resourceID, lease, report); err != nil {
				slog.Error("save connectivity check", "resource", resourceID, "error", err)
			}
		})
	}
	for _, connection := range connections {
		if connection.Provider == "proxmox" {
			enqueue(true, connection.ID, func(checkCtx context.Context) domain.Connectivity {
				return w.probeConnection(checkCtx, connection.ID, connection.Name)
			})
		}
	}
	for _, kind := range []string{"kubernetes-cluster", "harbor", "nfs"} {
		resources, err := w.store.ListManagedResources(ctx, kind)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("list connectivity targets", "kind", kind, "error", err)
			}
			continue
		}
		for _, resource := range resources {
			if resource.Status == "ready" {
				enqueue(false, resource.ID, func(checkCtx context.Context) domain.Connectivity { return w.probeResource(checkCtx, resource) })
			}
		}
	}
	// Keep network probes out of operation slots and bound simultaneous requests.
	queue := make(chan func())
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range queue {
				if ctx.Err() == nil {
					job()
				}
			}
		}()
	}
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			close(queue)
			group.Wait()
			return
		}
	}
	close(queue)
	group.Wait()
}
func connectivityReport(targets ...domain.ConnectivityTarget) domain.Connectivity {
	report := domain.Connectivity{Status: "reachable", CheckedAt: time.Now().UTC(), Targets: targets}
	priority := map[string]int{"reachable": 0, "unknown": 1, "checking": 2, "unavailable": 3, "access-denied": 4, "unreachable": 5}
	for _, target := range targets {
		if priority[target.Status] > priority[report.Status] {
			report.Status = target.Status
			report.Message = target.Message
		}
	}
	if len(targets) == 0 {
		report.Status = "unknown"
		report.Message = "No verified service endpoint is available."
	}
	return report
}
func (w *Worker) probeConnection(ctx context.Context, resourceID, name string) domain.Connectivity {
	unknown := func() domain.Connectivity {
		return connectivityReport(probe.Result(name, "unknown", "Could not read the saved Proxmox connection credentials."))
	}
	connection, err := w.store.GetProviderConnectionConfiguration(ctx, resourceID)
	if err != nil {
		return unknown()
	}
	var config struct {
		Endpoint      string `json:"endpoint"`
		CredentialRef string `json:"credentialRef"`
		VerifyTLS     bool   `json:"verifyTLS"`
	}
	if json.Unmarshal(connection.Configuration, &config) != nil || w.vault == nil {
		return unknown()
	}
	credential, err := w.store.GetEncryptedCredential(ctx, config.CredentialRef)
	if err != nil {
		return unknown()
	}
	raw, err := w.vault.Decrypt(credential.Nonce, credential.Ciphertext)
	if err != nil {
		return unknown()
	}
	var token struct {
		ID     string `json:"tokenId"`
		Secret string `json:"tokenSecret"`
	}
	if json.Unmarshal(raw, &token) != nil || token.ID == "" || token.Secret == "" {
		return unknown()
	}
	endpoint := strings.TrimSuffix(strings.TrimRight(config.Endpoint, "/"), "/api2/json") + "/api2/json/nodes"
	return connectivityReport(probe.HTTP(ctx, probe.HTTPOptions{Name: name, Address: endpoint, Authorization: "PVEAPIToken=" + token.ID + "=" + token.Secret, VerifyTLS: config.VerifyTLS, Service: "proxmox"}))
}
func (w *Worker) readConnectivityArtifact(ctx context.Context, artifact domain.Artifact) ([]byte, error) {
	if w.artifacts == nil {
		return nil, fmt.Errorf("artifact store unavailable")
	}
	raw, err := w.artifacts.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
	if err != nil {
		return nil, err
	}
	if artifact.Sensitive {
		if w.vault == nil {
			return nil, fmt.Errorf("artifact vault unavailable")
		}
		raw, err = w.vault.DecryptBound(artifact.EncryptionNonce, raw, []byte(artifact.ID))
		if err != nil {
			return nil, err
		}
	}
	if err = artifacts.Verify(raw, artifact.Digest, artifact.SizeBytes); err != nil {
		return nil, err
	}
	return raw, nil
}
func (w *Worker) probeResource(ctx context.Context, resource domain.ManagedResource) domain.Connectivity {
	unknown := func() domain.Connectivity {
		return connectivityReport(probe.Result(resource.Name, "unknown", "Could not read the saved service access configuration."))
	}
	var spec struct {
		Mode    string `json:"mode"`
		Members []struct {
			Name string `json:"name"`
		} `json:"members"`
	}
	if json.Unmarshal(resource.Spec, &spec) != nil {
		return unknown()
	}
	if resource.Kind == "kubernetes-cluster" {
		members := []string{resource.Name}
		if spec.Mode == "multi" {
			members = nil
			for _, member := range spec.Members {
				members = append(members, member.Name)
			}
		}
		targets := []domain.ConnectivityTarget{}
		for index, name := range members {
			stage := "kubernetes"
			if spec.Mode == "multi" {
				stage = fmt.Sprintf("c%d-kubernetes", index+1)
			}
			artifact, err := w.store.GetPipelineRunArtifactFromStage(ctx, resource.PipelineRunID, stage, "cluster-connection")
			if err != nil || artifact.Type != "ClusterConnection" || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
				targets = append(targets, probe.Result(name, "unknown", "A verified Kubernetes connection is unavailable."))
				continue
			}
			raw, err := w.readConnectivityArtifact(ctx, artifact)
			if err != nil {
				targets = append(targets, probe.Result(name, "unknown", "Could not read the saved Kubernetes connection."))
				continue
			}
			var value struct {
				Kind string `json:"kind"`
				Spec struct {
					Kubeconfig string `json:"kubeconfig"`
				} `json:"spec"`
			}
			if json.Unmarshal(raw, &value) != nil || value.Kind != "ClusterConnection" || value.Spec.Kubeconfig == "" {
				targets = append(targets, probe.Result(name, "unknown", "The saved Kubernetes connection is invalid."))
				continue
			}
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			targets = append(targets, probe.Kubernetes(checkCtx, name, value.Spec.Kubeconfig))
			cancel()
		}
		return connectivityReport(targets...)
	}
	outputs, err := w.store.ListPipelineRunArtifacts(ctx, resource.PipelineRunID)
	if err != nil {
		return unknown()
	}
	wanted := "RegistryEndpoint"
	if resource.Kind == "nfs" {
		wanted = "SharedStorageEndpoint"
	}
	for _, artifact := range outputs {
		if artifact.Type != wanted || artifact.VerifiedAt.IsZero() {
			continue
		}
		raw, err := w.readConnectivityArtifact(ctx, artifact)
		if err != nil {
			return unknown()
		}
		var value struct {
			Spec struct {
				Server   string `json:"server"`
				URL      string `json:"url"`
				Insecure bool   `json:"insecure"`
				CABundle string `json:"caBundle"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &value) != nil {
			return unknown()
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if resource.Kind == "nfs" {
			if value.Spec.Server == "" {
				return unknown()
			}
			return connectivityReport(probe.NFS(checkCtx, "NFS", value.Spec.Server))
		}
		credential, err := w.store.GetPipelineRunArtifact(ctx, resource.PipelineRunID, "registry-management-credential")
		if err != nil || credential.Type != "RegistryCredential" || !credential.Sensitive || credential.VerifiedAt.IsZero() {
			return unknown()
		}
		secret, err := w.readConnectivityArtifact(ctx, credential)
		if err != nil {
			return unknown()
		}
		var access struct {
			Spec struct {
				Username string `json:"username"`
				Password string `json:"password"`
				Server   string `json:"server"`
			} `json:"spec"`
		}
		if json.Unmarshal(secret, &access) != nil || access.Spec.Username == "" || access.Spec.Password == "" {
			return unknown()
		}
		endpoint, err := url.Parse(value.Spec.URL)
		if err != nil || access.Spec.Server != endpoint.Hostname() && access.Spec.Server != endpoint.Host {
			return unknown()
		}
		options := probe.HTTPOptions{Name: "Harbor", Address: strings.TrimRight(value.Spec.URL, "/") + "/api/v2.0/users/current", Authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte(access.Spec.Username+":"+access.Spec.Password)), VerifyTLS: !value.Spec.Insecure, CABundle: value.Spec.CABundle, Service: "harbor"}
		account := probe.HTTP(checkCtx, options)
		if account.Status != "reachable" {
			return connectivityReport(account)
		}
		options.Address = strings.TrimRight(value.Spec.URL, "/") + "/v2/"
		options.Authorization = ""
		options.Service = "registry"
		return connectivityReport(probe.HTTP(checkCtx, options))
	}
	return unknown()
}
