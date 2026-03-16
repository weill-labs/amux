package test

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (deterministic layout synchronization)
// ---------------------------------------------------------------------------

func TestSwapForward(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split to get 2 panes side-by-side: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	// Verify initial order: pane-1 left of pane-2
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position.X >= p2.Position.X {
		t.Fatalf("initial: pane-1 (x=%d) should be left of pane-2 (x=%d)", p1.Position.X, p2.Position.X)
	}

	// Swap forward: Ctrl-a } swaps active (pane-2) with next (wraps to pane-1)
	gen := h.generation()
	h.sendKeys("C-a", "}")
	h.waitLayout(gen)

	// After swap: pane-2 on left, pane-1 on right
	c = h.captureJSON()
	p1 = h.jsonPane(c, "pane-1")
	p2 = h.jsonPane(c, "pane-2")
	if p2.Position.X >= p1.Position.X {
		t.Errorf("swap forward: pane-2 (x=%d) should be left of pane-1 (x=%d)", p2.Position.X, p1.Position.X)
	}
}

func TestSwapBackward(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// 3 panes: pane-1 | pane-2 | pane-3 (active)
	h.splitV()
	h.splitV()

	// Swap backward: Ctrl-a { swaps active (pane-3) with previous (pane-2)
	gen := h.generation()
	h.sendKeys("C-a", "{")
	h.waitLayout(gen)

	// After swap: pane-1 | pane-3 | pane-2
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")
	if !(p1.Position.X < p3.Position.X && p3.Position.X < p2.Position.X) {
		t.Errorf("swap backward: expected pane-1 (x=%d) | pane-3 (x=%d) | pane-2 (x=%d)",
			p1.Position.X, p3.Position.X, p2.Position.X)
	}
}

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestSwapCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	out := h.runCmd("swap", "pane-1", "pane-2")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("swap command not recognized: %s", out)
	}

	// Swap is synchronous — capture immediately reflects the change.
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p2.Position.X >= p1.Position.X {
		t.Errorf("CLI swap: pane-2 (x=%d) should be left of pane-1 (x=%d)", p2.Position.X, p1.Position.X)
	}
}

func TestRotate(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// 3 panes: pane-1 | pane-2 | pane-3
	h.splitV()
	h.splitV()

	out := h.runCmd("rotate")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("rotate command not recognized: %s", out)
	}

	// Forward rotation: panes move forward through cells.
	// Cell 0 gets last pane (pane-3), cell 1 gets pane-1, cell 2 gets pane-2.
	// Result: pane-3 | pane-1 | pane-2
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")
	if !(p3.Position.X < p1.Position.X && p1.Position.X < p2.Position.X) {
		t.Errorf("rotate: expected pane-3 (x=%d) | pane-1 (x=%d) | pane-2 (x=%d)",
			p3.Position.X, p1.Position.X, p2.Position.X)
	}
}
