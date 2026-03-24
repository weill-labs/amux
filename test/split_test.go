package test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")

	// pane-1 should be left of pane-2
	if p1.Position.X >= p2.Position.X {
		t.Errorf("pane-1 (x=%d) should be left of pane-2 (x=%d)", p1.Position.X, p2.Position.X)
	}

	// Both should be on the same row
	if p1.Position.Y != p2.Position.Y {
		t.Errorf("pane-1 (y=%d) and pane-2 (y=%d) should be on same row", p1.Position.Y, p2.Position.Y)
	}
}

func TestSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")

	// pane-1 should be above pane-2
	if p1.Position.Y >= p2.Position.Y {
		t.Errorf("pane-1 (y=%d) should be above pane-2 (y=%d)", p1.Position.Y, p2.Position.Y)
	}

	// Both should be in the same column
	if p1.Position.X != p2.Position.X {
		t.Errorf("pane-1 (x=%d) and pane-2 (x=%d) should be in same column", p1.Position.X, p2.Position.X)
	}
}

func TestSplitNamedPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.doSplit("v", "--name", "worker-1")

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "worker-1")

	if p1.Position.X >= p2.Position.X {
		t.Errorf("pane-1 (x=%d) should be left of worker-1 (x=%d)", p1.Position.X, p2.Position.X)
	}

	if p1.Position.Y != p2.Position.Y {
		t.Errorf("pane-1 (y=%d) and worker-1 (y=%d) should be on same row", p1.Position.Y, p2.Position.Y)
	}
}

func TestSplitVerticalFlag(t *testing.T) {
	h := newServerHarness(t)

	h.doSplit("--vertical")

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")

	if p1.Position.X >= p2.Position.X {
		t.Errorf("pane-1 (x=%d) should be left of pane-2 (x=%d)", p1.Position.X, p2.Position.X)
	}
	if p1.Position.Y != p2.Position.Y {
		t.Errorf("pane-1 (y=%d) and pane-2 (y=%d) should be on same row", p1.Position.Y, p2.Position.Y)
	}
}

func TestSplitHorizontalFlag(t *testing.T) {
	h := newServerHarness(t)

	h.doSplit("--horizontal")

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")

	if p1.Position.Y >= p2.Position.Y {
		t.Errorf("pane-1 (y=%d) should be above pane-2 (y=%d)", p1.Position.Y, p2.Position.Y)
	}
	if p1.Position.X != p2.Position.X {
		t.Errorf("pane-1 (x=%d) and pane-2 (x=%d) should be in same column", p1.Position.X, p2.Position.X)
	}
}

func TestRootSplitVerticalFlag(t *testing.T) {
	h := newServerHarness(t)

	h.splitH()
	h.doSplit("root", "--vertical")

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	if p3.Position.X <= p1.Position.X {
		t.Errorf("pane-3 (x=%d) should be right of pane-1 (x=%d)", p3.Position.X, p1.Position.X)
	}
	if p3.Position.X <= p2.Position.X {
		t.Errorf("pane-3 (x=%d) should be right of pane-2 (x=%d)", p3.Position.X, p2.Position.X)
	}
	if p1.Position.Y >= p2.Position.Y {
		t.Errorf("pane-1 (y=%d) should be above pane-2 (y=%d)", p1.Position.Y, p2.Position.Y)
	}
}

func TestRootSplitVertical(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Horizontal split first: pane-1 top, pane-2 bottom
	h.splitH()

	// Root vertical split: left column (pane-1 + pane-2 stacked), right column (pane-3)
	h.splitRootV()

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	// pane-3 should be right of pane-1 and pane-2
	if p3.Position.X <= p1.Position.X {
		t.Errorf("pane-3 (x=%d) should be right of pane-1 (x=%d)", p3.Position.X, p1.Position.X)
	}
	if p3.Position.X <= p2.Position.X {
		t.Errorf("pane-3 (x=%d) should be right of pane-2 (x=%d)", p3.Position.X, p2.Position.X)
	}

	// pane-1 should be above pane-2
	if p1.Position.Y >= p2.Position.Y {
		t.Errorf("pane-1 (y=%d) should be above pane-2 (y=%d)", p1.Position.Y, p2.Position.Y)
	}
}

func TestRootSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Root horizontal split: top row (pane-1 + pane-2 side by side), bottom row (pane-3)
	h.splitRootH()
	if err := h.client.waitCommandReady(); err != nil {
		t.Fatalf("headless client not command-ready before capture: %v", err)
	}

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	// pane-1 and pane-2 should be on the same row
	if p1.Position.Y != p2.Position.Y {
		t.Errorf("pane-1 (y=%d) and pane-2 (y=%d) should be on same row", p1.Position.Y, p2.Position.Y)
	}

	// pane-3 should be below pane-1 and pane-2
	if p3.Position.Y <= p1.Position.Y {
		t.Errorf("pane-3 (y=%d) should be below pane-1 (y=%d)", p3.Position.Y, p1.Position.Y)
	}

	// pane-1 should be left of pane-2
	if p1.Position.X >= p2.Position.X {
		t.Errorf("pane-1 (x=%d) should be left of pane-2 (x=%d)", p1.Position.X, p2.Position.X)
	}
}

