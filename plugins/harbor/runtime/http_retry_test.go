package harbor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/managedcomponents"
)

func TestCommandChainStopsAtCompoundStageFailure(t *testing.T) {
	command := chainCommands([]string{"false", "status=201; test \"$status\" = 201", "printf reached"})
	output, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err == nil || strings.Contains(string(output), "reached") {
		t.Fatalf("failure bypassed: %v %s", err, output)
	}
}

func TestCreateAPIRetriesTransientErrorsOnly(t *testing.T) {
	for _, test := range []struct {
		name, responses string
		success         bool
		calls           string
	}{
		{"gateway then created", "502 201", true, "2"},
		{"gateway then already created", "502 409", true, "2"},
		{"unauthorized", "401", false, "1"},
		{"gateway persists", "502 502 502 502 502 502", false, "6"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!/bin/sh
count=0
if test -f "$COUNT_FILE"; then count=$(cat "$COUNT_FILE"); fi
count=$((count+1)); printf '%s' "$count" > "$COUNT_FILE"
printf '%s\n' "$RESPONSES" | awk -v position="$count" '{print $position}'
`
			if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			count := filepath.Join(dir, "count")
			command := exec.Command("sh", "-c", createAPICommand("test project", "192.0.2.10", "fake-password", "'{}'", "projects", "/tmp/test-response"))
			command.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "COUNT_FILE="+count, "RESPONSES="+test.responses)
			output, err := command.CombinedOutput()
			if (err == nil) != test.success {
				t.Fatalf("unexpected result: %v %s", err, output)
			}
			calls, err := os.ReadFile(count)
			if err != nil {
				t.Fatal(err)
			}
			if string(calls) != test.calls {
				t.Fatalf("got %s requests, want %s", calls, test.calls)
			}
		})
	}
}

func TestHealthGateRequiresOverallHealthyStatus(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq unavailable")
	}
	for _, test := range []struct {
		json    string
		healthy bool
	}{
		{`{"status":"unhealthy","components":[{"status":"healthy"}]}`, false},
		{`{"status":"healthy","components":[{"status":"healthy"}]}`, true},
	} {
		command := exec.Command("jq", "-e", `.status == "healthy"`)
		command.Stdin = strings.NewReader(test.json)
		if err := command.Run(); (err == nil) != test.healthy {
			t.Fatalf("wrong health decision for %s: %v", test.json, err)
		}
	}
	if !strings.Contains(installCommand("registry", "marker", "192.0.2.10", "admin", "db", "robot"), `jq -e '.status == "healthy"'`) {
		t.Fatal("installer does not use overall health gate")
	}
}

func TestManagedImagesUseBundledArchiveWhenAvailable(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("KUBEPHOS_MANAGED_IMAGE_DIRECTORY", directory)
	if err := os.WriteFile(filepath.Join(directory, "cluster-lens-"+managedcomponents.ClusterLensVersion+".tar"), []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(managedComponentCopyCommands("192.0.2.10", "password", "marker"), "\n")
	if !strings.Contains(commands, "docker-archive:/tmp/kubephos-images-marker/cluster-lens-"+managedcomponents.ClusterLensVersion+".tar") {
		t.Fatal("bundled component still uses GHCR")
	}
	if !strings.Contains(commands, "docker://ghcr.io/unict-cclab/kubephos-mon-agent:"+managedcomponents.MonAgentVersion) {
		t.Fatal("unbundled component lost registry fallback")
	}
}
