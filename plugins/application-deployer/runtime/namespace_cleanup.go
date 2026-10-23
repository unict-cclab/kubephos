package applicationdeployer

import (
	"context"
	"fmt"
	"kubephos.dev/kubephos/internal/plugins"
	"strings"
	"time"
)

// Acknowledgement of deletion is not proof of absence. Poll the API separately
// and persist diagnostic conditions rather than forcibly removing finalizers.
func deleteNamespace(ctx context.Context, runner commandRunner, kubeconfig, namespace string, log plugins.Logger, timeout, interval time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	requested := false
	nextDiagnostic := time.Now().Add(time.Minute)
	var lastError error
	for {
		if !requested {
			_, lastError = runner.Run(waitCtx, kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=false")
			if lastError == nil {
				requested = true
			} else if !transientNamespaceError(lastError) {
				namespaceCleanupDiagnostics(ctx, runner, kubeconfig, namespace, log)
				return lastError
			}
		}
		if requested {
			var value string
			value, lastError = runner.Run(waitCtx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", "name")
			if lastError == nil && strings.TrimSpace(value) == "" {
				return nil
			}
			if lastError != nil && !transientNamespaceError(lastError) {
				namespaceCleanupDiagnostics(ctx, runner, kubeconfig, namespace, log)
				return lastError
			}
		}
		if time.Now().After(nextDiagnostic) {
			namespaceCleanupDiagnostics(ctx, runner, kubeconfig, namespace, log)
			nextDiagnostic = time.Now().Add(time.Minute)
		}
		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			namespaceCleanupDiagnostics(ctx, runner, kubeconfig, namespace, log)
			// A final read closes the race between the last poll and the deadline.
			if ctx.Err() == nil {
				finalCtx, finalCancel := context.WithTimeout(ctx, 15*time.Second)
				value, err := runner.Run(finalCtx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", "name")
				finalCancel()
				if err == nil && strings.TrimSpace(value) == "" {
					return nil
				}
			}
			return fmt.Errorf("namespace cleanup timed out for %s: %w (last API error: %v)", namespace, waitCtx.Err(), lastError)
		case <-timer.C:
		}
	}
}

func transientNamespaceError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, text := range []string{"timeout", "timed out", "deadline exceeded", "connection refused", "connection reset", "unexpected eof", "too many requests", "serviceunavailable", "service unavailable", "internalerror", "internal server error", "server is currently unable", "unable to connect"} {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

func namespaceCleanupDiagnostics(ctx context.Context, runner commandRunner, kubeconfig, namespace string, log plugins.Logger) {
	diagnosticCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	commands := [][]string{
		{"get", "namespace", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.deletionTimestamp}{\"\\n\"}{.spec.finalizers}{\"\\n\"}{.status}"},
		{"get", "pods,persistentvolumeclaims,jobs,deployments,statefulsets,daemonsets", "-n", namespace, "--ignore-not-found", "-o", "jsonpath={range .items[*]}{.kind}/{.metadata.name} deletion={.metadata.deletionTimestamp} finalizers={.metadata.finalizers}{\"\\n\"}{end}"},
		{"get", "events", "-n", namespace, "--sort-by=.lastTimestamp", "-o", "custom-columns=TIME:.lastTimestamp,REASON:.reason,OBJECT:.involvedObject.name,MESSAGE:.message"},
	}
	for _, args := range commands {
		value, err := runner.Run(diagnosticCtx, kubeconfig, nil, args...)
		if len(value) > 24000 {
			value = value[:24000] + "\n[diagnostic output truncated]"
		}
		_ = log("warning", fmt.Sprintf("Namespace cleanup diagnostic [%s]: %s (error: %v)", strings.Join(args[:2], " "), value, err))
	}
}
