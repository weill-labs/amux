package test

import (
	"strings"
	"testing"
	"time"
)

func TestZoomToggle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	h.assertScreen("both panes visible before zoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Zoomed") {
		t.Errorf("zoom should confirm, got:\n%s", output)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("pane-1 should be visible and pane-2 hidden when zoomed\nScreen:\n%s", screen)
	}

	status := h.runCmd("status")
	if !strings.Contains(status, "zoomed") {
		t.Errorf("status should report zoomed state, got:\n%s", status)
	}

	output = h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Unzoomed") {
		t.Errorf("unzoom should confirm, got:\n%s", output)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after unzoom\nScreen:\n%s", screen)
	}
}

func TestZoomKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("C-a", "k")
	time.Sleep(300 * time.Millisecond)

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

func TestZoomSinglePaneFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "cannot zoom") {
		t.Errorf("zoom should fail with single pane, got:\n%s", output)
	}
}

func TestZoomKillZoomedPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)

	h.runCmd("zoom", "pane-2")
	h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	}, 3*time.Second)

	h.runCmd("kill", "pane-2")

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-3]") &&
			!strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("killing zoomed pane should unzoom and show remaining panes\nScreen:\n%s", screen)
	}

	status := h.runCmd("status")
	if strings.Contains(status, "zoomed") {
		t.Errorf("status should not report zoomed after kill, got:\n%s", status)
	}
}

func TestZoomAutoUnzoomOnSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

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

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

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
