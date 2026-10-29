package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

// Member setups overlap according to their Proxmox placement, with a maximum
// of four. VM cloning stays serialized; Retry cleanup is a full barrier.
func withParallelClusterProvisioning(def domain.PipelineDefinition, members int) domain.PipelineDefinition {
	// Keep two lanes for a single/unknown server, and use the available worker
	// slots when placements span more independent Proxmox servers.
	targets := map[string]bool{}
	for _, stage := range def.Stages {
		if !strings.HasPrefix(stage.ID, "c") || strings.HasPrefix(stage.ID, "cleanup-") || !strings.HasSuffix(stage.ID, "-machines") {
			continue
		}
		var machines struct {
			ConnectionRef string   `json:"connectionRef"`
			Node          string   `json:"node"`
			MachineNodes  []string `json:"machineNodes"`
		}
		if json.Unmarshal(stage.Spec, &machines) != nil {
			continue
		}
		nodes := machines.MachineNodes
		if len(nodes) == 0 {
			nodes = []string{machines.Node}
		}
		for _, node := range nodes {
			if node != "" {
				targets[machines.ConnectionRef+"/"+node] = true
			}
		}
	}
	parallelism := max(1, min(members, 4, max(2, len(targets))))
	def.MaxParallelStages = parallelism
	previous := map[int]string{}
	cleanupTail := ""
	for i := range def.Stages {
		stage := &def.Stages[i]
		add := func(dependency string) {
			if dependency == "" {
				return
			}
			for _, d := range stage.DependsOn {
				if d == dependency {
					return
				}
			}
			stage.DependsOn = append(stage.DependsOn, dependency)
		}
		if strings.HasPrefix(stage.ID, "cleanup-") {
			add(cleanupTail)
			cleanupTail = stage.ID
			continue
		}
		for member := 1; member <= members; member++ {
			if !strings.HasPrefix(stage.ID, fmt.Sprintf("c%d-", member)) {
				continue
			}
			add(previous[member])
			previous[member] = stage.ID
			if stage.ID == fmt.Sprintf("c%d-machines", member) {
				add(cleanupTail)
				if member > 1 {
					add(fmt.Sprintf("c%d-machines", member-1))
				}
				if member > parallelism {
					add(previous[member-parallelism])
				}
			}
		}
		if stage.ID == "mesh" {
			for member := 1; member <= members; member++ {
				add(previous[member])
			}
		}
	}
	return def
}
