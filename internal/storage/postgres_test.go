package storage

import "testing"

func TestDeletableExperimentStatus(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "canceled"} {
		if !deletableExperimentStatus(status) {
			t.Fatalf("terminal status %q was rejected", status)
		}
	}
	for _, status := range []string{"queued", "running", "prechecking", ""} {
		if deletableExperimentStatus(status) {
			t.Fatalf("active status %q was accepted", status)
		}
	}
}

func TestCancelableExperimentStatus(t *testing.T) {
	for _, status := range []string{"queued", "running"} {
		if !cancelableExperimentStatus(status) {
			t.Fatalf("active status %q was rejected", status)
		}
	}
	for _, status := range []string{"succeeded", "failed", "canceled", ""} {
		if cancelableExperimentStatus(status) {
			t.Fatalf("terminal status %q was accepted", status)
		}
	}
}
