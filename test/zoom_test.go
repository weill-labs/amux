package test

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestZoomToggle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	h.assertScreen("both panes visible before zoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Zoomed") {
		t.Errorf("zoom should confirm, got:\n%s", output)
	}

	h.assertScreen("pane-1 should be visible and pane-2 hidden when zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	status := h.runCmd("status")
	if !strings.Contains(status, "zoomed") {
		t.Errorf("status should report zoomed state, got:\n%s", status)
	}

	output = h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Unzoomed") {
		t.Errorf("unzoom should confirm, got:\n%s", output)
	}

	h.assertScreen("both panes should be visible after unzoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestZoomSinglePaneFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "cannot zoom") {
		t.Errorf("zoom should fail with single pane, got:\n%s", output)
	}
}

func TestZoomKillZoomedPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.splitH()

	h.runCmd("zoom", "pane-2")
	h.assertScreen("pane-2 zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	h.runCmd("kill", "pane-2")

	h.assertScreen("killing zoomed pane should unzoom and show remaining panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-3]") &&
			!strings.Contains(s, "[pane-2]")
	})

	status := h.runCmd("status")
	if strings.Contains(status, "zoomed") {
		t.Errorf("status should not report zoomed after kill, got:\n%s", status)
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — TmuxHarness (requires client for prefix key processing)
// ---------------------------------------------------------------------------

func TestZoomKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.splitH()

	h.sendKeys("C-a", "k")
	time.Sleep(400 * time.Millisecond)

	h.sendKeys("C-a", "z")

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("Ctrl-a z should zoom the active pane\nScreen:\n%s", screen)
	}

	h.sendKeys("C-a", "z")

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("Ctrl-a z should toggle unzoom\nScreen:\n%s", screen)
	}
}

func TestZoomAutoUnzoomOnSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.splitH()

	h.runCmd("zoom", "pane-1")
	h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	}, 3*time.Second)

	h.sendKeys("C-a", "-")
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]") &&
			strings.Contains(s, "[pane-3]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("split while zoomed should auto-unzoom and show all panes\nScreen:\n%s", screen)
	}
}

func TestZoomAutoUnzoomOnFocus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.splitH()

	h.sendKeys("C-a", "z")
	h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	}, 3*time.Second)

	h.sendKeys("C-a", "k")
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("focus while zoomed should auto-unzoom\nScreen:\n%s", screen)
	}
}
