package test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func setLead(t *testing.T, h *ServerHarness, pane string) {
	t.Helper()
	gen := h.generation()
	out := h.runCmd("set-lead", pane)
	if !strings.Contains(out, "Set lead") {
		t.Fatalf("set-lead output = %q, want success message", out)
	}
	h.waitLayout(gen)
}

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestRootSplitVerticalWithLead(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.splitV()
	setLead(t, h, "pane-1")

	gen := h.generation()
	h.runCmd("split", "pane-1", "root", "v")
	h.waitLayout(gen)

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	if !p1.Lead {
		t.Fatal("pane-1 should still be marked as lead")
	}
	if p1.Position.X >= p2.Position.X || p1.Position.X >= p3.Position.X {
		t.Fatalf("lead pane should remain leftmost: p1.x=%d p2.x=%d p3.x=%d", p1.Position.X, p2.Position.X, p3.Position.X)
	}
	if p2.Position.X >= p3.Position.X {
		t.Fatalf("logical-root panes should remain ordered left-to-right: p2.x=%d p3.x=%d", p2.Position.X, p3.Position.X)
	}
}

func TestRootSplitHorizontalWithLead(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.splitV()
	setLead(t, h, "pane-1")

	gen := h.generation()
	h.runCmd("split", "pane-1", "root", "--horizontal")
	h.waitLayout(gen)

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	if !p1.Lead {
		t.Fatal("pane-1 should still be marked as lead")
	}
	if p1.Position.X >= p2.Position.X || p1.Position.X >= p3.Position.X {
		t.Fatalf("lead pane should remain leftmost: p1.x=%d p2.x=%d p3.x=%d", p1.Position.X, p2.Position.X, p3.Position.X)
	}
	if p2.Position.Y >= p3.Position.Y {
		t.Fatalf("logical-root panes should split top-to-bottom: p2.y=%d p3.y=%d", p2.Position.Y, p3.Position.Y)
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

func TestGoldenThreeColumnsMiddleSplitWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitRootV()
	h.splitRootV()
	setLead(t, h, "pane-1")
	h.doFocus("pane-2")
	h.splitH()
	h.splitH()
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "three_col_middle_split_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "three_col_middle_split_lead.color", colorMap)
}

func TestGoldenCloseColumnRebalancesLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitRootV()
	h.splitRootV()
	setLead(t, h, "pane-1")

	gen := h.generation()
	out := h.runCmd("kill", "pane-3")
	if !strings.Contains(out, "Killed") {
		t.Fatalf("kill pane-3 failed: %s", out)
	}
	h.waitLayout(gen)
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "close_column_rebalances_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "close_column_rebalances_lead.color", colorMap)
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

func TestGoldenNinePaneWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitRootV()
	h.splitRootV()
	setLead(t, h, "pane-1")

	h.doFocus("pane-2")
	h.splitH()
	h.splitH()

	h.doFocus("pane-3")
	h.splitH()
	h.splitH()

	h.doFocus("pane-4")
	h.splitH()
	h.splitH()

	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "nine_pane_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "nine_pane_lead.color", colorMap)
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

func TestGoldenVerticalSplitWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	setLead(t, h, "pane-1")
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "vertical_split_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "vertical_split_lead.color", colorMap)
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

func TestGoldenHorizontalSplitWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	setLead(t, h, "pane-1")
	gen := h.generation()
	h.runCmd("split", "pane-1", "root", "--horizontal")
	h.waitLayout(gen)
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "horizontal_split_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "horizontal_split_lead.color", colorMap)
}

func TestGoldenRootVerticalSplitWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	setLead(t, h, "pane-1")
	gen := h.generation()
	h.runCmd("split", "pane-1", "root", "v")
	h.waitLayout(gen)
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "root_vertical_split_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "root_vertical_split_lead.color", colorMap)
}

func TestGoldenRootHorizontalSplitWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	setLead(t, h, "pane-1")
	gen := h.generation()
	h.runCmd("split", "pane-1", "root", "--horizontal")
	h.waitLayout(gen)
	h.doFocus("pane-1")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "root_horizontal_split_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "root_horizontal_split_lead.color", colorMap)
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

func TestGoldenFourPaneWithLead(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	setLead(t, h, "pane-1")
	h.doFocus("pane-2")
	h.splitH()
	h.doFocus("pane-2")

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "four_pane_lead.golden", frame)

	colorMap := h.runCmd("capture", "--colors")
	assertGolden(t, "four_pane_lead.color", colorMap)
}

