package test

import (
	"strings"
	"testing"
)

func TestSpawnLocalPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("spawn", "--name", "worker-1", "--task", "build")
	if !strings.Contains(out, "Spawned worker-1") {
		t.Fatalf("unexpected spawn output: %s", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "worker-1") {
		t.Fatalf("list should show spawned pane, got:\n%s", list)
	}
	if !strings.Contains(list, "build") {
		t.Fatalf("list should show task, got:\n%s", list)
	}
}

func TestSpawnRequiresName(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("spawn")
	if !strings.Contains(out, "--name is required") {
		t.Fatalf("expected --name error, got: %s", out)
	}
}

func TestKillLastPaneError(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("kill", "pane-1")
	if !strings.Contains(out, "cannot kill last pane") {
		t.Fatalf("expected last pane error, got: %s", out)
	}
}

func TestResizeWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("resize-window", "120", "40")
	if !strings.Contains(out, "Resized to 120x40") {
		t.Fatalf("unexpected resize output: %s", out)
	}
}

func TestResizeWindowInvalidArgs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("resize-window", "abc", "40")
	if !strings.Contains(out, "invalid") {
		t.Fatalf("expected invalid dimensions error, got: %s", out)
	}
}

func TestZoomUnzoom(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Need 2 panes to zoom
	h.runCmd("split")

	out := h.runCmd("zoom")
	if !strings.Contains(out, "Zoomed") {
		t.Fatalf("expected Zoomed, got: %s", out)
	}

	out = h.runCmd("zoom")
	if !strings.Contains(out, "Unzoomed") {
		t.Fatalf("expected Unzoomed, got: %s", out)
	}
}

func TestZoomByName(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("split")
	h.runCmd("focus", "pane-1")

	out := h.runCmd("zoom", "pane-2")
	if !strings.Contains(out, "Zoomed pane-2") {
		t.Fatalf("expected Zoomed pane-2, got: %s", out)
	}
}

func TestZoomNotFound(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("zoom", "nonexistent")
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not found error, got: %s", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("totally-bogus-command")
	if !strings.Contains(out, "unknown command") {
		t.Fatalf("expected unknown command error, got: %s", out)
	}
}

func TestReloadServer(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("reload-server")
	if !strings.Contains(out, "reloading") {
		t.Fatalf("expected reloading message, got: %s", out)
	}

	// After reload, the session should still be accessible
	h.waitFor("pane-1", "$")
	list := h.runCmd("list")
	if !strings.Contains(list, "pane-1") {
		t.Fatalf("pane-1 should survive reload, got:\n%s", list)
	}
}
