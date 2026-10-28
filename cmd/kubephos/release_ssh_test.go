package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedSSHInstallerPreservesRecoveryAccess(t *testing.T) {
	directory := t.TempDir()
	sshDirectory := filepath.Join(directory, "ssh")
	os.Mkdir(sshDirectory, 0700)
	path := filepath.Join(sshDirectory, "authorized_keys")
	if err := os.WriteFile(path, []byte("ssh-ed25519 previous-recovery-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := strings.ReplaceAll(installSharedSSHKey, `$HOME/.ssh`, sshDirectory)
	for i := 0; i < 2; i++ {
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdin = strings.NewReader("ssh-ed25519 shared-key\n")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("installer failed: %v %s", err, output)
		}
	}
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(value), "shared-key") != 1 || !strings.Contains(string(value), "previous-recovery-key") {
		t.Fatal("installer lost recovery access or duplicated shared key")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("incorrect authorized_keys permissions")
	}
}
