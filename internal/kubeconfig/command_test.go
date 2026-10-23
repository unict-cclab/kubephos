package kubeconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunKubectlUsesPrivateTemporaryFileAndPreservesArguments(t *testing.T) {
	directory := t.TempDir()
	script := `#!/bin/sh
[ "$1" = "--kubeconfig" ] || exit 1
[ "$(stat -c %a "$2")" = "600" ] || exit 2
[ "$(cat "$2")" = "verified-config" ] || exit 3
[ "$3" = "apply" ] || exit 4
[ "$4" = "name with spaces" ] || exit 5
printf '%s\n' "$2"
cat
`
	if err := os.WriteFile(filepath.Join(directory, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	output, err := RunKubectl(context.Background(), "verified-config", []byte("manifest"), "apply", "name with spaces")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(output), "\n", 2)
	if len(parts) != 2 || parts[1] != "manifest" {
		t.Fatalf("unexpected output: %q", output)
	}
	if _, err := os.Stat(parts[0]); !os.IsNotExist(err) {
		t.Fatalf("temporary kubeconfig remains: %v", err)
	}
}

func TestRunKubectlRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunKubectl(ctx, "config", nil, "get", "pods"); err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}
