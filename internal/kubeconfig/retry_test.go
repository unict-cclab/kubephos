package kubeconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeKubectl(t *testing.T, script string) string {
	t.Helper()
	directory := t.TempDir()
	counter := filepath.Join(directory, "attempts")
	t.Setenv("KUBEPHOS_TEST_ATTEMPTS", counter)
	if err := os.WriteFile(filepath.Join(directory, "kubectl"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	return counter
}

const transientScript = `count=0
[ ! -f "$KUBEPHOS_TEST_ATTEMPTS" ] || count=$(cat "$KUBEPHOS_TEST_ATTEMPTS")
count=$((count + 1))
printf '%s' "$count" > "$KUBEPHOS_TEST_ATTEMPTS"
if [ "$count" -lt 3 ]; then
 echo 'could not get server API group list: dial tcp 192.0.2.1:6443: i/o timeout' >&2
 echo 'error: the server does not have a resource type "nodes"' >&2
 exit 1
fi
printf '{"items":[]}'
`

func TestReadOnlyRetriesDiscoveryFailureAndPreservesJSON(t *testing.T) {
	counter := fakeKubectl(t, transientScript)
	output, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 5}, "get", "nodes", "-o", "json")
	if err != nil || string(output) != `{"items":[]}` {
		t.Fatalf("output %q, error %v", output, err)
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "3" {
		t.Fatalf("attempts %s", count)
	}
}

func TestKubectlFailureKeepsTransportCauseInsteadOfDiscoveryTail(t *testing.T) {
	counter := fakeKubectl(t, transientScript)
	output, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 2}, "get", "nodes")
	if err == nil || !strings.Contains(string(output), "Kubernetes API unavailable") || !strings.Contains(string(output), "2 attempt(s)") || strings.Contains(string(output), "resource type") {
		t.Fatalf("output %q, error %v", output, err)
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "2" {
		t.Fatalf("attempts %s", count)
	}
}

func TestWritesAndStreamsAreNeverReplayed(t *testing.T) {
	for _, args := range [][]string{{"apply", "-f", "-"}, {"delete", "namespace", "example"}, {"create", "job", "example"}, {"patch", "deployment", "example"}, {"exec", "pod", "--", "get"}, {"get", "pods", "--watch"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			counter := fakeKubectl(t, transientScript)
			_, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 5}, args...)
			count, _ := os.ReadFile(counter)
			if err == nil || string(count) != "1" {
				t.Fatalf("attempts %s, error %v", count, err)
			}
		})
	}
}

func TestPermanentFailuresDoNotRetry(t *testing.T) {
	for _, failure := range []string{"Error from server (Forbidden): nodes is forbidden", "Unauthorized", "x509: certificate has expired", "error: the server doesn't have a resource type widgets", "error: invalid selector"} {
		t.Run(failure, func(t *testing.T) {
			counter := fakeKubectl(t, `echo x >> "$KUBEPHOS_TEST_ATTEMPTS"
printf '%s' "$KUBEPHOS_TEST_FAILURE" >&2
exit 1
`)
			t.Setenv("KUBEPHOS_TEST_FAILURE", failure)
			output, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 5}, "get", "nodes")
			count, _ := os.ReadFile(counter)
			if err == nil || string(output) != failure || string(count) != "x\n" {
				t.Fatalf("output %q, attempts %q, error %v", output, count, err)
			}
		})
	}
}

func TestRetryBackoffHonorsCancellation(t *testing.T) {
	counter := fakeKubectl(t, transientScript)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := runKubectl(ctx, "config", nil, retryPolicy{attempts: 5, initialDelay: time.Hour}, "get", "nodes")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "1" {
		t.Fatalf("attempts %s", count)
	}
}

func TestReadOnlyQueryHasPerAttemptDeadline(t *testing.T) {
	fakeKubectl(t, "exec sleep 10\n")
	started := time.Now()
	output, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 2, queryTimeout: 20 * time.Millisecond}, "get", "nodes")
	if err == nil || !strings.Contains(string(output), "timed out") || time.Since(started) > 2*time.Second {
		t.Fatalf("output %q, error %v", output, err)
	}
}

func TestReadinessWaitKeepsCallerDeadline(t *testing.T) {
	fakeKubectl(t, "sleep 0.05\necho ready\n")
	output, err := runKubectl(context.Background(), "config", nil, retryPolicy{attempts: 2, queryTimeout: time.Millisecond}, "wait", "--for=condition=Ready", "pods")
	if err != nil || strings.TrimSpace(string(output)) != "ready" {
		t.Fatalf("output %q, error %v", output, err)
	}
}

func TestRetryClassification(t *testing.T) {
	for _, message := range []string{"http2: client connection lost", "connection refused", "unexpected EOF", "Error from server (ServiceUnavailable)", "Error from server (TooManyRequests)", "the server is currently unable to handle the request", "TLS handshake timeout"} {
		if transientAPIFailure([]byte(message)) == "" {
			t.Fatalf("not transient: %s", message)
		}
	}
	if transientAPIFailure([]byte("i/o timeout\nError from server (Forbidden)")) != "" {
		t.Fatal("authorization failure must not retry")
	}
	if retry, _ := retryableCommand([]string{"--request-timeout=7s", "get", "pods"}); !retry {
		t.Fatal("global timeout flag should retain read-only classification")
	}
}
