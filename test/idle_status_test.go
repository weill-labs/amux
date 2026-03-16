package test

import (
	"encoding/json"
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
