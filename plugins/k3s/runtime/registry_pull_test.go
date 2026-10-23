package k3s

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestRegistryPullRetriesTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name, failure      string
		failures, attempts int
		wantSuccess        bool
	}{
		{"recovers from EOF", "rpc error: code = Unavailable desc = error reading from server: EOF", 2, 3, true},
		{"bounds retries", "rpc error: code = Unavailable", 10, 5, false},
		{"rejects bad credentials", "unauthorized: authentication required", 10, 1, false},
		{"rejects missing image", "manifest unknown", 10, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A file preserves the attempt count across command substitutions.
			counter := t.TempDir() + "/attempts"
			script := `sudo() {
  printf 'attempt\n' >> "$COUNTER"
  count=$(wc -l < "$COUNTER")
  if [ "$count" -le "$FAILURES" ]; then printf '%s\n' "$FAILURE" >&2; return 17; fi
  return 0
}
sleep() { :; }
` + registryPullCommand("registry.internal/release/pilot:1.30.0") + `
status=$?
printf 'attempts=%s\n' "$(wc -l < "$COUNTER")"
exit "$status"
`
			cmd := exec.Command("sh", "-c", script)
			cmd.Env = append(cmd.Environ(), "COUNTER="+counter, "FAILURE="+tc.failure, "FAILURES="+strconv.Itoa(tc.failures))
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess || !strings.Contains(string(output), "attempts="+strconv.Itoa(tc.attempts)) {
				t.Fatalf("unexpected pull result: %v\n%s", err, output)
			}
			if !tc.wantSuccess && err.(*exec.ExitError).ExitCode() != 17 {
				t.Fatalf("pull exit code was lost: %v", err)
			}
		})
	}
}