func TestRootVerticalSplitRenderClipping(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.splitRootV()

	// Type a long line in pane-3 to trigger potential bleeding
	h.sendKeys("pane-3", "echo RIGHTPANETEST", "Enter")
	h.waitFor("pane-3", "RIGHTPANETEST")

	// Verify pane-3 content doesn't bleed into pane-1/pane-2's columns
	c := h.captureJSON()
	p3 := h.jsonPane(c, "pane-3")
	p1 := h.jsonPane(c, "pane-1")

	// pane-3 should start after pane-1 ends (plus border)
	if p3.Position.X <= p1.Position.X+p1.Position.Width {
		t.Errorf("pane-3 (x=%d) should start after pane-1 (x=%d, w=%d)",
			p3.Position.X, p1.Position.X, p1.Position.Width)
	}

	// Also verify via text capture that content doesn't bleed
	col := h.captureVerticalBorderCol()
	if col < 0 {
		t.Fatal("no consistent vertical border found")
	}

	lines := h.captureContentLines()
	for i, line := range lines {
		runes := []rune(line)
		if col >= len(runes) {
			continue
		}
		if !isBorderRune(runes[col]) {
			if strings.Contains(line, "[pane-") || strings.TrimSpace(line) == "" {
				continue
			}
			t.Errorf("row %d: expected border at col %d, got %c\nline: %s", i, col, runes[col], line)
		}
	}
}

func TestExitAfterDoubleRootVSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Root vertical split twice: pane-1 | pane-2 | pane-3
	h.splitRootV()
	h.splitRootV()

	h.sendKeys("pane-3", "exit", "Enter")
	if !h.waitForCaptureJSON(func(c proto.CaptureJSON) bool {
		return len(c.Panes) == 2
	}, 10*time.Second) {
		t.Fatalf("timed out waiting for pane exit\ncapture:\n%s", h.capture())
	}

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes after exit, got %d", len(c.Panes))
	}

	// Remaining panes should roughly split the width
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	totalW := p1.Position.Width + p2.Position.Width + 1 // +1 for border
	if totalW < 70 || totalW > 82 {
		t.Errorf("remaining panes should fill ~80 cols, got %d (p1=%d, p2=%d)",
			totalW, p1.Position.Width, p2.Position.Width)
	}
}

func TestFiveRootVerticalSplits(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	for i := 0; i < 4; i++ {
		h.splitRootV()
	}

	c := h.captureJSON()
	if len(c.Panes) != 5 {
		t.Fatalf("expected 5 panes, got %d", len(c.Panes))
	}

	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("pane-%d", i)
		h.jsonPane(c, name) // fails test if not found
	}

	// Verify panes are ordered left to right by x position
	for i := 0; i < len(c.Panes)-1; i++ {
		if c.Panes[i].Position.X >= c.Panes[i+1].Position.X {
			t.Errorf("pane %s (x=%d) should be left of pane %s (x=%d)",
				c.Panes[i].Name, c.Panes[i].Position.X,
				c.Panes[i+1].Name, c.Panes[i+1].Position.X)
		}
	}
}

func TestMultipleNonRootSplitsEqualWidth(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// 3 non-root splits produce 4 nested panes
	for i := 0; i < 3; i++ {
		h.splitV()
	}

	c := h.captureJSON()
	if len(c.Panes) != 4 {
		t.Fatalf("expected 4 panes, got %d", len(c.Panes))
	}

	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("pane-%d", i)
		h.jsonPane(c, name) // fails if not found
	}

	// Verify panes are ordered left to right
	for i := 0; i < len(c.Panes)-1; i++ {
		if c.Panes[i].Position.X >= c.Panes[i+1].Position.X {
			t.Errorf("pane %s (x=%d) should be left of pane %s (x=%d)",
				c.Panes[i].Name, c.Panes[i].Position.X,
				c.Panes[i+1].Name, c.Panes[i+1].Position.X)
		}
	}
}

