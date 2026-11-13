package engine

import (
	"context"
	"encoding/json"
	"errors"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/plugins"
)

func ensureBootstrapImages(ctx context.Context, pluginID string, step domain.PlanStep, log plugins.Logger) error {
	if pluginID != "io.kubephos.cluster.k3s.bootstrap" || step.Cleanup {
		return nil
	}
	var spec struct {
		Reconcile bool `json:"reconcile"`
	}
	if json.Unmarshal(step.Input, &spec) != nil || spec.Reconcile {
		return nil
	}
	endpoint, ok := step.ResolvedInputs["registry-endpoint"]
	if !ok {
		return nil
	}
	credential, ok := step.ResolvedInputs["registry-credential"]
	if !ok || !credential.Sensitive {
		return errors.New("Verified registry publication identity is unavailable.")
	}
	var target managedcomponents.Endpoint
	var access managedcomponents.Credential
	if json.Unmarshal(endpoint.Value, &target) != nil || json.Unmarshal(credential.Value, &access) != nil {
		return errors.New("Verified registry image publication settings are invalid.")
	}
	return (managedcomponents.Syncer{}).Ensure(ctx, target, access, log)
}
