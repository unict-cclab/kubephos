package harbor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryHealthProbeAuthenticatesAndRejectsUnauthorizedWithoutRetry(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required to test the Harbor startup probe")
	}
	for _, status := range []string{"200", "401", "403"} {
		t.Run(status, func(t *testing.T) {
			directory := t.TempDir()
			count := filepath.Join(directory, "count")
			response := filepath.Join(directory, "health.json")
			script := `#!/bin/sh
found=0
while test "$#" -gt 0; do
 if test "$1" = '-u'; then shift; test "$1" = 'admin:test-password' || exit 22; found=1; fi
 shift
done
test "$found" = 1 || exit 22
printf '1' >> "$COUNT_FILE"
printf '{"status":"healthy"}' > "$HEALTH_FILE"
printf '%s' "$HTTP_STATUS"
`
			if err := os.WriteFile(filepath.Join(directory, "curl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			command := strings.ReplaceAll(waitForRegistryHealthCommand("192.0.2.10", "test-password"), "/tmp/kubephos-harbor-health.json", response)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, "sh", "-c", command)
			process.WaitDelay = time.Second
			process.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "COUNT_FILE="+count, "HEALTH_FILE="+response, "HTTP_STATUS="+status)
			output, err := process.CombinedOutput()
			if status == "200" && err != nil {
				t.Fatalf("healthy probe failed: %v %s", err, output)
			}
			if status != "200" && (err == nil || !strings.Contains(string(output), "GET /api/v2.0/health") || !strings.Contains(string(output), status)) {
				t.Fatalf("missing actionable failure: %v %s", err, output)
			}
			if strings.Contains(string(output), "test-password") {
				t.Fatal("password exposed in diagnostics")
			}
			attempts, _ := os.ReadFile(count)
			if string(attempts) != "1" {
				t.Fatal("authentication failure was retried")
			}
		})
	}
}
