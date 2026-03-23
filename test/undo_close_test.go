package test

import (
	"strings"
	"testing"
)

func TestUndoClosePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create a second pane.
	h.splitV()

	// Kill pane-2.
	output := h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Fatalf("kill should confirm, got:\n%s", output)
	}

	// pane-2 should be gone from the layout.
	h.assertScreen("pane-2 should be gone after kill", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	// Undo should restore pane-2.
	output = h.runCmd("undo")
	if !strings.Contains(output, "pane-2") {
		t.Fatalf("undo should report restored pane name, got:\n%s", output)
	}

	// pane-2 should be back in the layout.
	h.assertScreen("pane-2 should be back after undo", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	// pane-2 should appear in the list.
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-2") {
		t.Errorf("list should contain pane-2 after undo, got:\n%s", listOut)
	}
}

func TestUndoCloseNoPendingPanes(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Undo with no closed panes should return an error.
	output := h.runCmd("undo")
	if !strings.Contains(output, "no closed pane") {
		t.Errorf("undo with nothing to undo should error, got:\n%s", output)
	}
}

func TestUndoCloseMultiplePanes(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create pane-2 and pane-3.
	h.splitV()
	h.splitV()

	// Kill pane-3 then pane-2 (stack order: pane-2 on top, pane-3 below).
	h.runCmd("kill", "pane-3")
	h.runCmd("kill", "pane-2")

	// First undo should restore pane-2 (most recent).
	output := h.runCmd("undo")
	if !strings.Contains(output, "pane-2") {
		t.Fatalf("first undo should restore pane-2, got:\n%s", output)
	}
	h.assertScreen("pane-2 restored", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})

	// Second undo should restore pane-3.
	output = h.runCmd("undo")
	if !strings.Contains(output, "pane-3") {
		t.Fatalf("second undo should restore pane-3, got:\n%s", output)
	}
	h.assertScreen("pane-3 restored", func(s string) bool {
		return strings.Contains(s, "[pane-3]")
	})
}

func TestUndoCloseLastPaneNotUndoable(t *testing.T) {
	t.Parallel()
	h := newServerHarnessPersistent(t)

	h.splitV()

	// Kill pane-1 (not the last pane — pane-2 remains).
	h.runCmd("kill", "pane-1")

	// Undo should work.
	output := h.runCmd("undo")
	if !strings.Contains(output, "pane-1") {
		t.Fatalf("undo should restore pane-1, got:\n%s", output)
	}
}

func TestUndoCloseRestoredPaneIsActive(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Focus pane-1, then kill pane-2.
	h.runCmd("focus", "pane-1")
	h.runCmd("kill", "pane-2")

	// Undo should restore pane-2 and make it active.
	h.runCmd("undo")

	c := h.captureJSON()
	p2 := h.jsonPane(c, "pane-2")
	if !p2.Active {
		t.Errorf("restored pane-2 should be active, got active=%v", p2.Active)
	}
}
