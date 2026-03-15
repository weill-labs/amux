package test

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Keybinding tests — TmuxHarness (requires client for prefix key processing)
// ---------------------------------------------------------------------------

func TestSwapForward(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split to get 2 panes side-by-side: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	// Verify initial order: pane-1 left of pane-2
	lines := h.captureAmuxContentLines()
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 < 0 || col2 < 0 || col1 >= col2 {
		t.Fatalf("initial: pane-1 (col %d) should be left of pane-2 (col %d)", col1, col2)
	}

	// Swap forward: Ctrl-a } swaps active (pane-2) with next (wraps to pane-1)
	h.sendKeys("C-a", "}")

	// After swap: pane-2 on left, pane-1 on right
	ok := h.waitForFunc(func(screen string) bool {
		ls := strings.Split(screen, "\n")
		c1 := paneNameCol(ls, "pane-1")
		c2 := paneNameCol(ls, "pane-2")
		return c1 >= 0 && c2 >= 0 && c2 < c1
	}, 3*time.Second)
	if !ok {
		lines = h.captureAmuxContentLines()
		t.Errorf("swap forward: expected pane-2 left of pane-1\n%s", strings.Join(lines, "\n"))
	}
}

func TestSwapBackward(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// 3 panes: pane-1 | pane-2 | pane-3 (active)
	h.splitV()
	h.splitV()

	// Swap backward: Ctrl-a { swaps active (pane-3) with previous (pane-2)
	h.sendKeys("C-a", "{")

	// After swap: pane-1 | pane-3 | pane-2
	ok := h.waitForFunc(func(screen string) bool {
		ls := strings.Split(screen, "\n")
		c1 := paneNameCol(ls, "pane-1")
		c2 := paneNameCol(ls, "pane-2")
		c3 := paneNameCol(ls, "pane-3")
		return c1 >= 0 && c2 >= 0 && c3 >= 0 && c1 < c3 && c3 < c2
	}, 3*time.Second)
	if !ok {
		lines := h.captureAmuxContentLines()
		t.Errorf("swap backward: expected pane-1|pane-3|pane-2\n%s", strings.Join(lines, "\n"))
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
	lines := h.captureContentLines()
	c1 := paneNameCol(lines, "pane-1")
	c2 := paneNameCol(lines, "pane-2")
	if c1 < 0 || c2 < 0 || c2 >= c1 {
		t.Errorf("CLI swap: expected pane-2 (col %d) left of pane-1 (col %d)\n%s",
			c2, c1, strings.Join(lines, "\n"))
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
	// Rotate is synchronous — capture immediately reflects the change.
	lines := h.captureContentLines()
	c1 := paneNameCol(lines, "pane-1")
	c2 := paneNameCol(lines, "pane-2")
	c3 := paneNameCol(lines, "pane-3")
	if c1 < 0 || c2 < 0 || c3 < 0 || c3 >= c1 || c1 >= c2 {
		t.Errorf("rotate: expected pane-3 (col %d) | pane-1 (col %d) | pane-2 (col %d)\n%s",
			c3, c1, c2, strings.Join(lines, "\n"))
	}
}