// ---------------------------------------------------------------------------
// Golden file tests — layout mutation operations
// ---------------------------------------------------------------------------

const (
	mutationGoldenWidth   = 80
	mutationGoldenHeight  = 24
	mutationGoldenSession = "t-00000000"
)

type mutationGoldenHarness struct {
	t      *testing.T
	window *mux.Window
	panes  map[uint32]*mutationGoldenPaneData
	nextID uint32
}

type mutationGoldenPaneData struct {
	id    uint32
	name  string
	color string
	lead  bool
}

func (p *mutationGoldenPaneData) RenderScreen(bool) string { return "" }
func (p *mutationGoldenPaneData) CellAt(int, int, bool) render.ScreenCell {
	return render.ScreenCell{Char: " ", Width: 1}
}
func (p *mutationGoldenPaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *mutationGoldenPaneData) CursorPos() (int, int)                   { return 0, 0 }
func (p *mutationGoldenPaneData) CursorHidden() bool                      { return true }
func (p *mutationGoldenPaneData) HasCursorBlock() bool                    { return false }
func (p *mutationGoldenPaneData) ID() uint32                              { return p.id }
func (p *mutationGoldenPaneData) Name() string                            { return p.name }
func (p *mutationGoldenPaneData) TrackedPRs() []proto.TrackedPR           { return nil }
func (p *mutationGoldenPaneData) TrackedIssues() []proto.TrackedIssue     { return nil }
func (p *mutationGoldenPaneData) Issue() string                           { return "" }
func (p *mutationGoldenPaneData) Host() string                            { return mux.DefaultHost }
func (p *mutationGoldenPaneData) Task() string                            { return "" }
func (p *mutationGoldenPaneData) Color() string                           { return p.color }
func (p *mutationGoldenPaneData) Idle() bool                              { return true }
func (p *mutationGoldenPaneData) IsLead() bool                            { return p.lead }
func (p *mutationGoldenPaneData) ConnStatus() string                      { return "" }
func (p *mutationGoldenPaneData) InCopyMode() bool                        { return false }
func (p *mutationGoldenPaneData) CopyModeSearch() string                  { return "" }

func newMutationGoldenHarness(t *testing.T) *mutationGoldenHarness {
	t.Helper()
	h := &mutationGoldenHarness{
		t:      t,
		panes:  make(map[uint32]*mutationGoldenPaneData),
		nextID: 1,
	}
	first := h.newPane()
	h.window = mux.NewWindow(first, mutationGoldenWidth, mutationGoldenHeight-render.GlobalBarHeight)
	return h
}

func (h *mutationGoldenHarness) newPane() *mux.Pane {
	id := h.nextID
	h.nextID++
	name := fmt.Sprintf("pane-%d", id)
	color := config.AccentColor(id - 1)
	h.panes[id] = &mutationGoldenPaneData{id: id, name: name, color: color}
	return &mux.Pane{
		ID: id,
		Meta: mux.PaneMeta{
			Name:  name,
			Host:  mux.DefaultHost,
			Color: color,
		},
	}
}

func (h *mutationGoldenHarness) splitV() {
	h.t.Helper()
	h.mustPane(h.window.Split(mux.SplitVertical, h.newPane()))
}

func (h *mutationGoldenHarness) splitH() {
	h.t.Helper()
	h.mustPane(h.window.Split(mux.SplitHorizontal, h.newPane()))
}

func (h *mutationGoldenHarness) splitRootV() {
	h.t.Helper()
	h.mustPane(h.window.SplitRoot(mux.SplitVertical, h.newPane()))
}

func (h *mutationGoldenHarness) focus(id uint32) {
	h.t.Helper()
	pane, err := h.window.ResolvePane(fmt.Sprintf("%d", id))
	if err != nil {
		h.t.Fatalf("resolve pane %d: %v", id, err)
	}
	h.window.FocusPane(pane)
}

func (h *mutationGoldenHarness) setLead(id uint32) {
	h.t.Helper()
	if err := h.window.SetLead(id); err != nil {
		h.t.Fatalf("set lead pane-%d: %v", id, err)
	}
}

func (h *mutationGoldenHarness) closePane(id uint32) {
	h.t.Helper()
	if err := h.window.ClosePane(id); err != nil {
		h.t.Fatalf("close pane-%d: %v", id, err)
	}
}

func (h *mutationGoldenHarness) movePane(paneID, targetPaneID uint32, before bool) {
	h.t.Helper()
	if err := h.window.MovePane(paneID, targetPaneID, before); err != nil {
		h.t.Fatalf("move pane-%d relative to pane-%d: %v", paneID, targetPaneID, err)
	}
}

