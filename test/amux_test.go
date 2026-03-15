package test

import (
	"strings"
	"testing"
	"time"
)

func TestBasicStartAndDetach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Verify layout via amux capture (server-side compositor)
	lines := h.captureAmuxLines()
	if len(lines) == 0 || !strings.Contains(lines[0], "[pane-") {
		t.Errorf("capture: first row should contain pane status, got: %q", lines[0])
	}

	// Global bar should be on the last non-empty row
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastNonEmpty = lines[i]
			break
		}
	}
	if !isGlobalBar(lastNonEmpty) {
		t.Errorf("capture: last row should be global bar, got: %q", lastNonEmpty)
	}

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)
}

func TestNamedSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.assertScreen("session name in status bar", func(s string) bool {
		return strings.Contains(s, h.session)
	})

	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list with -s should work, got:\n%s", output)
	}
}

func TestCtrlACtrlA(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "C-a")
	time.Sleep(300 * time.Millisecond)

	h.assertScreen("amux still running after Ctrl-a Ctrl-a", func(s string) bool {
		return strings.Contains(s, "[pane-") && strings.Contains(s, "amux")
	})
}

func TestReattach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "H", "E", "L", "L", "O", "Enter")
	h.waitFor("HELLO", 3*time.Second)

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)

	h.sendKeys(amuxBin, " -s ", h.session, "Enter")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("reattach failed, screen:\n%s", screen)
	}

	h.assertScreen("HELLO and status bar after reattach", func(s string) bool {
		return strings.Contains(s, "HELLO") && strings.Contains(s, "[pane-")
	})
}

func TestList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list should contain pane-1, got:\n%s", output)
	}
	if !strings.Contains(output, "pane-2") {
		t.Errorf("list should contain pane-2, got:\n%s", output)
	}
	if !strings.Contains(output, "*") {
		t.Errorf("list should mark active pane with *, got:\n%s", output)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("status")
	if !strings.Contains(output, "1 total") {
		t.Errorf("status should show 1 total, got:\n%s", output)
	}
}

func TestGoldenSinglePane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	frame := extractFrame(h.captureAmux(), h.session)
	assertGolden(t, "single_pane.golden", frame)
}
