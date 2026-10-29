package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestMultiClusterParallelismPreservesCloneKeyAndCleanupBarriers(t *testing.T) {
	def := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "cleanup-platform"}, {ID: "cleanup-machines"}}}
	for m := 1; m <= 4; m++ {
		for _, phase := range []string{"machines", "kubernetes", "storage", "observability", "platform"} {
			def.Stages = append(def.Stages, domain.PipelineStage{ID: fmt.Sprintf("c%d-%s", m, phase)})
		}
	}
	def.Stages = append(def.Stages, domain.PipelineStage{ID: "mesh"})
	def = withParallelClusterProvisioning(def, 4)
	def = withParallelClusterProvisioning(def, 4)
	dependencies := map[string][]string{}
	seen := map[string]bool{}
	for _, s := range def.Stages {
		for _, d := range s.DependsOn {
			if !seen[d] {
				t.Fatalf("invalid/forward dependency %s -> %s", s.ID, d)
			}
		}
		seen[s.ID] = true
		dependencies[s.ID] = s.DependsOn
	}
	for stage, want := range map[string][]string{"cleanup-machines": {"cleanup-platform"}, "c1-machines": {"cleanup-machines"}, "c2-machines": {"cleanup-machines", "c1-machines"}, "c3-machines": {"cleanup-machines", "c2-machines", "c1-platform"}, "c4-machines": {"cleanup-machines", "c3-machines", "c2-platform"}, "mesh": {"c1-platform", "c2-platform", "c3-platform", "c4-platform"}} {
		for _, d := range want {
			if !slices.Contains(dependencies[stage], d) {
				t.Fatalf("missing barrier %s -> %s", stage, d)
			}
		}
	}
	if def.MaxParallelStages != 2 {
		t.Fatal("unbounded provisioning")
	}
}

func TestRepeatedMultiClusterRetryDropsPreviousCleanupDependencies(t *testing.T) {
	def := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "cleanup-old"}, {ID: "c1-machines"}, {ID: "c1-platform"}, {ID: "c2-machines"}, {ID: "c2-platform"}, {ID: "mesh"}}}
	def = withParallelClusterProvisioning(def, 2)
	provision := clusterProvisioningStages(def)
	retry := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "cleanup-new"}}}
	retry.Stages = append(retry.Stages, provision.Stages...)
	retry = withParallelClusterProvisioning(retry, 2)
	for _, stage := range retry.Stages {
		if slices.Contains(stage.DependsOn, "cleanup-old") {
			t.Fatal("Retry retained obsolete cleanup dependency")
		}
	}
	if !slices.Contains(retry.Stages[1].DependsOn, "cleanup-new") {
		t.Fatal("Retry omitted new cleanup barrier")
	}
}

func TestMultiClusterParallelismUsesPlacementWithBoundedWorkerSlots(t *testing.T) {
	for _, servers := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(fmt.Sprint(servers), func(t *testing.T) {
			cleanupSpec := json.RawMessage(`{"connectionRef":"old","machineNodes":["old-a","old-b","old-c","old-d"]}`)
			def := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "cleanup-machines", Spec: cleanupSpec}}}
			for m := 1; m <= 6; m++ {
				raw, _ := json.Marshal(map[string]any{"connectionRef": "proxmox", "node": "template-server", "machineNodes": []string{fmt.Sprintf("pve%d", (m-1)%servers)}})
				def.Stages = append(def.Stages, domain.PipelineStage{ID: fmt.Sprintf("c%d-machines", m), Spec: raw}, domain.PipelineStage{ID: fmt.Sprintf("c%d-platform", m)})
			}
			def.Stages = append(def.Stages, domain.PipelineStage{ID: "mesh"})
			def = withParallelClusterProvisioning(def, 6)
			want := min(4, max(2, servers))
			if def.MaxParallelStages != want {
				t.Fatalf("got %d slots, want %d", def.MaxParallelStages, want)
			}
			for _, stage := range def.Stages {
				for m := 1; m <= 6; m++ {
					if stage.ID != fmt.Sprintf("c%d-machines", m) {
						continue
					}
					if m > 1 && !slices.Contains(stage.DependsOn, fmt.Sprintf("c%d-machines", m-1)) {
						t.Fatal("VM cloning became parallel")
					}
					if m > want && !slices.Contains(stage.DependsOn, fmt.Sprintf("c%d-platform", m-want)) {
						t.Fatal("member setup limit was lost")
					}
					if m > 1 && m <= want && slices.Contains(stage.DependsOn, fmt.Sprintf("c%d-platform", m-1)) {
						t.Fatal("independent member setup was serialized")
					}
				}
			}
		})
	}
}
