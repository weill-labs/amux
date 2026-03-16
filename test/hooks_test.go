package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// waitForFile polls until path exists or timeout expires.
func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitForFileContent polls until path exists with non-empty content or timeout expires.
func waitForFileContent(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

func TestSetHookAndListHooks(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-hook", "on-idle", "echo idle")
	if strings.Contains(out, "error") || strings.Contains(out, "unknown") {
		t.Fatalf("set-hook failed: %s", out)
	}

	out = h.runCmd("set-hook", "on-activity", "echo active")
	if strings.Contains(out, "error") || strings.Contains(out, "unknown") {
		t.Fatalf("set-hook failed: %s", out)
	}

	out = h.runCmd("list-hooks")
	if !strings.Contains(out, "on-idle") {
		t.Errorf("list-hooks should show on-idle, got:\n%s", out)
	}
	if !strings.Contains(out, "echo idle") {
		t.Errorf("list-hooks should show command, got:\n%s", out)
	}
	if !strings.Contains(out, "on-activity") {
		t.Errorf("list-hooks should show on-activity, got:\n%s", out)
	}
}

func TestUnsetHook(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-hook", "on-idle", "echo a")
	h.runCmd("set-hook", "on-idle", "echo b")

	out := h.runCmd("unset-hook", "on-idle", "0")
	if strings.Contains(out, "error") {
		t.Fatalf("unset-hook failed: %s", out)
	}

	out = h.runCmd("list-hooks")
	if strings.Contains(out, "echo a") {
		t.Errorf("echo a should have been removed, got:\n%s", out)
	}
	if !strings.Contains(out, "echo b") {
		t.Errorf("echo b should remain, got:\n%s", out)
	}
}

func TestUnsetHookAll(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-hook", "on-idle", "echo a")
	h.runCmd("set-hook", "on-idle", "echo b")
	h.runCmd("set-hook", "on-activity", "echo c")

	h.runCmd("unset-hook", "on-idle")

	out := h.runCmd("list-hooks")
	if strings.Contains(out, "on-idle") {
		t.Errorf("all on-idle hooks should be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "on-activity") {
		t.Errorf("on-activity should remain, got:\n%s", out)
	}
}

func TestSetHookInvalidEvent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-hook", "invalid-event", "echo test")
	if !strings.Contains(out, "unknown hook event") {
		t.Errorf("expected error for invalid event, got:\n%s", out)
	}
}

func TestHookOnIdleFires(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "idle-fired")

	h.runCmd("set-hook", "on-idle", "touch "+marker)

	h.sendKeys("pane-1", "echo TRIGGER_ACTIVITY", "Enter")
	h.waitFor("pane-1", "TRIGGER_ACTIVITY")

	if !waitForFile(t, marker, 5*time.Second) {
		t.Fatal("on-idle hook did not fire within timeout")
	}
}

func TestHookOnActivityFires(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "activity-fired")

	// Wait for initial idle state (shell prompt appears, then quiet period expires)
	h.waitIdle("pane-1")

	h.runCmd("set-hook", "on-activity", "touch "+marker)

	h.sendKeys("pane-1", "echo TRIGGER", "Enter")

	if !waitForFile(t, marker, 5*time.Second) {
		t.Fatal("on-activity hook did not fire within timeout")
	}
}

func TestHookReceivesEnvVars(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "hook-env")

	h.runCmd("set-hook", "on-idle", "env > "+envFile)

	h.sendKeys("pane-1", "echo ENV_TEST", "Enter")
	h.waitFor("pane-1", "ENV_TEST")

	content := waitForFileContent(t, envFile, 5*time.Second)
	if content == "" {
		t.Fatal("hook env output not written within timeout")
	}
	if !strings.Contains(content, "AMUX_PANE_ID=") {
		t.Errorf("missing AMUX_PANE_ID in hook env")
	}
	if !strings.Contains(content, "AMUX_PANE_NAME=pane-1") {
		t.Errorf("missing AMUX_PANE_NAME=pane-1 in hook env")
	}
	if !strings.Contains(content, "AMUX_EVENT=on-idle") {
		t.Errorf("missing AMUX_EVENT=on-idle in hook env")
	}
}

func TestHookFailingCommandLogsToSessionLog(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-hook", "on-idle", "/nonexistent/binary/xyz")

	h.sendKeys("pane-1", "echo FAIL_TEST", "Enter")
	h.waitFor("pane-1", "FAIL_TEST")

	logPath := filepath.Join(server.SocketDir(), h.session+".log")
	content := waitForFileContent(t, logPath, 5*time.Second)
	if !strings.Contains(content, "hook") || !strings.Contains(content, "failed") {
		t.Fatalf("expected hook failure in session log, got:\n%s", content)
	}
}