func (h *mutationGoldenHarness) moveToColumn(paneID, targetPaneID uint32) {
	h.t.Helper()
	if err := h.window.MovePaneToColumn(paneID, targetPaneID); err != nil {
		h.t.Fatalf("move pane-%d to column pane-%d: %v", paneID, targetPaneID, err)
	}
}

func (h *mutationGoldenHarness) moveUp(id uint32) {
	h.t.Helper()
	if err := h.window.MovePaneUp(id); err != nil {
		h.t.Fatalf("move-up pane-%d: %v", id, err)
	}
}

func (h *mutationGoldenHarness) moveDown(id uint32) {
	h.t.Helper()
	if err := h.window.MovePaneDown(id); err != nil {
		h.t.Fatalf("move-down pane-%d: %v", id, err)
	}
}

func (h *mutationGoldenHarness) swapForward() {
	h.t.Helper()
	if err := h.window.SwapPaneForward(); err != nil {
		h.t.Fatalf("swap forward: %v", err)
	}
}

func (h *mutationGoldenHarness) swapBackward() {
	h.t.Helper()
	if err := h.window.SwapPaneBackward(); err != nil {
		h.t.Fatalf("swap backward: %v", err)
	}
}

func (h *mutationGoldenHarness) swapPanes(id1, id2 uint32) {
	h.t.Helper()
	if err := h.window.SwapPanes(id1, id2); err != nil {
		h.t.Fatalf("swap pane-%d pane-%d: %v", id1, id2, err)
	}
}

func (h *mutationGoldenHarness) swapTree(id1, id2 uint32) {
	h.t.Helper()
	if err := h.window.SwapTree(id1, id2); err != nil {
		h.t.Fatalf("swap-tree pane-%d pane-%d: %v", id1, id2, err)
	}
}

func (h *mutationGoldenHarness) rotate(forward bool) {
	h.t.Helper()
	if err := h.window.RotatePanes(forward); err != nil {
		h.t.Fatalf("rotate forward=%t: %v", forward, err)
	}
}

func (h *mutationGoldenHarness) resizePane(id uint32, direction string, delta int) {
	h.t.Helper()
	if !h.window.ResizePane(id, direction, delta) {
		h.t.Fatalf("resize-pane pane-%d %s %d did not change layout", id, direction, delta)
	}
}

func (h *mutationGoldenHarness) resizeBorder(x, y, delta int) {
	h.t.Helper()
	if !h.window.ResizeBorder(x, y, delta) {
		h.t.Fatalf("resize-border %d,%d %d did not change layout", x, y, delta)
	}
}

func (h *mutationGoldenHarness) equalize(widths, heights bool) {
	h.t.Helper()
	if !h.window.Equalize(widths, heights) {
		h.t.Fatalf("equalize widths=%t heights=%t did not change layout", widths, heights)
	}
}

func (h *mutationGoldenHarness) assertGolden(name string) {
	h.t.Helper()
	raw := h.renderANSI()
	frame := extractFrame(render.MaterializeGrid(raw, mutationGoldenWidth, mutationGoldenHeight), mutationGoldenSession)
	assertGolden(h.t, name+".golden", frame)

	colorMap := render.ExtractColorMap(raw, mutationGoldenWidth, mutationGoldenHeight) + "\n"
	assertGolden(h.t, name+".color", colorMap)
}

func (h *mutationGoldenHarness) renderANSI() string {
	comp := render.NewCompositor(mutationGoldenWidth, mutationGoldenHeight, mutationGoldenSession)
	return comp.RenderFullWithOverlay(h.window.Root, h.window.ActivePane.ID, func(id uint32) render.PaneData {
		pane := h.panes[id]
		if pane == nil {
			return nil
		}
		pane.lead = h.window.IsLeadPane(id)
		return pane
	}, render.OverlayState{}, true)
}

