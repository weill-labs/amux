package test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

func TestIdleStatus_ShellAtPrompt(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// A freshly spawned pane with shell at prompt should be idle.
	out := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if !pane.Idle {
		t.Error("pane at shell prompt should be idle")
	}
}

func TestIdleStatus_BusyWhileRunning(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "sleep 30", "Enter")
	h.waitBusy("pane-1")

	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse full JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" {
			if p.Idle {
				t.Error("pane running 'sleep 30' should be busy (not idle)")
			}
			return
		}
	}
	t.Error("pane-1 not found in capture")
}

func TestIdleStatus_BusyWithMultiplePanes(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)

	h.splitV()

	h.sendKeys("pane-1", "sleep 30", "Enter")
	h.waitBusy("pane-1")

	capture := captureJSONRetrying(t, h.runCmd)

	if len(capture.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(capture.Panes))
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" && p.Idle {
			t.Error("pane-1 running 'sleep 30' should be busy")
		}
	}

	stopLongRunningCommand(t, h, "pane-1")
}

func TestWaitBusy_EventBased(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)
	t.Cleanup(func() {
		shutdownSinglePaneSession(t, h)
	})

	// Wait for pane to become idle first so wait-busy has something to wait for.
	h.sendKeys("pane-1", "echo INIT", "Enter")
	h.waitFor("pane-1", "INIT")
	out := h.runCmd("wait-idle", "pane-1", "--timeout", "20s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("wait-idle pane-1: %s", strings.TrimSpace(out))
	}

	// wait-busy should block until a real child exists, not just prompt echo.
	h.sendKeys("pane-1", "sleep 300", "Enter")
	out = h.runCmd("wait-busy", "pane-1", "--timeout", "15s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("wait-busy pane-1: %s", strings.TrimSpace(out))
	}

	stopLongRunningCommand(t, h, "pane-1")
}

func TestWaitBusy_WaitsForChildProcessNotPromptEcho(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo READY", "Enter")
	h.waitFor("pane-1", "READY")
	h.waitIdle("pane-1")

	h.sendKeys("pane-1", "sleep 300", "Enter")
	h.waitBusy("pane-1")

	pane := captureJSONPane(t, h, "pane-1")
	if pane.Idle {
		t.Error("pane should be busy after waitBusy returns")
	}
	if len(pane.ChildPIDs) == 0 {
		t.Error("waitBusy should not return on prompt echo alone")
	}
}

func TestWaitIdle_EventBased(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Generate activity, then wait for idle.
	h.sendKeys("pane-1", "echo ACTIVITY", "Enter")
	h.waitFor("pane-1", "ACTIVITY")
	h.waitIdle("pane-1")

	// Verify via JSON capture — should report idle with shell name.
	pane := captureJSONPane(t, h, "pane-1")
	if !pane.Idle {
		t.Error("pane should be idle after waitIdle returns")
	}
	if pane.CurrentCommand == "" {
		t.Error("idle pane should report shell name as current_command")
	}
	// ShellName() extracts from cmd.Path — should be bash/zsh/etc.
	if !strings.Contains(pane.CurrentCommand, "sh") {
		t.Errorf("expected shell name containing 'sh', got %q", pane.CurrentCommand)
	}
}

func TestWaitBusy_AlreadyBusy(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Start a command before calling wait-busy — should return immediately.
	h.startLongSleep("pane-1")

	out := h.runCmd("wait-busy", "pane-1", "--timeout", "1s")
	if strings.Contains(out, "timeout") {
		t.Error("wait-busy should return immediately when pane is already busy")
	}
}

func TestWaitIdle_AlreadyIdle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for idle, then call wait-idle again — should return immediately.
	h.sendKeys("pane-1", "echo READY", "Enter")
	h.waitFor("pane-1", "READY")
	h.waitIdle("pane-1")

	out := h.runCmd("wait-idle", "pane-1", "--timeout", "1s")
	if strings.Contains(out, "timeout") {
		t.Error("wait-idle should return immediately when pane is already idle")
	}
}

func TestWaitIdle_DoesNotTreatQuietBusyPaneAsIdle(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)
	t.Cleanup(func() {
		shutdownSinglePaneSession(t, h)
	})

	h.startLongSleep("pane-1")

	time.Sleep(server.DefaultIdleTimeout + time.Second)

	out := h.runCmd("wait-busy", "pane-1", "--timeout", "1s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("quiet pane should still be busy after the idle window, got: %s", strings.TrimSpace(out))
	}

	stopLongRunningCommand(t, h, "pane-1")
}
