package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/pluginssh"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

// Existing provisioning credentials remain valid so that historical artifacts,
// cleanup and recovery operations do not lose access during this migration.
const installSharedSSHKey = `set -eu
umask 077
key=$(cat)
test -n "$key"
test ! -L "$HOME/.ssh"
install -d -m 700 "$HOME/.ssh"
test ! -L "$HOME/.ssh/authorized_keys"
touch "$HOME/.ssh/authorized_keys"
if ! grep -Fqx -- "$key" "$HOME/.ssh/authorized_keys"; then
 printf '\n%s\n' "$key" >> "$HOME/.ssh/authorized_keys"
fi
chmod 600 "$HOME/.ssh/authorized_keys"`

func releaseClusterSSHShare(cfg config.Config) error {
	if len(os.Args) != 4 || !strings.HasPrefix(os.Args[3], "cluster_") {
		return errors.New("usage: kubephos release cluster-ssh-share <cluster-resource-id>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	store, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id FROM managed_resources WHERE id=$1 AND deleted_at IS NULL AND recreation_pipeline_run_id IS NULL AND deletion_pipeline_run_id IS NULL AND deletion_operation_id IS NULL FOR UPDATE`, os.Args[3]).Scan(&lockedID); err != nil {
		return errors.New("cluster is unavailable or undergoing maintenance")
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM experiments WHERE status IN ('queued','running')) OR EXISTS(SELECT 1 FROM pipeline_runs WHERE status IN ('queued','running')) OR EXISTS(SELECT 1 FROM operations WHERE status IN ('queued','prechecking','running','verifying'))`).Scan(&active); err != nil {
		return err
	}
	if active {
		return errors.New("wait for active operations and experiments to finish")
	}
	resource, err := store.GetManagedResource(ctx, lockedID)
	if err != nil {
		return err
	}
	var saved struct {
		Mode    string `json:"mode"`
		Members []struct {
			Name string `json:"name"`
		} `json:"members"`
	}
	if resource.Kind != "kubernetes-cluster" || resource.Status != "ready" || json.Unmarshal(resource.Spec, &saved) != nil || saved.Mode != "multi" || len(saved.Members) < 2 {
		return errors.New("select a ready multi cluster")
	}
	vault, err := secrets.Open(cfg.CredentialKeyFile)
	if err != nil {
		return err
	}
	client := artifacts.New(cfg.ArtifactEndpoint)
	read := func(stage, output, kind string, sensitive bool) ([]byte, error) {
		artifact, e := store.GetPipelineRunArtifactFromStage(ctx, resource.PipelineRunID, stage, output)
		if e != nil || artifact.Type != kind || artifact.Sensitive != sensitive || artifact.VerifiedAt.IsZero() {
			return nil, fmt.Errorf("verified %s is unavailable for %s", output, stage)
		}
		return readReleaseArtifact(ctx, client, vault, artifact)
	}
	type target struct {
		cluster string
		machine pluginssh.Target
		oldKey  string
	}
	targets := []target{}
	commonKey := ""
	for index, member := range saved.Members {
		stage := fmt.Sprintf("c%d-machines", index+1)
		raw, e := read(stage, "machine-access", "MachineAccess", true)
		if e != nil {
			return e
		}
		var access struct {
			Kind string `json:"kind"`
			Spec struct {
				PrivateKey string `json:"privateKey"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &access) != nil || access.Kind != "MachineAccess" {
			return errors.New("invalid machine access")
		}
		if _, e = ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey)); e != nil {
			return errors.New("invalid machine SSH key")
		}
		if index == 0 {
			commonKey = access.Spec.PrivateKey
		}
		raw, e = read(stage, "machine-set", "MachineSet", false)
		if e != nil {
			return e
		}
		var machines struct {
			Kind string `json:"kind"`
			Spec struct {
				Machines []struct {
					Address string `json:"address"`
					SSHPort int    `json:"sshPort"`
					SSHUser string `json:"sshUser"`
				} `json:"machines"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &machines) != nil || machines.Kind != "MachineSet" || len(machines.Spec.Machines) == 0 {
			return errors.New("invalid machine inventory")
		}
		for _, machine := range machines.Spec.Machines {
			if machine.Address == "" || machine.SSHUser == "" || machine.SSHPort < 1 {
				return errors.New("invalid machine SSH target")
			}
			targets = append(targets, target{member.Name, pluginssh.Target{Address: machine.Address, Port: machine.SSHPort, User: machine.SSHUser}, access.Spec.PrivateKey})
		}
	}
	signer, _ := ssh.ParsePrivateKey([]byte(commonKey))
	publicKey := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	runner := pluginssh.New()
	for _, target := range targets {
		if _, err = runner.Run(ctx, target.machine, target.oldKey, "true"); err != nil {
			return fmt.Errorf("existing SSH access failed for %s/%s", target.cluster, target.machine.Address)
		}
	}
	for _, target := range targets {
		if _, err = runner.RunWithInput(ctx, target.machine, target.oldKey, installSharedSSHKey, strings.NewReader(publicKey)); err != nil {
			return fmt.Errorf("could not install shared SSH access on %s/%s", target.cluster, target.machine.Address)
		}
		if _, err = runner.Run(ctx, target.machine, commonKey, "true"); err != nil {
			return fmt.Errorf("shared SSH verification failed for %s/%s", target.cluster, target.machine.Address)
		}
		fmt.Printf("PASS shared SSH access: %s/%s\n", target.cluster, target.machine.Address)
	}
	// Advertise the single download only after every machine accepted that key.
	if _, err = tx.Exec(ctx, `UPDATE managed_resources SET spec=jsonb_set(spec,'{sharedSSHKey}','true'::jsonb),updated_at=now() WHERE id=$1`, resource.ID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"clusters": len(saved.Members), "machines": len(targets)})
	if err = store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "maintenance", Action: "kubernetes-cluster.ssh-key.share", TargetType: "kubernetes-clusters", TargetID: resource.ID, Outcome: "succeeded", Details: details}); err != nil {
		return err
	}
	fmt.Printf("PASS one shared SSH key for %d clusters and %d machines\n", len(saved.Members), len(targets))
	return nil
}
