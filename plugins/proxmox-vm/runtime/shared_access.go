package proxmoxvm

import (
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
	"kubephos.dev/kubephos/internal/domain"
)

func topologyProvisioningKey(step domain.PlanStep, reference string) (string, string, error) {
	if reference == "" {
		return generateSSHKey()
	}
	artifact, ok := step.ResolvedInputs["shared-access"]
	if !ok || artifact.ID != reference || artifact.Type != "MachineAccess" || !artifact.Sensitive {
		return "", "", errors.New("verified shared machine access is required")
	}
	var access machineAccess
	if json.Unmarshal(artifact.Value, &access) != nil || access.Kind != "MachineAccess" {
		return "", "", errors.New("shared machine access is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey))
	if err != nil {
		return "", "", errors.New("shared machine access contains an invalid SSH key")
	}
	return access.Spec.PrivateKey, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}
