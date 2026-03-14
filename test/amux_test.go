package test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBasicStartAndDetach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Status line should be on row 0 with pane name
	h.assertScreen("should show pane status on first row", func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 0 && strings.Contains(lines[0], "[pane-")
	})

	// Global bar should be on the last non-empty row
	h.assertScreen("should show global bar on last row", func(s string) bool {
		lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
		last := lines[len(lines)-1]
		return isGlobalBar(last)
	})

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)
}

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Both pane names should be on the SAME row (side by side)
	h.assertScreen("pane names on same row (left/right split)", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "[pane-2]") {
				return true
			}
		}
		return false
	})

	// Vertical border should span most rows
	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no consistent vertical border found")
	}

	// pane-1 name should be left of the border, pane-2 right of it
	lines := h.contentLines()
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col || col2 <= col {
		t.Errorf("pane-1 (col %d) should be left of border (col %d), pane-2 (col %d) right", col1, col, col2)
	}
}

func TestSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Pane names should be on DIFFERENT rows (top/bottom split)
	lines := h.contentLines()
	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 < 0 || row2 < 0 {
		t.Fatal("could not find both pane names")
	}
	if row1 >= row2 {
		t.Errorf("pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
	}

	// Horizontal border should be between the two panes
	borderRow := h.horizontalBorderRow()
	if borderRow < 0 {
		t.Fatal("no horizontal border found")
	}
	if borderRow <= row1 || borderRow >= row2 {
		t.Errorf("border (row %d) should be between pane-1 (row %d) and pane-2 (row %d)", borderRow, row1, row2)
	}
}

func TestFocusCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// pane-2 should be active (●) after split
	h.assertScreen("pane-2 should be active after split", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-2]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})

	// Cycle focus
	h.sendKeys("C-a", "o")
	time.Sleep(500 * time.Millisecond)

	// Now pane-1 should be active (●) and pane-2 inactive (○)
	h.assertScreen("pane-1 active, pane-2 inactive after cycle", func(s string) bool {
		pane1Active := false
		pane2Inactive := false
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
				pane1Active = true
			}
			if strings.Contains(line, "[pane-2]") && strings.Contains(line, "○") {
				pane2Inactive = true
			}
		}
		return pane1Active && pane2Inactive
	})
}

func TestPaneClose(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("e", "x", "i", "t", "Enter")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after exit")
	}

	// pane-1 should be the only pane, taking full width (no vertical borders)
	h.assertScreen("single pane after close, no borders", func(s string) bool {
		hasPane1 := false
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-1]") {
				hasPane1 = true
			}
			if isGlobalBar(line) {
				continue
			}
			if strings.Contains(line, "│") {
				return false
			}
		}
		return hasPane1
	})

	// pane-1 status should be on row 0 (full screen, no offset)
	h.assertScreen("pane-1 status on first row", func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 0 && strings.Contains(lines[0], "[pane-1]")
	})
}

func TestList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list should contain pane-1, got:\n%s", output)
	}
	if !strings.Contains(output, "pane-2") {
		t.Errorf("list should contain pane-2, got:\n%s", output)
	}
	// Active pane should be marked with *
	if !strings.Contains(output, "*") {
		t.Errorf("list should mark active pane with *, got:\n%s", output)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("status")
	if !strings.Contains(output, "1 total") {
		t.Errorf("status should show 1 total, got:\n%s", output)
	}
}

func TestReattach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "H", "E", "L", "L", "O", "Enter")
	h.waitFor("HELLO", 3*time.Second)

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)

	h.sendKeys(amuxBin, " -s ", h.session, "Enter")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("reattach failed, screen:\n%s", screen)
	}

	// Screen should be reconstructed — HELLO visible and status bar present
	h.assertScreen("HELLO and status bar after reattach", func(s string) bool {
		return strings.Contains(s, "HELLO") && strings.Contains(s, "[pane-")
	})
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

	lines := h.contentLines()

	// All 3 pane names should be visible
	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("pane %s not found on screen", name)
		}
	}

	// Vertical border from root split should span most of the height
	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no consistent vertical border found for root split")
	}

	// pane-3 should be to the right of the border
	col3 := paneNameCol(lines, "pane-3")
	if col3 <= col {
		t.Errorf("pane-3 (col %d) should be right of root border (col %d)", col3, col)
	}

	// pane-1 and pane-2 should both be to the left of the border
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col {
		t.Errorf("pane-1 (col %d) should be left of root border (col %d)", col1, col)
	}
	if col2 >= col {
		t.Errorf("pane-2 (col %d) should be left of root border (col %d)", col2, col)
	}

	// pane-1 should be above pane-2 (stacked in left column)
	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 >= row2 {
		t.Errorf("pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
	}
}

