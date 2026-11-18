package api

import (
	"testing"

	"kubephos.dev/kubephos/internal/plugins"
	hpa "kubephos.dev/kubephos/plugins/autoscalers/hpa/runtime"
	strategycontroller "kubephos.dev/kubephos/plugins/strategy-controller/runtime"
)

func TestStrategyPluginSelectsTheAutoscalerOfTheEntry(t *testing.T) {
	cpa := strategycontroller.Plugin{Kind: "autoscaler"}.Manifest()
	builtin := hpa.Plugin{}.Manifest()
	server := &Server{registry: plugins.NewRegistry(manifestOnlyPlugin{cpa}, manifestOnlyPlugin{builtin})}
	if !strategyRunsImage(cpa) || strategyRunsImage(builtin) {
		t.Fatal("only the Custom Pod Autoscaler runs a catalog image")
	}
	for pluginID, expected := range map[string]string{"": cpa.ID, cpa.ID: cpa.ID, builtin.ID: builtin.ID} {
		plugin, err := server.strategyPlugin("autoscaler", pluginID)
		if err != nil || plugin.Manifest().ID != expected {
			t.Fatalf("plugin %q resolved to %v, %v", pluginID, plugin, err)
		}
	}
	if _, err := server.strategyPlugin("descheduler", builtin.ID); err == nil {
		t.Fatal("an autoscaler plugin was accepted for a descheduler entry")
	}
}
