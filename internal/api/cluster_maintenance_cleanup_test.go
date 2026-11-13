package api

import (
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestMaintenanceCleanupPreservesOriginalServiceOwnership(t *testing.T) {
	original := domain.PipelineRun{Stages: []domain.PipelineRunStage{{StageID: "storage", PluginID: "nfs", Status: "succeeded", OperationID: "original-owner"}, {StageID: "platform", PluginID: "platform", Status: "succeeded", OperationID: "platform-owner"}}}
	failed := domain.PipelineRun{Stages: []domain.PipelineRunStage{{StageID: "machines", OperationID: "migrated-vms"}, {StageID: "storage", PluginID: "nfs", Status: "failed", OperationID: "precheck-only"}, {StageID: "platform", PluginID: "platform", Status: "pending"}}}
	result := mergeMaintenanceServices(original, failed, map[string]bool{"storage": true, "platform": true})
	if result.Stages[0].OperationID != "migrated-vms" || result.Stages[1].OperationID != "original-owner" || result.Stages[2].OperationID != "platform-owner" {
		t.Fatal(result)
	}
	// A replacement that actually executed keeps its own identity and ownership checks.
	result = mergeMaintenanceServices(original, failed, map[string]bool{})
	if result.Stages[1].OperationID != "precheck-only" {
		t.Fatal("overrode an executed replacement")
	}
	if failed.Stages[1].OperationID != "precheck-only" {
		t.Fatal("modified saved lifecycle")
	}
}
