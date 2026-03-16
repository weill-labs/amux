package test

import (
	"strings"
	"testing"
	"time"
)

func TestCapture(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo SCREENCAP", "Enter")
	h.waitFor("pane-1", "SCREENCAP")

	out := h.capture()
	if !strings.Contains(out, "SCREENCAP") {
		t.Errorf("amux capture should contain typed text, got:\n%s", out)
	}
	if !strings.Contains(out, "[pane-") {
		t.Errorf("amux capture should contain pane status, got:\n%s", out)
	}
	if !strings.Contains(out, "amux") {
		t.Errorf("amux capture should contain global bar, got:\n%s", out)
	}
}

func TestCapturePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo OUTPUTMARKER", "Enter")
	h.waitFor("pane-1", "OUTPUTMARKER")

	output := h.runCmd("capture", "pane-1")
	if !strings.Contains(output, "OUTPUTMARKER") {
		t.Errorf("amux capture <pane> should contain OUTPUTMARKER, got:\n%s", output)
	}
}

func TestCapturePaneANSI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Write colored text so the pane has ANSI sequences.
	// Split the done-marker across two printf calls so it only appears as a
	// contiguous string in the OUTPUT, not in the echoed command text.
	h.sendKeys("pane-1", `printf '\033[31mRED\033[m\n' && printf COL; printf 'DONE\n'`, "Enter")
	h.waitFor("pane-1", "COLDONE")

	// Per-pane capture without --ansi should be plain text
	plain := h.runCmd("capture", "pane-1")
	if strings.Contains(plain, "\033[") {
		t.Errorf("capture pane without --ansi should be plain text, got ANSI escapes:\n%s", plain)
	}
	if !strings.Contains(plain, "RED") {
		t.Errorf("capture pane should contain RED, got:\n%s", plain)
	}

	// Per-pane capture with --ansi should preserve ANSI sequences
	ansi := h.runCmd("capture", "--ansi", "pane-1")
	if !strings.Contains(ansi, "\033[") {
		t.Errorf("capture pane --ansi should contain ANSI escapes, got:\n%s", ansi)
	}
	if !strings.Contains(ansi, "RED") {
		t.Errorf("capture pane --ansi should contain RED, got:\n%s", ansi)
	}
}

func TestCursorBlockOnlyInActivePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Split so we have two panes with shell prompts
	h.splitV()

	// Focus pane-2 — pane-1 becomes inactive.
	// Use per-pane --ansi capture (returns emulator Render() output)
	// to check each pane independently, avoiding false positives from
	// the compositor's own ANSI sequences or shell prompt styling.
	h.runCmd("focus", "pane-2")

	inactive := h.runCmd("capture", "--ansi", "pane-1")
	if strings.Contains(inactive, "\033[7m") {
		t.Errorf("inactive pane should have no reverse-video cursor blocks, got:\n%s", inactive)
	}
}

func TestCaptureIdleIndicator(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Split so pane-1 becomes inactive (pane-2 gets focus)
	h.splitV()

	// Wait for pane-1 to be idle (no child processes), then poll for the
	// idle timer to fire (DefaultIdleTimeout = 2s) which sets the idle
	// state tracked by the server and shows the ◇ indicator.
	h.waitIdle("pane-1")
	h.waitFor("pane-2", "$")

	deadline := time.Now().Add(5 * time.Second)
	for {
		out := h.capture()
		if strings.Contains(out, "◇") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture should show idle diamond indicator for inactive idle pane, got:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCaptureWithSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo LEFTPANE", "Enter")
	h.waitFor("pane-1", "LEFTPANE")

	h.splitV()
	h.sendKeys("pane-2", "echo RIGHTPANE", "Enter")
	h.waitFor("pane-2", "RIGHTPANE")

	out := h.capture()
	if !strings.Contains(out, "LEFTPANE") {
		t.Errorf("amux capture should contain left pane text, got:\n%s", out)
	}
	if !strings.Contains(out, "RIGHTPANE") {
		t.Errorf("amux capture should contain right pane text, got:\n%s", out)
	}
	if !strings.Contains(out, "[pane-1]") || !strings.Contains(out, "[pane-2]") {
		t.Errorf("amux capture should contain both pane names, got:\n%s", out)
	}
}
