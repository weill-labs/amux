package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHasOtherActiveTestRunIgnoresCurrentProcessLock(t *testing.T) {
	t.Parallel()

	socketDir := t.TempDir()
	if _, err := writeTestRunLock(socketDir, os.Getpid()); err != nil {
		t.Fatalf("writeTestRunLock(current): %v", err)
	}

	if hasOtherActiveTestRun(socketDir, os.Getpid()) {
		t.Fatal("hasOtherActiveTestRun reported current process as another active test run")
	}
}

func TestHasOtherActiveTestRunDetectsLiveOtherProcess(t *testing.T) {
	t.Parallel()

	socketDir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	if _, err := writeTestRunLock(socketDir, cmd.Process.Pid); err != nil {
		t.Fatalf("writeTestRunLock(other): %v", err)
	}

	if !hasOtherActiveTestRun(socketDir, os.Getpid()) {
		t.Fatal("hasOtherActiveTestRun did not detect the live other test process")
	}
}

func TestHasOtherActiveTestRunRemovesStaleLocks(t *testing.T) {
	t.Parallel()

	socketDir := t.TempDir()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived process: %v", err)
	}
	stalePID := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait short-lived process: %v", err)
	}

	lockPath, err := writeTestRunLock(socketDir, stalePID)
	if err != nil {
		t.Fatalf("writeTestRunLock(stale): %v", err)
	}

	if hasOtherActiveTestRun(socketDir, os.Getpid()) {
		t.Fatal("hasOtherActiveTestRun reported a stale lock as live")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale lock %s should be removed, stat err=%v", filepath.Base(lockPath), err)
	}
}
