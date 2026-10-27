package managedobservability

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type crashRunner struct {
	fakeRunner
	previous    bool
	unavailable bool
	logRequests int
}

func (r *crashRunner) Kubectl(ctx context.Context, config string, stdin []byte, args ...string) (string, error) {
	if strings.Join(args, " ") == "get pods -n "+namespace+" -o json" {
		return `{"items":[{"metadata":{"name":"prometheus-0"},"status":{"phase":"Running","containerStatuses":[{"name":"prometheus","ready":false,"restartCount":5,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"Error","exitCode":2}}},{"name":"config-reloader","ready":true}]}}]}`, nil
	}
	if len(args) > 0 && args[0] == "logs" {
		r.logRequests++
		command := strings.Join(args, " ")
		r.previous = strings.Contains(command, "--previous") && strings.Contains(command, "--limit-bytes=8192") && strings.Contains(command, "-c prometheus")
		if r.unavailable {
			return "", errors.New("previous container no longer exists")
		}
		return `opening storage failed: lock DB directory: resource temporarily unavailable`, nil
	}
	return r.fakeRunner.Kubectl(ctx, config, stdin, args...)
}
func TestPodFailureIncludesCrashReason(t *testing.T) {
	err := verifyPods(context.Background(), &crashRunner{}, "config")
	if err == nil || !strings.Contains(err.Error(), "CrashLoopBackOff") || !strings.Contains(err.Error(), "exit code 2") {
		t.Fatalf("missing crash details: %v", err)
	}
}
func TestFailureDiagnosticsCapturePreviousCrashWithoutHealthySidecar(t *testing.T) {
	runner := &crashRunner{}
	var messages []string
	logFailedContainers(context.Background(), runner, "config", func(level, message string) error { messages = append(messages, message); return nil })
	if runner.logRequests != 1 || !runner.previous {
		t.Fatalf("expected bounded previous logs for only failed container: %+v", runner)
	}
	text := strings.Join(messages, "\n")
	if !strings.Contains(text, "lock DB directory") || !strings.Contains(text, "CrashLoopBackOff") {
		t.Fatalf("missing useful crash diagnostic: %s", text)
	}
}
func TestUnavailableCrashLogDoesNotBlockCleanup(t *testing.T) {
	runner := &crashRunner{unavailable: true}
	var messages []string
	logFailedContainers(context.Background(), runner, "config", func(level, message string) error { messages = append(messages, message); return nil })
	if !strings.Contains(strings.Join(messages, "\n"), "previous container no longer exists") {
		t.Fatal("missing diagnostic retrieval failure")
	}
}
