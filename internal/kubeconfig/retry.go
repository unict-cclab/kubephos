package kubeconfig

import (
	"strings"
	"time"
)

type retryPolicy struct {
	attempts     int
	queryTimeout time.Duration
	initialDelay time.Duration
}

var kubectlRetryPolicy = retryPolicy{attempts: 5, queryTimeout: 30 * time.Second, initialDelay: 2 * time.Second}

// Only observations may be replayed. A lost response to a write does not prove
// that the API server did not apply it; never replay an entire pipeline stage.
func retryableCommand(args []string) (retry, boundedQuery bool) {
	for len(args) > 0 && strings.HasPrefix(args[0], "--") && strings.Contains(args[0], "=") {
		args = args[1:]
	}
	if len(args) == 0 {
		return false, false
	}
	for _, arg := range args {
		if arg == "--watch" || arg == "--watch=true" || arg == "--watch-only" || arg == "-w" {
			return false, false
		}
	}
	switch args[0] {
	case "get", "api-resources", "api-versions", "version":
		return true, true
	case "wait":
		return true, false // Preserve the caller's workload-readiness deadline.
	case "rollout":
		return len(args) > 1 && args[1] == "status", false
	default:
		return false, false
	}
}

// Discovery can end with "server doesn't have a resource type" after transport
// errors. Inspect all diagnostics rather than trusting kubectl's final line.
func transientAPIFailure(output []byte) string {
	message := strings.ToLower(string(output))
	for _, permanent := range []string{"forbidden", "unauthorized", "x509:", "certificate signed by unknown authority", "certificate has expired", "you must be logged in"} {
		if strings.Contains(message, permanent) {
			return ""
		}
	}
	for _, marker := range []string{"i/o timeout", "context deadline exceeded", "client.timeout", "timeout awaiting response headers", "tls handshake timeout", "request canceled (client.timeout"} {
		if strings.Contains(message, marker) {
			return "timed out while contacting the Kubernetes API server"
		}
	}
	for _, marker := range []string{"connection refused", "connection reset", "connection lost", "unexpected eof", "no route to host", "network is unreachable", "temporary failure in name resolution"} {
		if strings.Contains(message, marker) {
			return "connection to the Kubernetes API server was interrupted"
		}
	}
	for _, marker := range []string{"serviceunavailable", "service unavailable", "server is currently unable to handle the request", "too many requests", "toomanyrequests", "gateway timeout", "gatewaytimeout", "servertimeout"} {
		if strings.Contains(message, marker) {
			return "Kubernetes API server is temporarily unavailable or overloaded"
		}
	}
	return ""
}
