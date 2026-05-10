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

func TestIsTestSessionAcceptsExpandedEntropy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session string
		want    bool
	}{
		{name: "legacy eight hex", session: "t-0123abcd", want: true},
		{name: "expanded sixteen hex", session: "t-0123456789abcdef", want: true},
		{name: "too short", session: "t-1234", want: false},
		{name: "non hex", session: "t-0123456789abcdeg", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isTestSession(tt.session); got != tt.want {
				t.Fatalf("isTestSession(%q) = %t, want %t", tt.session, got, tt.want)
			}
		})
	}
}

func TestParseAmuxServerProcessLineRequiresFullCommand(t *testing.T) {
	t.Parallel()

	pid, session, ok := parseAmuxServerProcessLine("498495 /tmp/amux-test/amux _server t-0123456789abcdef", isTestSession)
	if !ok {
		t.Fatal("parseAmuxServerProcessLine rejected a full amux server command")
	}
	if pid != "498495" || session != "t-0123456789abcdef" {
		t.Fatalf("parseAmuxServerProcessLine() = (%q, %q), want (498495, t-0123456789abcdef)", pid, session)
	}

	if _, _, ok := parseAmuxServerProcessLine("498495 amux", isTestSession); ok {
		t.Fatal("parseAmuxServerProcessLine accepted pgrep output without command arguments")
	}
}

func TestParseAmuxServerProcessLineAcceptsRemoteSessionSuffix(t *testing.T) {
	t.Parallel()

	pid, session, ok := parseAmuxServerProcessLine("2308581 /tmp/amux-test/amux _server t-01234567@hetzner-1", isTestSession)
	if !ok {
		t.Fatal("parseAmuxServerProcessLine rejected a remote test session")
	}
	if pid != "2308581" || session != "t-01234567" {
		t.Fatalf("parseAmuxServerProcessLine() = (%q, %q), want (2308581, t-01234567)", pid, session)
	}
}
