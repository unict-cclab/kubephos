package k3s

import (
	"context"
	"fmt"
	"kubephos.dev/kubephos/internal/plugins"
	"strings"
	"time"
)

// Emit only fixed diagnostic categories and systemd counters. Raw journal entries
// may contain credentials and must never be copied into operation logs.
const bootstrapDiagnosticsCommand = `set -eu
sudo systemctl show k3s k3s-agent --property=ActiveState,SubState,Result,NRestarts --no-pager 2>/dev/null || true
df -P / | awk 'NR==2 {print "rootDiskUsage=" $5}'
journal=$(sudo journalctl -u k3s -u k3s-agent -n 2000 --no-pager -o cat 2>/dev/null || true)
if printf '%s' "$journal" | grep -q 'extension-apiserver-authentication.*forbidden'; then echo cloudControllerRBAC=denied; fi
if printf '%s' "$journal" | grep -q 'No space left on device'; then echo diskFull=true; fi
if printf '%s' "$journal" | grep -q 'apply request took too long'; then echo etcdSlow=true; fi
if sudo journalctl -k -n 200 --no-pager -o cat 2>/dev/null | grep -Eq 'Out of memory|Killed process.*k3s'; then echo kernelOOM=true; fi`

func collectBootstrapDiagnostics(ctx context.Context, runner commandRunner, machines machineSet, access machineAccess, log plugins.Logger) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	detail := ""
	for _, m := range machines.Spec.Machines {
		if ctx.Err() != nil {
			break
		}
		nodeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		raw, err := runner.Run(nodeCtx, m, access, bootstrapDiagnosticsCommand)
		stop()
		if err != nil {
			_ = log("warning", "Kubernetes diagnostics unavailable for "+m.Name)
			continue
		}
		// Allowlist output too, so unexpected remote output cannot disclose secrets.
		fields := []string{}
		for _, line := range strings.Split(raw, "\n") {
			if safeBootstrapDiagnostic(line) {
				fields = append(fields, line)
			}
			if line == "cloudControllerRBAC=denied" {
				detail = "K3s cloud controller cannot read kube-system/extension-apiserver-authentication (RBAC denied) on " + m.Name
			}
		}
		if len(fields) > 0 {
			_ = log("warning", fmt.Sprintf("Kubernetes diagnostics on %s (%s): %s", m.Name, m.Address, strings.Join(fields, "; ")))
		}
	}
	return detail
}

func safeBootstrapDiagnostic(line string) bool {
	switch line {
	case "cloudControllerRBAC=denied", "diskFull=true", "etcdSlow=true", "kernelOOM=true":
		return true
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 || len(parts[1]) > 30 {
		return false
	}
	switch parts[0] {
	case "ActiveState", "SubState", "Result":
		if parts[1] == "" {
			return false
		}
		for _, r := range parts[1] {
			if (r < 'a' || r > 'z') && r != '-' {
				return false
			}
		}
		return true
	case "NRestarts", "rootDiskUsage":
		value := parts[1]
		if parts[0] == "rootDiskUsage" {
			value = strings.TrimSuffix(value, "%")
		}
		if value == "" {
			return false
		}
		for _, r := range value {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}