func TestRootSplitHorizontal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Vertical split first: pane-1 left, pane-2 right
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Root horizontal split: top row (pane-1 + pane-2 side by side), bottom row (pane-3)
	h.sendKeys("C-a", "_")
	h.waitFor("[pane-3]", 3*time.Second)

	lines := h.contentLines()

	// All 3 pane names should be visible
	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("pane %s not found on screen", name)
		}
	}

	// pane-1 and pane-2 should be on the same row (side by side, top half)
	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 != row2 {
		t.Errorf("pane-1 (row %d) and pane-2 (row %d) should be on same row", row1, row2)
	}

	// pane-3 should be below both
	row3 := paneNameRow(lines, "pane-3")
	if row3 <= row1 {
		t.Errorf("pane-3 (row %d) should be below pane-1/pane-2 (row %d)", row3, row1)
	}

	// Horizontal border from root split should be between top and bottom
	borderRow := h.horizontalBorderRow()
	if borderRow < 0 {
		t.Fatal("no horizontal border found for root split")
	}
	if borderRow <= row1 || borderRow >= row3 {
		t.Errorf("border (row %d) should be between top panes (row %d) and pane-3 (row %d)", borderRow, row1, row3)
	}

	// pane-1 should be left of pane-2
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col2 {
		t.Errorf("pane-1 (col %d) should be left of pane-2 (col %d)", col1, col2)
	}
}

func TestFocusNavigationThreePanes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create 3 panes: split vertical, then split one horizontal
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)

	// Cycle through all 3 panes with Ctrl-a o
	// After 3 cycles we should be back to the starting pane
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		h.sendKeys("C-a", "o")
		time.Sleep(400 * time.Millisecond)
		for _, line := range strings.Split(h.capture(), "\n") {
			for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
				if strings.Contains(line, "["+name+"]") && strings.Contains(line, "●") {
					seen[name] = true
				}
			}
		}
	}

	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if !seen[name] {
			t.Errorf("focus cycle never reached %s (saw: %v)", name, seen)
		}
	}
}

func TestDirectionalFocusAfterRootSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create: pane-1 top-left, pane-2 bottom-left, pane-3 right (root split)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	// pane-3 is active (rightmost). Navigate left with h.
	h.sendKeys("C-a", "h")
	time.Sleep(400 * time.Millisecond)

	// Should land on pane-1 or pane-2 (left column)
	leftActive := false
	for _, line := range strings.Split(h.capture(), "\n") {
		if (strings.Contains(line, "[pane-1]") || strings.Contains(line, "[pane-2]")) &&
			strings.Contains(line, "●") {
			leftActive = true
			break
		}
	}
	if !leftActive {
		screen := h.capture()
		t.Errorf("Ctrl-a h from pane-3 should focus a left pane\nScreen:\n%s", screen)
	}

	// Navigate right with l — should go back to pane-3
	h.sendKeys("C-a", "l")
	time.Sleep(400 * time.Millisecond)

	h.assertScreen("Ctrl-a l should focus pane-3", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-3]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})

	// Navigate up/down between pane-1 and pane-2
	h.sendKeys("C-a", "h")
	time.Sleep(400 * time.Millisecond)

	// Find which left pane is active
	var activeName string
	for _, line := range strings.Split(h.capture(), "\n") {
		if strings.Contains(line, "●") {
			if strings.Contains(line, "[pane-1]") {
				activeName = "pane-1"
			} else if strings.Contains(line, "[pane-2]") {
				activeName = "pane-2"
			}
		}
	}

	if activeName == "" {
		t.Fatal("no left pane is active")
	}

	// Navigate to the other left pane with j or k
	if activeName == "pane-1" {
		h.sendKeys("C-a", "j") // down to pane-2
		time.Sleep(400 * time.Millisecond)
		h.assertScreen("j from pane-1 should reach pane-2", func(s string) bool {
			for _, line := range strings.Split(s, "\n") {
				if strings.Contains(line, "[pane-2]") && strings.Contains(line, "●") {
					return true
				}
			}
			return false
		})
	} else {
		h.sendKeys("C-a", "k") // up to pane-1
		time.Sleep(400 * time.Millisecond)
		h.assertScreen("k from pane-2 should reach pane-1", func(s string) bool {
			for _, line := range strings.Split(s, "\n") {
				if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
					return true
				}
			}
			return false
		})
	}
}

