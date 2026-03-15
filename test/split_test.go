package test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	lines := h.captureAmuxContentLines()

	// Both pane names should be on the SAME row (side by side)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "[pane-1]") && strings.Contains(line, "[pane-2]") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("capture: pane names should be on same row\n%s", strings.Join(lines, "\n"))
	}

	// Vertical border should span most rows
	col := findVerticalBorderCol(lines)
	if col < 0 {
		t.Fatal("capture: no consistent vertical border found")
	}

	// pane-1 name should be left of the border, pane-2 right of it
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col || col2 <= col {
		t.Errorf("capture: pane-1 (col %d) should be left of border (col %d), pane-2 (col %d) right", col1, col, col2)
	}
}

func TestSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	lines := h.captureAmuxContentLines()

	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 < 0 || row2 < 0 {
		t.Fatalf("capture: could not find both pane names\n%s", strings.Join(lines, "\n"))
	}
	if row1 >= row2 {
		t.Errorf("capture: pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
	}

	hBorderRow := findHorizontalBorderRow(lines)
	if hBorderRow < 0 {
		t.Fatal("capture: no horizontal border found")
	}
	if hBorderRow <= row1 || hBorderRow >= row2 {
		t.Errorf("capture: border (row %d) should be between pane-1 (row %d) and pane-2 (row %d)", hBorderRow, row1, row2)
	}
}

func TestRootSplitVertical(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Horizontal split first: pane-1 top, pane-2 bottom
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Root vertical split: left column (pane-1 + pane-2 stacked), right column (pane-3)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	lines := h.captureAmuxContentLines()

	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("capture: pane %s not found\n%s", name, strings.Join(lines, "\n"))
		}
	}

	col := findVerticalBorderCol(lines)
	if col < 0 {
		t.Fatal("capture: no consistent vertical border found for root split")
	}

	col3 := paneNameCol(lines, "pane-3")
	if col3 <= col {
		t.Errorf("capture: pane-3 (col %d) should be right of root border (col %d)", col3, col)
	}

	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col {
		t.Errorf("capture: pane-1 (col %d) should be left of root border (col %d)", col1, col)
	}
	if col2 >= col {
		t.Errorf("capture: pane-2 (col %d) should be left of root border (col %d)", col2, col)
	}

	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 >= row2 {
		t.Errorf("capture: pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
	}
}

func TestRootSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Root horizontal split: top row (pane-1 + pane-2 side by side), bottom row (pane-3)
	h.sendKeys("C-a", "_")
	h.waitFor("[pane-3]", 3*time.Second)

	lines := h.captureAmuxContentLines()

	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("capture: pane %s not found\n%s", name, strings.Join(lines, "\n"))
		}
	}

	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 != row2 {
		t.Errorf("capture: pane-1 (row %d) and pane-2 (row %d) should be on same row", row1, row2)
	}

	row3 := paneNameRow(lines, "pane-3")
	if row3 <= row1 {
		t.Errorf("capture: pane-3 (row %d) should be below pane-1/pane-2 (row %d)", row3, row1)
	}

	hBorderRow := findHorizontalBorderRow(lines)
	if hBorderRow < 0 {
		t.Fatal("capture: no horizontal border found for root split")
	}
	if hBorderRow <= row1 || hBorderRow >= row3 {
		t.Errorf("capture: border (row %d) should be between top panes (row %d) and pane-3 (row %d)", hBorderRow, row1, row3)
	}

	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col2 {
		t.Errorf("capture: pane-1 (col %d) should be left of pane-2 (col %d)", col1, col2)
	}
}

func TestRootVerticalSplitRenderClipping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	// Type a long line in pane-3 to trigger potential bleeding
	h.sendKeys("e", "c", "h", "o", " ", "R", "I", "G", "H", "T", "P", "A", "N", "E", "T", "E", "S", "T", "Enter")
	time.Sleep(500 * time.Millisecond)

	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no consistent vertical border found")
	}

	lines := h.contentLines()
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
	h := newHarness(t)

	// Root vertical split twice: pane-1 | pane-2 | pane-3
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	h.sendKeys("e", "x", "i", "t", "Enter")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-3]")
	}, 5*time.Second) {
		t.Fatal("pane-3 should disappear after exit")
	}

	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no vertical border found — panes may not have resized")
	}

	if col < 30 || col > 50 {
		lines := h.contentLines()
		t.Errorf("border at col %d, expected near middle (30-50) — panes didn't resize\nScreen:\n%s",
			col, strings.Join(lines, "\n"))
	}
}

func TestFiveRootVerticalSplits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for i := 0; i < 4; i++ {
		h.sendKeys("C-a", "|")
		h.waitFor(fmt.Sprintf("[pane-%d]", i+2), 3*time.Second)
	}

	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("pane-%d", i)
		if !h.waitFor("["+name+"]", 3*time.Second) {
			screen := h.capture()
			t.Fatalf("%s not found\nScreen:\n%s", name, screen)
		}
	}

	lines := h.contentLines()
	row0 := lines[0]
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("[pane-%d]", i)
		if !strings.Contains(row0, name) {
			t.Errorf("%s not on first row\nRow 0: %s", name, row0)
		}
	}

	borderCount := 0
	for _, r := range []rune(row0) {
		if isVerticalBorderRune(r) {
			borderCount++
		}
	}
	if borderCount != 4 {
		t.Errorf("expected 4 vertical borders on first row, got %d\nRow 0: %s", borderCount, row0)
	}
}

func TestMultipleNonRootSplitsEqualWidth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for i := 0; i < 3; i++ {
		h.sendKeys("C-a", "\\")
		h.waitFor(fmt.Sprintf("[pane-%d]", i+2), 3*time.Second)
	}

	lines := h.contentLines()
	row0 := lines[0]
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("[pane-%d]", i)
		if !strings.Contains(row0, name) {
			t.Errorf("%s not on first row\nRow 0: %s", name, row0)
		}
	}

	borderCount := 0
	for _, r := range []rune(row0) {
		if isVerticalBorderRune(r) {
			borderCount++
		}
	}
	if borderCount != 3 {
		t.Errorf("expected 3 vertical borders, got %d\nRow 0: %s", borderCount, row0)
	}
}