func (h *mutationGoldenHarness) mustPane(_ *mux.Pane, err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

func TestGoldenClosePaneInColumn(t *testing.T) {
	t.Parallel()

	h := newMutationGoldenHarness(t)
	h.splitV()
	h.splitH()
	h.closePane(3)
	h.focus(1)
	h.assertGolden("close_pane_in_column")

	lead := newMutationGoldenHarness(t)
	lead.splitV()
	lead.setLead(1)
	lead.focus(2)
	lead.splitH()
	lead.closePane(3)
	lead.focus(1)
	lead.assertGolden("close_pane_in_column_lead")
}

func TestGoldenCloseTriggersSingleChildCollapse(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitH()
	h.closePane(2)
	h.focus(1)
	h.assertGolden("close_triggers_single_child_collapse")
}

func TestGoldenSwapForwardBackward(t *testing.T) {
	t.Parallel()

	forward := newMutationGoldenHarness(t)
	forward.splitRootV()
	forward.splitRootV()
	forward.focus(2)
	forward.swapForward()
	forward.assertGolden("swap_forward")

	backward := newMutationGoldenHarness(t)
	backward.splitRootV()
	backward.splitRootV()
	backward.focus(2)
	backward.swapBackward()
	backward.assertGolden("swap_backward")
}

func TestGoldenSwapCrossColumn(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitV()
	h.splitH()
	h.focus(1)
	h.swapPanes(1, 3)
	h.assertGolden("swap_cross_column")
}

func TestGoldenSwapTree(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitV()
	h.focus(1)
	h.splitH()
	h.focus(1)
	h.swapTree(1, 2)
	h.assertGolden("swap_tree")
}

func TestGoldenMoveBeforeAfter(t *testing.T) {
	t.Parallel()

	before := newMutationGoldenHarness(t)
	before.splitRootV()
	before.splitRootV()
	before.movePane(3, 1, true)
	before.focus(3)
	before.assertGolden("move_before")

	after := newMutationGoldenHarness(t)
	after.splitRootV()
	after.splitRootV()
	after.focus(1)
	after.movePane(1, 3, false)
	after.focus(1)
	after.assertGolden("move_after")
}

func TestGoldenMoveToColumn(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitRootV()
	h.splitRootV()
	h.focus(3)
	h.moveToColumn(3, 2)
	h.focus(3)
	h.assertGolden("move_to_column")
}

func TestGoldenMoveUpDown(t *testing.T) {
	t.Parallel()

	up := newMutationGoldenHarness(t)
	up.splitH()
	up.splitH()
	up.moveUp(3)
	up.focus(3)
	up.assertGolden("move_up")

	down := newMutationGoldenHarness(t)
	down.splitH()
	down.splitH()
	down.focus(1)
	down.moveDown(1)
	down.focus(1)
	down.assertGolden("move_down")
}

func TestGoldenRotateForwardBackward(t *testing.T) {
	t.Parallel()

	forward := newMutationGoldenHarness(t)
	forward.splitRootV()
	forward.splitRootV()
	forward.focus(1)
	forward.rotate(true)
	forward.assertGolden("rotate_forward")

	backward := newMutationGoldenHarness(t)
	backward.splitRootV()
	backward.splitRootV()
	backward.focus(1)
	backward.rotate(false)
	backward.assertGolden("rotate_backward")
}

func TestGoldenResizePaneHorizontalVertical(t *testing.T) {
	t.Parallel()

	horizontal := newMutationGoldenHarness(t)
	horizontal.splitRootV()
	horizontal.splitRootV()
	horizontal.focus(2)
	horizontal.resizePane(2, "right", 6)
	horizontal.assertGolden("resize_pane_horizontal")

	vertical := newMutationGoldenHarness(t)
	vertical.splitV()
	vertical.focus(2)
	vertical.splitH()
	vertical.focus(2)
	vertical.resizePane(2, "down", 3)
	vertical.assertGolden("resize_pane_vertical")
}

func TestGoldenEqualizeAfterManualResize(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitRootV()
	h.splitRootV()
	h.focus(1)
	h.resizePane(1, "right", 6)
	h.equalize(true, false)
	h.focus(1)
	h.assertGolden("equalize_after_manual_resize")
}

func TestGoldenEqualizeLeadPreservesUserIntent(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitV()
	h.setLead(1)
	h.resizePane(2, "left", 8)
	h.splitRootV()
	h.focus(1)
	h.assertGolden("equalize_lead_preserves_user_intent")
}

func TestGoldenRootVerticalSplitAfterManualResizeLead(t *testing.T) {
	t.Parallel()
	h := newMutationGoldenHarness(t)

	h.splitRootV()
	h.splitRootV()
	h.setLead(1)
	h.resizeBorder(h.window.Root.Children[0].W, 0, 14)
	h.splitRootV()
	h.focus(1)
	h.assertGolden("splitroot_after_manual_resize_lead")
}

// ---------------------------------------------------------------------------
// Golden file tests — zoom and multi-window states
// ---------------------------------------------------------------------------

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