func TestNavigateBackToRightPaneAfterRootHSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Vertical split: pane-1 left, pane-2 right
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Root horizontal split: top (pane-1 | pane-2), bottom (pane-3)
	h.sendKeys("C-a", "_")
	h.waitFor("[pane-3]", 3*time.Second)

	// pane-3 is active (bottom). Navigate up with k.
	h.sendKeys("C-a", "k")
	time.Sleep(400 * time.Millisecond)

	// Should be on pane-1 or pane-2 (top row)
	screen := h.capture()
	topActive := false
	for _, line := range strings.Split(screen, "\n") {
		if (strings.Contains(line, "[pane-1]") || strings.Contains(line, "[pane-2]")) &&
			strings.Contains(line, "●") {
			topActive = true
		}
	}
	if !topActive {
		t.Fatalf("k from pane-3 should focus a top pane\nScreen:\n%s", screen)
	}

	// Now navigate right with l to reach pane-2
	h.sendKeys("C-a", "l")
	time.Sleep(400 * time.Millisecond)

	h.assertScreen("l should reach pane-2 (right side of top row)", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-2]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})
}

func TestRootVerticalSplitRenderClipping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Horizontal split first, then root vertical
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	// Type a long line in pane-3 to trigger potential bleeding
	h.sendKeys("e", "c", "h", "o", " ", "R", "I", "G", "H", "T", "P", "A", "N", "E", "T", "E", "S", "T", "Enter")
	time.Sleep(500 * time.Millisecond)

	// The vertical border column should be consistent on every content row.
	// If content bleeds, some rows will have the border shifted or missing.
	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no consistent vertical border found")
	}

	// Check that no content from the right pane appears left of the border
	// (excluding the status bar rows which have pane names)
	lines := h.contentLines()
	for i, line := range lines {
		runes := []rune(line)
		if col >= len(runes) {
			continue
		}
		// The character at the border column should be │ on most lines
		if runes[col] != '│' && runes[col] != '─' {
			// Allow status line rows (contain [pane-]) and empty lines
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

	// Exit pane-3 (rightmost, active)
	h.sendKeys("e", "x", "i", "t", "Enter")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-3]")
	}, 5*time.Second) {
		t.Fatal("pane-3 should disappear after exit")
	}

	// Remaining panes should fill the full width
	// The vertical border should be roughly at the midpoint (~col 40 for 80-wide terminal)
	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("no vertical border found — panes may not have resized")
	}

	// Border should be near the middle (between col 30 and 50 for an 80-wide terminal)
	if col < 30 || col > 50 {
		lines := h.contentLines()
		t.Errorf("border at col %d, expected near middle (30-50) — panes didn't resize\nScreen:\n%s",
			col, strings.Join(lines, "\n"))
	}
}

func TestFiveRootVerticalSplits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Do 4 more root-level vertical splits (5 panes total)
	for i := 0; i < 4; i++ {
		h.sendKeys("C-a", "|")
		h.waitFor(fmt.Sprintf("[pane-%d]", i+2), 3*time.Second)
	}

	// All 5 pane names should be visible
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("pane-%d", i)
		if !h.waitFor("["+name+"]", 3*time.Second) {
			screen := h.capture()
			t.Fatalf("%s not found\nScreen:\n%s", name, screen)
		}
	}

	// All pane names should be on the SAME row (all side by side)
	lines := h.contentLines()
	row0 := lines[0]
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("[pane-%d]", i)
		if !strings.Contains(row0, name) {
			t.Errorf("%s not on first row\nRow 0: %s", name, row0)
		}
	}

	// Should have 4 vertical borders
	borderCount := 0
	for _, r := range []rune(row0) {
		if r == '│' {
			borderCount++
		}
	}
	if borderCount != 4 {
		t.Errorf("expected 4 vertical borders on first row, got %d\nRow 0: %s", borderCount, row0)
	}
}
