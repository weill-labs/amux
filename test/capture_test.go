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
