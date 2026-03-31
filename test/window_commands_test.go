package test

import (
	"strings"
	"testing"
)

func TestRenameWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("rename-window", "my-window")
	if !strings.Contains(out, "Renamed window to my-window") {
		t.Fatalf("unexpected output: %s", out)
	}

	// Verify via list-windows
	lw := h.runCmd("list-windows")
	if !strings.Contains(lw, "my-window") {
		t.Fatalf("list-windows should show renamed window, got:\n%s", lw)
	}
}

func TestSelectWindowByName(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create a second window with a name
	h.runCmd("new-window", "--name", "second")

	// Switch back to first window by index
	h.runCmd("select-window", "1")
	lw := h.runCmd("list-windows")
	if !strings.Contains(lw, "*1:") {
		t.Fatalf("should be on window 1, got:\n%s", lw)
	}

	// Switch to second window by name
	h.runCmd("select-window", "second")
	lw = h.runCmd("list-windows")
	if !strings.Contains(lw, "*2:") {
		t.Fatalf("should be on window 2, got:\n%s", lw)
	}
}

func TestSelectWindowNotFound(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("select-window", "nonexistent")
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not found error, got: %s", out)
	}
}

func TestLastWindowCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("new-window", "--name", "second")

	h.runCmd("last-window")
	lw := h.runCmd("list-windows")
	if !strings.Contains(lw, "*1:") {
		t.Fatalf("last-window should switch back to window 1, got:\n%s", lw)
	}

	h.runCmd("last-window")
	lw = h.runCmd("list-windows")
	if !strings.Contains(lw, "*2:") {
		t.Fatalf("second last-window should switch back to window 2, got:\n%s", lw)
	}
}

func TestLastWindowBellsWithoutPreviousWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("last-window")
	if !strings.Contains(out, "\a") {
		t.Fatalf("last-window without history should ring bell, got %q", out)
	}
}
