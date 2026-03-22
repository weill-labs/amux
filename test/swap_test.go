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

func TestSwapTreeCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("focus", "pane-1")
	h.splitH()

	out := h.runCmd("swap-tree", "pane-1", "pane-2")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("swap-tree command not recognized: %s", out)
	}

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	if !(p2.Position.X < p1.Position.X && p2.Position.X < p3.Position.X) {
		t.Fatalf("swap-tree should move full-height pane-2 left of pane-1/pane-3: p1=%+v p2=%+v p3=%+v", p1.Position, p2.Position, p3.Position)
	}
	if p1.Position.X != p3.Position.X {
		t.Fatalf("swap-tree should keep pane-1 and pane-3 in the same moved column: p1=%+v p3=%+v", p1.Position, p3.Position)
	}
	if p1.Position.Y >= p3.Position.Y {
		t.Fatalf("swap-tree should preserve stacked order in moved column: p1=%+v p3=%+v", p1.Position, p3.Position)
	}
}

func TestSwapTreeCLIRejectsSameRootBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("focus", "pane-1")
	h.splitH()

	out := h.runCmd("swap-tree", "pane-1", "pane-3")
	if !strings.Contains(out, "same root-level group") {
		t.Fatalf("expected same-root-group error, got: %s", out)
	}
}

func TestMoveBeforeCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitV()
	h.runCmd("focus", "pane-1")
	h.splitH()

	if out := h.runCmd("resize-pane", "pane-1", "right", "6"); !strings.Contains(out, "Resized") {
		t.Fatalf("expected resize confirmation, got: %s", out)
	}

	before := h.captureJSON()
	beforePane1 := h.jsonPane(before, "pane-1")
	beforePane3 := h.jsonPane(before, "pane-3")
	beforePane4 := h.jsonPane(before, "pane-4")

	out := h.runCmd("move", "pane-3", "--before", "pane-1")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("move command not recognized: %s", out)
	}

	after := h.captureJSON()
	p1 := h.jsonPane(after, "pane-1")
	p2 := h.jsonPane(after, "pane-2")
	p3 := h.jsonPane(after, "pane-3")
	p4 := h.jsonPane(after, "pane-4")

	if !(p3.Position.X < p1.Position.X && p1.Position.X < p2.Position.X) {
		t.Fatalf("move before should reorder root branches to pane-3 | pane-1 subtree | pane-2: p1=%+v p2=%+v p3=%+v", p1.Position, p2.Position, p3.Position)
	}
	if p1.Position.X != p4.Position.X {
		t.Fatalf("move before should keep pane-1 and pane-4 stacked in one moved branch: p1=%+v p4=%+v", p1.Position, p4.Position)
	}
	if p1.Position.Width != beforePane1.Position.Width || p4.Position.Width != beforePane4.Position.Width {
		t.Fatalf("move before should preserve moved subtree width: before p1=%+v p4=%+v after p1=%+v p4=%+v", beforePane1.Position, beforePane4.Position, p1.Position, p4.Position)
	}
	if p3.Position.Width != beforePane3.Position.Width {
		t.Fatalf("move before should preserve pane-3 branch width: before=%+v after=%+v", beforePane3.Position, p3.Position)
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
