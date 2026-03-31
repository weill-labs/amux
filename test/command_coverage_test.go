package test

import (
	"strings"
	"testing"
	"time"
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

func TestSpawnWithoutNameAutoNamesPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("spawn")
	if !strings.Contains(out, "Spawned pane-2") {
		t.Fatalf("expected auto-named spawn, got: %s", out)
	}
}

func TestKillLastPaneExitsSession(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("kill", "pane-1")
	if !strings.Contains(out, "session exiting") {
		t.Fatalf("expected session exit message, got: %s", out)
	}
}

func TestKillCleanupLastPaneExitsSession(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "trap 'sleep 0.3; exit 0' TERM; while :; do sleep 1; done", "Enter")
	h.waitBusy("pane-1")

	out := h.runCmd("kill", "--cleanup", "--timeout", "100ms", "pane-1")
	if !strings.Contains(out, "Cleaning up pane-1") {
		t.Fatalf("expected cleanup confirmation, got: %s", out)
	}

	h.waitForShutdownSignal(5 * time.Second)
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
	h.runCmd("spawn", "--at", "pane-1")

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

	h.runCmd("spawn", "--at", "pane-1")
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

func TestRemovedBuiltInCommandsAreUnknown(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	for _, cmd := range []string{"minimize", "restore", "delegate", "split-focus", "add-pane-focus", "spawn-focus"} {
		out := h.runCmd(cmd, "pane-1")
		if !strings.Contains(out, "unknown command") {
			t.Fatalf("%s should be unknown, got: %s", cmd, out)
		}
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

func TestSwapUsageWithTreeFlag(t *testing.T) {
	h := newServerHarness(t)

	out := h.runCmd("swap", "pane-1", "--tree")
	if !strings.Contains(out, "usage: amux swap <pane1> <pane2> [--tree] | amux swap forward | amux swap backward") {
		t.Fatalf("expected swap usage error, got: %s", out)
	}
}

func TestMoveUsage(t *testing.T) {
	h := newServerHarness(t)

	out := h.runCmd("move", "pane-1", "--before")
	if !strings.Contains(out, "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)") {
		t.Fatalf("expected move usage error, got: %s", out)
	}
}

func TestMoveRejectsConflictingFlags(t *testing.T) {
	h := newServerHarness(t)

	h.splitV()

	out := h.runCmd("move", "pane-1", "--before", "pane-2", "--after", "pane-2")
	if !strings.Contains(out, "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)") {
		t.Fatalf("expected move parser usage error, got: %s", out)
	}
}

func TestMoveToColumnUsage(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("move", "pane-1", "--to-column")
	if !strings.Contains(out, "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)") {
		t.Fatalf("expected move usage error, got: %s", out)
	}
}
