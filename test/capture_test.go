package test

import (
	"strings"
	"testing"
	"time"
)

func TestCapture(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "S", "C", "R", "E", "E", "N", "C", "A", "P", "Enter")
	h.waitFor("SCREENCAP", 3*time.Second)

	out := h.runCmd("capture")
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
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "O", "U", "T", "P", "U", "T", "M", "A", "R", "K", "E", "R", "Enter")
	h.waitFor("OUTPUTMARKER", 3*time.Second)

	output := h.runCmd("capture", "pane-1")
	if !strings.Contains(output, "OUTPUTMARKER") {
		t.Errorf("amux capture <pane> should contain OUTPUTMARKER, got:\n%s", output)
	}
}

func TestCapturePaneANSI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Write colored text so the pane has ANSI sequences
	h.sendKeys("e", "c", "h", "o", " ", "-", "e", " ",
		"'", "\\", "0", "3", "3", "[", "3", "1", "m", "R", "E", "D", "\\", "0", "3", "3", "[", "m", "'",
		"Enter")
	h.waitFor("RED", 3*time.Second)

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

func TestCaptureWithSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "L", "E", "F", "T", "P", "A", "N", "E", "Enter")
	h.waitFor("LEFTPANE", 3*time.Second)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("e", "c", "h", "o", " ", "R", "I", "G", "H", "T", "P", "A", "N", "E", "Enter")
	h.waitFor("RIGHTPANE", 3*time.Second)

	out := h.runCmd("capture")
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