func TestThreeColumnsMiddleSplitEqualRows(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Build: 3 columns, then split middle column into 3 rows
	// pane-1 | pane-2 | pane-3
	//        | pane-4 |
	//        | pane-5 |
	h.splitRootV()
	h.splitRootV()

	// Focus middle column (pane-2) and split horizontally twice
	h.doFocus("pane-2")
	h.splitH()
	h.splitH()

	c := h.captureJSON()
	if len(c.Panes) != 5 {
		t.Fatalf("expected 5 panes, got %d", len(c.Panes))
	}

	// The 3 panes in the middle column should have approximately equal heights
	p2 := h.jsonPane(c, "pane-2")
	p4 := h.jsonPane(c, "pane-4")
	p5 := h.jsonPane(c, "pane-5")

	// Equal splits should produce heights within 1 of each other
	heights := []int{p2.Position.Height, p4.Position.Height, p5.Position.Height}
	minH, maxH := heights[0], heights[0]
	for _, v := range heights[1:] {
		if v < minH {
			minH = v
		}
		if v > maxH {
			maxH = v
		}
	}
	if maxH-minH > 1 {
		t.Errorf("middle column rows not equal: pane-2 H=%d, pane-4 H=%d, pane-5 H=%d",
			p2.Position.Height, p4.Position.Height, p5.Position.Height)
	}

	// pane-1 and pane-3 should span the full height (leftmost/rightmost columns)
	p1 := h.jsonPane(c, "pane-1")
	p3 := h.jsonPane(c, "pane-3")
	if p1.Position.Y != 0 || p3.Position.Y != 0 {
		t.Errorf("pane-1 (y=%d) and pane-3 (y=%d) should start at row 0", p1.Position.Y, p3.Position.Y)
	}
	if p1.Position.Height != p3.Position.Height {
		t.Errorf("pane-1 (H=%d) and pane-3 (H=%d) should have equal height", p1.Position.Height, p3.Position.Height)
	}
}

func TestGoldenThreeColumnsMiddleSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitRootV()
	h.splitRootV()
	h.doFocus("pane-2")
	h.splitH()
	h.splitH()

	// Focus pane-1 so active state is deterministic
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "three_col_middle_split.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "three_col_middle_split.color", colorMap)
}

func TestGoldenNinePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// 3 columns: pane-1 | pane-2 | pane-3
	h.splitRootV()
	h.splitRootV()

	// Left column: pane-1 → split H x2 → pane-1, pane-4, pane-5
	h.doFocus("pane-1")
	h.splitH()
	h.splitH()

	// Middle column: pane-2 → split H x2 → pane-2, pane-6, pane-7
	h.doFocus("pane-2")
	h.splitH()
	h.splitH()

	// Right column: pane-3 → split H x2 → pane-3, pane-8, pane-9
	h.doFocus("pane-3")
	h.splitH()
	h.splitH()

	// Focus pane-1 for deterministic active state
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "nine_pane.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "nine_pane.color", colorMap)
}

// ---------------------------------------------------------------------------
// Golden file tests
// ---------------------------------------------------------------------------

func TestGoldenVerticalSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "vertical_split.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "vertical_split.color", colorMap)
}

func TestGoldenHorizontalSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "horizontal_split.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "horizontal_split.color", colorMap)
}

func TestGoldenFourPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitH()
	h.doFocus("pane-1")
	h.splitH()

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "four_pane.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "four_pane.color", colorMap)
}

// ---------------------------------------------------------------------------
// Golden file tests — minimize, zoom, and multi-window states
// ---------------------------------------------------------------------------

func TestGoldenMinimizedColumn(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Build 3 panes stacked vertically: pane-1 (top), pane-2 (middle), pane-3 (bottom)
	h.splitH()
	h.splitH()

	// Minimize the middle pane — collapses to status-line-only height
	gen := h.generation()
	h.runCmd("minimize", "pane-2")
	h.waitLayout(gen)

	// Focus pane-1 so active state is deterministic
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "minimized_column.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "minimized_column.color", colorMap)
}

func TestGoldenZoomed(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create 3-pane layout: pane-1 | pane-2 (left-right), pane-3 below pane-2
	h.splitV()
	h.splitH()

	// Zoom pane-1 — fills the entire window, hides borders and other panes.
	// No doFocus needed: Window.Zoom() sets the target pane as active internally.
	gen := h.generation()
	h.runCmd("zoom", "pane-1")
	h.waitLayout(gen)

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "zoomed.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "zoomed.color", colorMap)
}

func TestGoldenMultiWindowTabs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Window 1: 2 panes left-right
	h.splitV()

	// Create window 2 with a distinct name
	gen := h.generation()
	h.runCmd("new-window", "--name", "logs")
	h.waitLayout(gen)

	// Split window 2 so it has 2 panes too
	h.splitV()

	// Switch back to window 1
	gen = h.generation()
	h.runCmd("select-window", "1")
	h.waitLayout(gen)

	// Focus pane-1 so active state is deterministic
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "multi_window_tabs.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "multi_window_tabs.color", colorMap)
}
