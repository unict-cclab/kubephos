package mesh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIUsesMergedKubeconfigForSourceAndDestination(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cilium")
	script := `#!/bin/sh
set -eu
test "$1" = --kubeconfig
test "$2" = "$KUBECONFIG"
grep -q 'name: cluster-a' "$KUBECONFIG"
grep -q 'name: cluster-b' "$KUBECONFIG"
printf 'both contexts available'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "unrelated-config"))
	runner := &cliRunner{directory: dir, binary: binary}
	configs := map[string]string{"cluster-a": fixtureKubeconfig("cluster-a"), "cluster-b": fixtureKubeconfig("cluster-b")}
	for _, action := range []string{"connect", "disconnect", "status"} {
		output, err := runner.Run(context.Background(), configs, "clustermesh", action, "--context", "cluster-a", "--destination-context", "cluster-b")
		if err != nil || !strings.Contains(output, "both contexts available") {
			t.Fatalf("%s failed to pass destination configuration: %v %s", action, err, output)
		}
	}
	if os.Getenv("KUBECONFIG") != filepath.Join(dir, "unrelated-config") {
		t.Fatal("CLI modified the parent kubeconfig")
	}
	info, err := os.Stat(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("merged kubeconfig must remain private")
	}
}
