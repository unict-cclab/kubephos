package pluginssh

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestCancellableCommandTerminatesProcessGroup(t *testing.T) {
	command := exec.Command("setsid", "bash", "-c", cancellableCommand("sleep 30"))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	})
	time.Sleep(100 * time.Millisecond)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- command.Wait() }()
	select {
	case <-completed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellable command left its process group running")
	}
}
