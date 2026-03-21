package test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestFocusByName(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	h.assertActive("pane-2")

	output := h.doFocus("pane-1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus should confirm, got:\n%s", output)
	}

	h.assertActive("pane-1")
	h.assertInactive("pane-2")
}

func TestFocusByID(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	output := h.doFocus("1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus by ID should confirm, got:\n%s", output)
	}

	h.assertActive("pane-1")
}

func TestFocusResyncsStaleCursorState(t *testing.T) {
	t.Parallel()
	h := newServerHarnessWithSize(t, 255, 62)

	h.splitV()
	h.splitH()
	h.waitForPaneContent("pane-2", "$", 10*time.Second)

	healthyCapture := h.captureJSON()
	healthy := h.jsonPane(healthyCapture, "pane-2")

	// Simulate stale client-side cursor state for the inactive top-right pane.
	h.client.renderer.HandlePaneOutput(2, []byte("\033[1;24H"))

	var before proto.CapturePane
	if err := json.Unmarshal([]byte(h.client.renderer.CapturePaneJSON(2, nil)), &before); err != nil {
		t.Fatalf("unmarshal pane-2 before focus: %v", err)
	}
	if got := before.Cursor.Col; got != 23 {
		t.Fatalf("precondition failed: pane-2 cursor col = %d, want 23", got)
	}

	h.doFocus("pane-2")

	afterCapture := h.captureJSON()
	after := h.jsonPane(afterCapture, "pane-2")
	if got, want := after.Content[0], healthy.Content[0]; got != want {
		t.Fatalf("pane-2 content after focus = %q, want %q", got, want)
	}
	if got, want := after.Cursor.Col, healthy.Cursor.Col; got != want {
		t.Fatalf("pane-2 cursor col after focus = %d, want %d", got, want)
	}
}

func TestFocusNotFound(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("focus", "nonexistent")
	if !strings.Contains(output, "not found") {
		t.Errorf("focus of nonexistent pane should report error, got:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (inner amux inside outer server)
// ---------------------------------------------------------------------------

func TestFocusCycle(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	h.assertActive("pane-2")

	gen := h.generation()
	h.sendKeys("C-a", "o")
	h.waitLayout(gen)

	h.assertActive("pane-1")
	h.assertInactive("pane-2")
}

func TestFocusNavigationThreePanes(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()
	h.splitH()

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		gen := h.generation()
		h.sendKeys("C-a", "o")
		h.waitLayout(gen)
		seen[h.activePaneName()] = true
	}

	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if !seen[name] {
			t.Errorf("focus cycle never reached %s (saw: %v)", name, seen)
		}
	}
}

func TestDirectionalFocusAfterRootSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Create: pane-1 top-left, pane-2 bottom-left, pane-3 right (root split)
	h.splitH()
	h.splitRootV()

	// pane-3 is active (rightmost). Navigate left with h.
	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	active := h.activePaneName()
	if active != "pane-1" && active != "pane-2" {
		t.Errorf("Ctrl-a h from pane-3 should focus a left pane, got %s", active)
	}

	// Navigate right with l — should go back to pane-3
	gen = h.generation()
	h.sendKeys("C-a", "l")
	h.waitLayout(gen)

	h.assertActive("pane-3")

	// Navigate up/down between pane-1 and pane-2
	gen = h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	active = h.activePaneName()
	if active != "pane-1" && active != "pane-2" {
		t.Fatal("no left pane is active")
	}

	if active == "pane-1" {
		gen = h.generation()
		h.sendKeys("C-a", "j") // down to pane-2
		h.waitLayout(gen)
		h.assertActive("pane-2")
	} else {
		gen = h.generation()
		h.sendKeys("C-a", "k") // up to pane-1
		h.waitLayout(gen)
		h.assertActive("pane-1")
	}
}

func TestNavigateBackToRightPaneAfterRootHSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	// Root horizontal split: top (pane-1 | pane-2), bottom (pane-3)
	h.splitRootH()

	// pane-3 is active (bottom). Navigate up with k.
	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	upTarget := h.activePaneName()
	if upTarget != "pane-1" && upTarget != "pane-2" {
		t.Fatalf("k from pane-3 should focus a top pane, got %s", upTarget)
	}

	// Navigate right with l. The target depends on which pane "up" landed on:
	// - From pane-1: l reaches pane-2 (adjacent right)
	// - From pane-2: l wraps to pane-1 (rightmost, so wraps to left edge)
	gen = h.generation()
	h.sendKeys("C-a", "l")
	h.waitLayout(gen)

	rightTarget := h.activePaneName()
	if upTarget == "pane-1" && rightTarget != "pane-2" {
		t.Errorf("l from pane-1 should reach pane-2, got %s", rightTarget)
	}
	if upTarget == "pane-2" && rightTarget != "pane-1" {
		t.Errorf("l from pane-2 should wrap to pane-1, got %s", rightTarget)
	}
}

func TestPrefixArrowFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split vertically: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	h.assertActive("pane-2")

	// Prefix + Left arrow should focus pane-1
	gen := h.generation()
	h.sendKeys("C-a", "Left")
	h.waitLayout(gen)

	h.assertActive("pane-1")

	// Prefix + Right arrow should focus pane-2
	gen = h.generation()
	h.sendKeys("C-a", "Right")
	h.waitLayout(gen)

	h.assertActive("pane-2")
}

func TestAltHJKLFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split vertically: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	h.assertActive("pane-2")

	// Alt+h should focus left (pane-1)
	gen := h.generation()
	h.sendKeys("M-h")
	h.waitLayout(gen)

	h.assertActive("pane-1")

	// Alt+l should focus right (pane-2)
	gen = h.generation()
	h.sendKeys("M-l")
	h.waitLayout(gen)

	h.assertActive("pane-2")
}

func TestFocusUpFromFullWidthBottomPane(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)

	// Root split creates a new full-width pane below the existing 3x3 grid.
	runLayoutCommand(t, h, "focus", "pane-9")
	runLayoutCommand(t, h, "split", "root")

	h.assertActive("pane-10")

	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	if got := h.activePaneName(); got == "pane-10" {
		t.Fatalf("focus up from full-width bottom pane should move to a pane above, got %s", got)
	}
}

func TestFocusUpFromFullWidthBottomPaneAfterResizeRoundTrip(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	runLayoutCommand(t, h, "focus", "pane-9")
	runLayoutCommand(t, h, "split", "root")
	resizeRoundTrip(t, h)

	h.assertActive("pane-10")

	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	if got := h.activePaneName(); got == "pane-10" {
		t.Fatalf("focus up after resize round-trip should move to a pane above, got %s", got)
	}
}

func TestFocusUpFromFullWidthBottomPaneInUnevenGrid(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	makeGridUneven(t, h)
	runLayoutCommand(t, h, "focus", "pane-9")
	runLayoutCommand(t, h, "split", "root")

	h.assertActive("pane-10")

	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	if got := h.activePaneName(); got == "pane-10" {
		t.Fatalf("focus up in uneven grid should move to a pane above, got %s", got)
	}
}

func TestFocusUpFromFullWidthBottomPaneAfterVerticalPaneResize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  string
	}{
		{name: "grow-up", dir: "up"},
		{name: "grow-down", dir: "down"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newAmuxHarness(t)

			makeThreeByThreeGrid(t, h)
			runLayoutCommand(t, h, "focus", "pane-9")
			runLayoutCommand(t, h, "split", "root")
			runLayoutCommand(t, h, "resize-pane", "pane-10", tt.dir, "2")

			h.assertActive("pane-10")

			gen := h.generation()
			h.sendKeys("C-a", "k")
			h.waitLayout(gen)

			if got := h.activePaneName(); got == "pane-10" {
				t.Fatalf("focus up after vertical resize %s should move to a pane above, got %s", tt.dir, got)
			}
		})
	}
}

func TestAltHJKLFocusVertical(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split horizontally: pane-1 (top) / pane-2 (bottom, active)
	h.splitH()

	// Alt+k should focus up (pane-1)
	gen := h.generation()
	h.sendKeys("M-k")
	h.waitLayout(gen)

	h.assertActive("pane-1")

	// Alt+j should focus down (pane-2)
	gen = h.generation()
	h.sendKeys("M-j")
	h.waitLayout(gen)

	h.assertActive("pane-2")
}

func TestPrefixArrowFocusVertical(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split horizontally: pane-1 (top) / pane-2 (bottom, active)
	h.splitH()

	// Prefix + Up arrow should focus pane-1
	gen := h.generation()
	h.sendKeys("C-a", "Up")
	h.waitLayout(gen)

	h.assertActive("pane-1")

	// Prefix + Down arrow should focus pane-2
	gen = h.generation()
	h.sendKeys("C-a", "Down")
	h.waitLayout(gen)

	h.assertActive("pane-2")
}
