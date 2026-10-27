package k3s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeWatchLimitsPersistMinimumAndPreserveHigherSettings(t *testing.T) {
	for _, test := range []struct{ name, instances, watches, want string }{{"raise defaults", "128", "8192", "fs.inotify.max_user_instances = 8192\nfs.inotify.max_user_watches = 1048576\n"}, {"preserve larger limits", "16384", "2097152", "fs.inotify.max_user_instances = 16384\nfs.inotify.max_user_watches = 2097152\n"}} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			profile := filepath.Join(dir, "node limits.conf")
			sudo := []byte("#!/bin/sh\n[ \"$1\" = -n ] && shift\nexec \"$@\"\n")
			sysctl := []byte("#!/bin/sh\nset -eu\nif [ \"$1\" = -p ]; then test -f \"$2\"; exit; fi\ncase \"$2\" in fs.inotify.max_user_instances) echo \"$TEST_INSTANCES\";; fs.inotify.max_user_watches) echo \"$TEST_WATCHES\";; *) exit 1;; esac\n")
			for name, body := range map[string][]byte{"sudo": sudo, "sysctl": sysctl} {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				command := exec.Command("sh", "-c", nodeWatchLimitsCommand(profile))
				command.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TEST_INSTANCES="+test.instances, "TEST_WATCHES="+test.watches)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("node preparation failed: %v %s", err, output)
				}
				raw, err := os.ReadFile(profile)
				if err != nil || string(raw) != test.want {
					t.Fatalf("unexpected persistent limits: %s %v", raw, err)
				}
			}
		})
	}
}

func TestNodeWatchLimitsApplyBeforeKubernetesInstallation(t *testing.T) {
	command := installCommand("agent", "node", "https://192.0.2.1:6443", "test-token", false)
	if !strings.Contains(command, "99-kubephos-inotify.conf") || strings.Index(command, "max_user_instances") > strings.Index(command, "https://get.k3s.io") {
		t.Fatal("Kubernetes installation does not prepare node watcher limits first")
	}
}
