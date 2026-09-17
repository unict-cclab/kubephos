package storage

import "testing"

func TestDeletableCatalogStrategyStatus(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "canceled"} {
		if !deletableCatalogStrategyStatus(status) {
			t.Fatalf("terminal status %q was rejected", status)
		}
	}
	for _, status := range []string{"ready", "queued", "prechecking", "running", "verifying", ""} {
		if deletableCatalogStrategyStatus(status) {
			t.Fatalf("active status %q was accepted", status)
		}
	}
}
