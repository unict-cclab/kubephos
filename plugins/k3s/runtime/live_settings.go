package k3s

import (
	"context"
	"encoding/json"
	"fmt"
)

// ApplyPoolSettings updates only application-node metadata and registration
// flags. It never invokes bootstrap, drains, node deletion or service restarts.
func ApplyPoolSettings(ctx context.Context, spec Spec, machinesRaw, accessRaw json.RawMessage) error {
	var machines machineSet
	var access machineAccess
	if err := json.Unmarshal(machinesRaw, &machines); err != nil {
		return fmt.Errorf("Verified cluster machines could not be read.")
	}
	if err := json.Unmarshal(accessRaw, &access); err != nil {
		return fmt.Errorf("Verified cluster access could not be read.")
	}
	if err := validateMachineArtifacts(machines, access); err != nil {
		return err
	}
	if err := validatePoolConfiguration(spec); err != nil {
		return err
	}
	if err := validatePoolCapacity(spec, len(machines.Spec.Machines)); err != nil {
		return err
	}
	for index, m := range machines.Spec.Machines {
		if m.Name != managedNodeName(spec, index) {
			return fmt.Errorf("The saved node topology no longer matches this cluster.")
		}
	}
	spec.Reconcile = true
	return reconcileNodeSettings(ctx, newSSHRunner(), machines.Spec.Machines[0], machines.Spec.Machines, access, spec, true)
}
