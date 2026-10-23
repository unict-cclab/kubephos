package kubeconfig

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RunKubectl supplies a private, temporary kubeconfig and removes it on every
// exit path. Transient API failures are retried only for read-only commands;
// the caller's cancellation and deadline always take precedence.
func RunKubectl(ctx context.Context, value string, stdin []byte, args ...string) ([]byte, error) {
	return runKubectl(ctx, value, stdin, kubectlRetryPolicy, args...)
}

func runKubectl(ctx context.Context, value string, stdin []byte, policy retryPolicy, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "kubephos-cluster-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "kubeconfig")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		return nil, err
	}
	retry, boundedQuery := retryableCommand(args)
	retry = retry && stdin == nil
	attempts := 1
	if retry {
		attempts = policy.attempts
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := policy.initialDelay
	started := time.Now()
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx := ctx
		cancel := func() {}
		if retry && boundedQuery && policy.queryTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, policy.queryTimeout)
		}
		command := exec.CommandContext(attemptCtx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
		if stdin != nil {
			command.Stdin = bytes.NewReader(stdin)
		}
		output, err := command.CombinedOutput()
		attemptTimedOut := attemptCtx.Err() == context.DeadlineExceeded
		cancel()
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		if err == nil {
			return output, nil
		}
		reason := transientAPIFailure(output)
		if reason == "" && attemptTimedOut && retry && boundedQuery {
			reason = "timed out while contacting the Kubernetes API server"
		}
		if reason == "" {
			return output, err
		}
		if attempt == attempts {
			message := fmt.Sprintf("Kubernetes API unavailable: %s. Failed after %d attempt(s) over %s.", reason, attempt, time.Since(started).Round(time.Second))
			if !retry {
				message += " The command was not retried because it may have side effects."
			}
			return []byte(message), fmt.Errorf("%s: %w", message, err)
		}
		log.Printf("Kubernetes API unavailable: %s; retrying read-only command (attempt %d/%d) in %s", reason, attempt+1, attempts, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
	panic("unreachable kubectl retry loop")
}
