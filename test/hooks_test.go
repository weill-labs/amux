package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

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

	// Register on-idle hook that creates a marker file
	h.runCmd("set-hook", "on-idle", "touch "+marker)

	// Generate pane activity, then wait for idle (default 2s timeout)
	h.sendKeys("pane-1", "echo TRIGGER_ACTIVITY", "Enter")
	h.waitFor("pane-1", "TRIGGER_ACTIVITY")

	// Wait for idle timeout + hook execution
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // on-idle hook fired
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("on-idle hook did not fire within timeout")
}

func TestHookOnActivityFires(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "activity-fired")

	// Wait for initial idle state (pane starts, shell prompt appears, then goes idle)
	time.Sleep(3 * time.Second)

	// Register on-activity hook
	h.runCmd("set-hook", "on-activity", "touch "+marker)

	// Trigger activity by sending input that produces output
	h.sendKeys("pane-1", "echo TRIGGER", "Enter")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // on-activity hook fired
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("on-activity hook did not fire within timeout")
}

func TestHookReceivesEnvVars(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "hook-env")

	// Register on-idle hook that dumps env vars
	h.runCmd("set-hook", "on-idle", "env > "+envFile)

	// Generate activity then wait for idle
	h.sendKeys("pane-1", "echo ENV_TEST", "Enter")
	h.waitFor("pane-1", "ENV_TEST")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(envFile)
		if err == nil && len(data) > 0 {
			content := string(data)
			if !strings.Contains(content, "AMUX_PANE_ID=") {
				t.Errorf("missing AMUX_PANE_ID in hook env")
			}
			if !strings.Contains(content, "AMUX_PANE_NAME=pane-1") {
				t.Errorf("missing AMUX_PANE_NAME=pane-1 in hook env")
			}
			if !strings.Contains(content, "AMUX_EVENT=on-idle") {
				t.Errorf("missing AMUX_EVENT=on-idle in hook env")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("hook env output not written within timeout")
}

func TestHookFailingCommandLogsToSessionLog(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Register a hook with a command that will fail
	h.runCmd("set-hook", "on-idle", "/nonexistent/binary/xyz")

	// Trigger activity then wait for idle → hook fires and fails
	h.sendKeys("pane-1", "echo FAIL_TEST", "Enter")
	h.waitFor("pane-1", "FAIL_TEST")

	// Wait for idle timeout (2s) + hook execution + log flush
	logPath := filepath.Join(server.SocketDir(), h.session+".log")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(data), "hook") && strings.Contains(string(data), "failed") {
			return // error was logged to session log
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Read final log content for error message
	data, _ := os.ReadFile(logPath)
	t.Fatalf("expected hook failure in session log, got:\n%s", string(data))
}
