package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
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

	// Background sleep first, then echo sentinel. The sentinel only prints
	// after the shell has forked the child, so pgrep will see it.
	h.sendKeys("pane-1", `sleep 30 & printf '\x42\x55\x53\x59_OK\n'; wait`, "Enter")
	h.waitFor("pane-1", "BUSY_OK")

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
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Make pane-1 busy. Background sleep first, then echo sentinel.
	h.sendKeys("pane-1", `sleep 30 & printf '\x42\x55\x53\x59_OK\n'; wait`, "Enter")
	h.waitFor("pane-1", "BUSY_OK")

	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if len(capture.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(capture.Panes))
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" && p.Idle {
			t.Error("pane-1 running 'sleep 30' should be busy")
		}
	}
}

func TestWaitBusy_EventBased(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for pane to become idle first so wait-busy has something to wait for.
	h.sendKeys("pane-1", "echo INIT", "Enter")
	h.waitFor("pane-1", "INIT")
	h.waitIdle("pane-1")

	// Start a command and use waitBusy to detect it.
	h.sendKeys("pane-1", "sleep 300", "Enter")
	h.waitBusy("pane-1")

	// Verify the pane is indeed busy via JSON capture.
	pane := captureJSONPane(t, h, "pane-1")
	if pane.Idle {
		t.Error("pane should be busy after waitBusy returns")
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
