package applicationdeployer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type cleanupRunner struct {
	run func(context.Context, []string) (string, error)
}

func (r cleanupRunner) Run(ctx context.Context, _ string, _ []byte, args ...string) (string, error) {
	return r.run(ctx, args)
}
func TestNamespaceCleanupRetriesTransientReadAndWaitsForAbsence(t *testing.T) {
	reads, deletes := 0, 0
	runner := cleanupRunner{func(ctx context.Context, args []string) (string, error) {
		command := strings.Join(args, " ")
		if args[0] == "delete" {
			deletes++
			if !strings.Contains(command, "--wait=false") {
				t.Fatal(command)
			}
			return "deleted", nil
		}
		reads++
		if reads == 1 {
			return "namespace/app", nil
		}
		if reads == 2 {
			return "", errors.New("ServiceUnavailable")
		}
		return "", nil
	}}
	if err := deleteNamespace(context.Background(), runner, "", "app", discardLog, 100*time.Millisecond, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 || reads != 3 {
		t.Fatalf("deletes=%d reads=%d", deletes, reads)
	}
}
func TestNamespaceCleanupPersistsDiagnosticsWithoutForcingFinalizers(t *testing.T) {
	logs := ""
	runner := cleanupRunner{func(ctx context.Context, args []string) (string, error) {
		command := strings.Join(args, " ")
		if strings.Contains(command, "force") || args[0] == "patch" || args[0] == "replace" {
			t.Fatal(command)
		}
		if args[0] == "delete" {
			return "deleted", nil
		}
		if strings.Contains(command, "jsonpath=") {
			return `NamespaceDeletionContentFailure message=API-error finalizers=kubernetes`, nil
		}
		return "namespace/app", nil
	}}
	err := deleteNamespace(context.Background(), runner, "", "app", func(level, message string) error { logs += message; return nil }, 8*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "namespace cleanup timed out") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(logs, "NamespaceDeletionContentFailure") || !strings.Contains(logs, "get events") {
		t.Fatalf("missing diagnostics: %s", logs)
	}
}
func TestNamespaceCleanupDoesNotRetryForbiddenDelete(t *testing.T) {
	deletes := 0
	runner := cleanupRunner{func(ctx context.Context, args []string) (string, error) {
		if args[0] == "delete" {
			deletes++
			return "", errors.New("Forbidden: not authorized")
		}
		return "", nil
	}}
	err := deleteNamespace(context.Background(), runner, "", "app", discardLog, time.Second, time.Millisecond)
	if err == nil || deletes != 1 {
		t.Fatalf("err=%v deletes=%d", err, deletes)
	}
}
func TestNamespaceCleanupRetriesTransientDelete(t *testing.T) {
	deletes := 0
	runner := cleanupRunner{func(ctx context.Context, args []string) (string, error) {
		if args[0] == "delete" {
			deletes++
			if deletes == 1 {
				return "", errors.New("connection reset by peer")
			}
		}
		return "", nil
	}}
	if err := deleteNamespace(context.Background(), runner, "", "app", discardLog, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if deletes != 2 {
		t.Fatalf("deletes=%d", deletes)
	}
}
