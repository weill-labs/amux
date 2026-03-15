package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBasicStartAndDetach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Verify layout via amux capture (server-side compositor)
	lines := h.captureAmuxLines()
	if len(lines) == 0 || !strings.Contains(lines[0], "[pane-") {
		t.Errorf("capture: first row should contain pane status, got: %q", lines[0])
	}

	// Global bar should be on the last non-empty row
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastNonEmpty = lines[i]
			break
		}
	}
	if !isGlobalBar(lastNonEmpty) {
		t.Errorf("capture: last row should be global bar, got: %q", lastNonEmpty)
	}

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)
}

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Verify layout via amux capture
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

	// Verify layout via amux capture
	lines := h.captureAmuxContentLines()

	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 < 0 || row2 < 0 {
		t.Fatalf("capture: could not find both pane names\n%s", strings.Join(lines, "\n"))
	}
	if row1 >= row2 {
		t.Errorf("capture: pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
	}

	// Horizontal border should be between the two panes
	hBorderRow := -1
	for i, line := range lines {
		count := 0
		for _, r := range line {
			if r == '─' || r == '┼' || r == '┬' || r == '┴' {
				count++
			}
		}
		if count > 10 {
			hBorderRow = i
			break
		}
	}
	if hBorderRow < 0 {
		t.Fatal("capture: no horizontal border found")
	}
	if hBorderRow <= row1 || hBorderRow >= row2 {
		t.Errorf("capture: border (row %d) should be between pane-1 (row %d) and pane-2 (row %d)", hBorderRow, row1, row2)
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

	// Verify layout via amux capture: single pane, no borders
	capLines := h.captureAmuxContentLines()
	hasPane1 := false
	for _, line := range capLines {
		if strings.Contains(line, "[pane-1]") {
			hasPane1 = true
		}
		if strings.Contains(line, "│") {
			t.Errorf("capture: no vertical borders expected after close, got: %q", line)
			break
		}
	}
	if !hasPane1 {
		t.Errorf("capture: pane-1 should still be visible\n%s", strings.Join(capLines, "\n"))
	}

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

	// Verify layout via amux capture
	lines := h.captureAmuxContentLines()

	// All 3 pane names should be visible
	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("capture: pane %s not found\n%s", name, strings.Join(lines, "\n"))
		}
	}

	// Vertical border from root split should span most of the height
	col := findVerticalBorderCol(lines)
	if col < 0 {
		t.Fatal("capture: no consistent vertical border found for root split")
	}

	// pane-3 should be to the right of the border
	col3 := paneNameCol(lines, "pane-3")
	if col3 <= col {
		t.Errorf("capture: pane-3 (col %d) should be right of root border (col %d)", col3, col)
	}

	// pane-1 and pane-2 should both be to the left of the border
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col {
		t.Errorf("capture: pane-1 (col %d) should be left of root border (col %d)", col1, col)
	}
	if col2 >= col {
		t.Errorf("capture: pane-2 (col %d) should be left of root border (col %d)", col2, col)
	}

	// pane-1 should be above pane-2 (stacked in left column)
	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 >= row2 {
		t.Errorf("capture: pane-1 (row %d) should be above pane-2 (row %d)", row1, row2)
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

	// Verify layout via amux capture
	lines := h.captureAmuxContentLines()

	// All 3 pane names should be visible
	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if paneNameRow(lines, name) < 0 {
			t.Errorf("capture: pane %s not found\n%s", name, strings.Join(lines, "\n"))
		}
	}

	// pane-1 and pane-2 should be on the same row (side by side, top half)
	row1 := paneNameRow(lines, "pane-1")
	row2 := paneNameRow(lines, "pane-2")
	if row1 != row2 {
		t.Errorf("capture: pane-1 (row %d) and pane-2 (row %d) should be on same row", row1, row2)
	}

	// pane-3 should be below both
	row3 := paneNameRow(lines, "pane-3")
	if row3 <= row1 {
		t.Errorf("capture: pane-3 (row %d) should be below pane-1/pane-2 (row %d)", row3, row1)
	}

	// Horizontal border from root split should be between top and bottom
	hBorderRow := -1
	for i, line := range lines {
		count := 0
		for _, r := range line {
			if r == '─' || r == '┼' || r == '┬' || r == '┴' {
				count++
			}
		}
		if count > 10 {
			hBorderRow = i
			break
		}
	}
	if hBorderRow < 0 {
		t.Fatal("capture: no horizontal border found for root split")
	}
	if hBorderRow <= row1 || hBorderRow >= row3 {
		t.Errorf("capture: border (row %d) should be between top panes (row %d) and pane-3 (row %d)", hBorderRow, row1, row3)
	}

	// pane-1 should be left of pane-2
	col1 := paneNameCol(lines, "pane-1")
	col2 := paneNameCol(lines, "pane-2")
	if col1 >= col2 {
		t.Errorf("capture: pane-1 (col %d) should be left of pane-2 (col %d)", col1, col2)
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
		// The character at the border column should be a border character
		if !isBorderRune(runes[col]) {
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

	// Split active pane 3 times (4 panes total, all side by side)
	for i := 0; i < 3; i++ {
		h.sendKeys("C-a", "\\")
		h.waitFor(fmt.Sprintf("[pane-%d]", i+2), 3*time.Second)
	}

	// All 4 pane names should be on the same row
	lines := h.contentLines()
	row0 := lines[0]
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("[pane-%d]", i)
		if !strings.Contains(row0, name) {
			t.Errorf("%s not on first row\nRow 0: %s", name, row0)
		}
	}

	// 3 vertical borders
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

func TestCapturePane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type a distinctive string
	h.sendKeys("e", "c", "h", "o", " ", "O", "U", "T", "P", "U", "T", "M", "A", "R", "K", "E", "R", "Enter")
	h.waitFor("OUTPUTMARKER", 3*time.Second)

	output := h.runCmd("capture", "pane-1")
	if !strings.Contains(output, "OUTPUTMARKER") {
		t.Errorf("amux capture <pane> should contain OUTPUTMARKER, got:\n%s", output)
	}
}

func TestSpawn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("spawn", "--name", "test-agent", "--task", "TASK-42")
	if !strings.Contains(output, "test-agent") {
		t.Errorf("spawn should report agent name, got:\n%s", output)
	}

	// Should appear in list with metadata
	h.waitFor("[test-agent]", 3*time.Second)
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-agent") {
		t.Errorf("list should contain test-agent, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "TASK-42") {
		t.Errorf("list should contain TASK-42, got:\n%s", listOut)
	}
}

func TestMinimizeRestore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Minimize pane-1
	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Errorf("minimize should confirm, got:\n%s", output)
	}

	// pane-1 status should still be visible but pane should be small
	time.Sleep(500 * time.Millisecond)
	h.assertScreen("pane-1 still visible after minimize", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	// Restore pane-1
	output = h.runCmd("restore", "pane-1")
	if !strings.Contains(output, "Restored") {
		t.Errorf("restore should confirm, got:\n%s", output)
	}

	// Both panes should be visible with reasonable sizes
	time.Sleep(500 * time.Millisecond)
	h.assertScreen("both panes visible after restore", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestKill(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Kill pane-2
	output := h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Errorf("kill should confirm, got:\n%s", output)
	}

	// pane-2 should disappear
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after kill")
	}

	// pane-1 should remain
	h.assertScreen("pane-1 should remain after kill", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	// List should show only pane-1
	listOut := h.runCmd("list")
	if strings.Contains(listOut, "pane-2") {
		t.Errorf("list should not contain pane-2 after kill, got:\n%s", listOut)
	}
}

func TestNamedSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// The harness starts amux with -s <session>. Verify the session name
	// appears in the global status bar.
	h.assertScreen("session name in status bar", func(s string) bool {
		return strings.Contains(s, h.session)
	})

	// List via -s flag should work
	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list with -s should work, got:\n%s", output)
	}
}

func TestTerminalResize(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split vertically
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Resize the tmux pane (simulates terminal resize → SIGWINCH)
	exec.Command("tmux", "resize-pane", "-t", h.session, "-x", "120", "-y", "40").Run()
	time.Sleep(1 * time.Second)

	// After resize, both panes should still be visible
	h.assertScreen("both panes visible after resize", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	// Border should still exist
	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("vertical border missing after resize")
	}

	// Border should be roughly in the middle of the new width
	if col < 40 || col > 80 {
		t.Errorf("border at col %d, expected near middle of 120-wide terminal", col)
	}
}

func TestCtrlACtrlA(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Ctrl-a Ctrl-a should forward a literal Ctrl-a (0x01) to the shell,
	// NOT trigger an amux command. The shell may display it as ^A.
	// Verify: amux stays running (not detached/split) and the byte reaches the pane.
	h.sendKeys("C-a", "C-a")
	time.Sleep(300 * time.Millisecond)

	// amux should still be running (status bar visible)
	h.assertScreen("amux still running after Ctrl-a Ctrl-a", func(s string) bool {
		return strings.Contains(s, "[pane-") && strings.Contains(s, "amux")
	})
}

func TestOnlyActivePaneBordersColored(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create 3 panes side by side: pane-1 | pane-2 | pane-3
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-3]", 3*time.Second)

	// Focus pane-1 (leftmost) — only border-A (pane-1|pane-2) should be colored,
	// border-B (pane-2|pane-3) should be dim.
	h.sendKeys("C-a", "h")
	time.Sleep(300 * time.Millisecond)
	h.sendKeys("C-a", "h")
	time.Sleep(500 * time.Millisecond)

	// Capture with ANSI escapes preserved
	out, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		t.Fatalf("capture-pane -e: %v", err)
	}

	// Extract ANSI color escapes preceding each │ on a middle content row.
	// Split the line by │ — the ANSI escape at the end of each segment
	// is the color used for the following border character.
	colorLine := pickContentLine(string(out))
	borders := extractBorderColors(colorLine)

	if len(borders) < 2 {
		t.Fatalf("expected 2 borders, found %d in line: %q", len(borders), colorLine)
	}

	// Border-A (adjacent to active pane-1) and border-B (between pane-2
	// and pane-3) should have DIFFERENT colors. Border-A is colored,
	// border-B is dim.
	if borders[0] == borders[1] {
		t.Errorf("borders should have different colors: border-A=%s, border-B=%s", borders[0], borders[1])
	}
}

func TestFocusByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create two panes side by side
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// pane-2 should be active after split
	h.assertScreen("pane-2 active after split", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-2]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})

	// Focus pane-1 by name via CLI
	output := h.runCmd("focus", "pane-1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)

	// pane-1 should now be active
	h.assertScreen("pane-1 active after focus by name", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})

	// pane-2 should be inactive
	h.assertScreen("pane-2 inactive after focus by name", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-2]") && strings.Contains(line, "○") {
				return true
			}
		}
		return false
	})
}

func TestFocusByID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create two panes
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Focus pane-1 by numeric ID
	output := h.runCmd("focus", "1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus by ID should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)

	// pane-1 should be active
	h.assertScreen("pane-1 active after focus by ID", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})
}

func TestFocusNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Try to focus a non-existent pane
	output := h.runCmd("focus", "nonexistent")
	if !strings.Contains(output, "not found") {
		t.Errorf("focus of nonexistent pane should report error, got:\n%s", output)
	}
}

// pickContentLine returns a middle content line from ANSI-escaped screen output,
// skipping status lines and empty lines.
func pickContentLine(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) / 2; i < len(lines); i++ {
		if strings.Contains(lines[i], "│") && !strings.Contains(lines[i], "amux") {
			return lines[i]
		}
	}
	// Fallback: any line with │
	for _, line := range lines {
		if strings.Contains(line, "│") && !strings.Contains(lines[0], "[pane-") {
			return line
		}
	}
	return ""
}

// extractBorderColors finds each │ in an ANSI-escaped line and returns
// the most recent \033[...m escape sequence before each one.
func extractBorderColors(line string) []string {
	var colors []string
	lastEscape := ""
	i := 0
	for i < len(line) {
		// Track ANSI escapes
		if line[i] == '\033' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				lastEscape = line[i : j+1]
				i = j + 1
				continue
			}
		}
		// Check for │ (UTF-8: e2 94 82)
		if i+2 < len(line) && line[i] == '\xe2' && line[i+1] == '\x94' && line[i+2] == '\x82' {
			colors = append(colors, lastEscape)
			i += 3
			continue
		}
		i++
	}
	return colors
}

func TestHotReloadKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type a distinctive string before reload
	h.sendKeys("e", "c", "h", "o", " ", "R", "E", "L", "O", "A", "D", "M", "E", "Enter")
	h.waitFor("RELOADME", 3*time.Second)

	// Trigger hot reload with Ctrl-a r
	h.sendKeys("C-a", "r")

	// Wait for session to be ready
	if !h.waitFor("[pane-", 8*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after Ctrl-a r\nScreen:\n%s", screen)
	}
	time.Sleep(300 * time.Millisecond)

	// Press Enter to submit whatever is on the command line.
	// If Ctrl-a r was NOT consumed (forwarded to shell), \x01 (readline home)
	// + 'r' was sent, putting 'r' in the input. Enter executes 'r' → "not found".
	// If Ctrl-a r WAS consumed (reload happened), the line is empty. Enter does nothing.
	h.sendKeys("Enter")
	time.Sleep(500 * time.Millisecond)

	h.assertScreen("Ctrl-a r should be consumed, not forwarded (no 'not found' error)", func(s string) bool {
		return !strings.Contains(s, "not found")
	})

	// Previously typed text should still be visible (server kept emulator state)
	h.assertScreen("RELOADME visible after hot reload", func(s string) bool {
		return strings.Contains(s, "RELOADME")
	})
}

func TestHotReloadAutoDetect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type a distinctive string
	h.sendKeys("e", "c", "h", "o", " ", "A", "U", "T", "O", "R", "L", "D", "Enter")
	h.waitFor("AUTORLD", 3*time.Second)

	// Rebuild the binary (triggers fsnotify → auto-reload)
	out, err := exec.Command("go", "build", "-o", amuxBin, "..").CombinedOutput()
	if err != nil {
		t.Fatalf("rebuilding amux binary: %v\n%s", err, out)
	}

	// After auto-reload, the session should continue
	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	// Previously typed text should still be visible
	h.assertScreen("AUTORLD visible after auto-reload", func(s string) bool {
		return strings.Contains(s, "AUTORLD") && strings.Contains(s, "[pane-")
	})
}

func TestJunctionNotColoredOnInactiveBorder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Build layout:
	//   pane-1 │ pane-2      (left column has 3 stacked panes,
	//   ───────┤              right column is a single pane)
	//   pane-3 │ pane-2
	//   ───────┤
	//   pane-4 │ pane-2
	//
	// Vertical split: pane-1 left, pane-2 right
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Focus left pane (pane-1) and split horizontally twice
	h.sendKeys("C-a", "h")
	time.Sleep(300 * time.Millisecond)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-4]", 3*time.Second)

	// pane-4 is active (bottom-left). The vertical border has junction
	// characters (┤) where horizontal borders meet it. The junction at
	// the TOP horizontal border (between pane-1 and pane-3) is NOT
	// adjacent to pane-4, so it should be DIM.

	// Capture with ANSI escapes
	out, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		t.Fatalf("capture-pane -e: %v", err)
	}
	lines := strings.Split(string(out), "\n")

	// Find horizontal border rows (contain ─ characters).
	// With 3 stacked panes on the left, there are 2 horizontal borders.
	var hBorderRows []int
	for i, line := range lines {
		if strings.Contains(line, "─") && !isGlobalBar(line) {
			hBorderRows = append(hBorderRows, i)
		}
	}
	if len(hBorderRows) < 2 {
		t.Fatalf("expected 2 horizontal border rows, found %d\nScreen:\n%s",
			len(hBorderRows), string(out))
	}

	// Extract the color of the junction character on each horizontal border row.
	// The junction is the box-drawing character at the vertical border column
	// on that row (e.g., ┤, ├, ┼).
	topJunctionColor := extractJunctionColor(lines[hBorderRows[0]])
	bottomJunctionColor := extractJunctionColor(lines[hBorderRows[1]])

	if topJunctionColor == "" || bottomJunctionColor == "" {
		t.Fatalf("could not extract junction colors: top=%q bottom=%q\n  topLine: %q\n  bottomLine: %q",
			topJunctionColor, bottomJunctionColor,
			lines[hBorderRows[0]], lines[hBorderRows[1]])
	}

	// The bottom junction (between pane-3 and pane-4) IS adjacent to
	// the active pane-4, so it should be colored. The top junction
	// (between pane-1 and pane-3) is NOT adjacent to pane-4, so it
	// should be DIM. They must have DIFFERENT colors.
	if topJunctionColor == bottomJunctionColor {
		t.Errorf("top junction (pane-1/pane-3 border, row %d) should be dim but has same color as bottom junction (pane-3/pane-4 border, row %d):\n  top:    %s\n  bottom: %s",
			hBorderRows[0], hBorderRows[1], topJunctionColor, bottomJunctionColor)
	}
}

// extractJunctionColor finds the first junction box-drawing character
// (┤, ├, ┼, ┬, ┴) in an ANSI-escaped line and returns the most recent
// ANSI escape sequence before it.
func extractJunctionColor(line string) string {
	lastEscape := ""
	i := 0
	for i < len(line) {
		// Track ANSI escapes
		if line[i] == '\033' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				lastEscape = line[i : j+1]
				i = j + 1
				continue
			}
		}
		// Check for junction box-drawing characters (3-byte UTF-8)
		if i+2 < len(line) && line[i] == '\xe2' && line[i+1] == '\x94' {
			b := line[i+2]
			// ┤=\xa4  ├=\x9c  ┼=\xbc  ┬=\xac  ┴=\xb4
			if b == '\xa4' || b == '\x9c' || b == '\xbc' || b == '\xac' || b == '\xb4' {
				return lastEscape
			}
		}
		i++
	}
	return ""
}

func TestVerticalBorderPartialColor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Vertical split then horizontal split on the right side:
	// pane-1 (left) | pane-2 (top-right)
	//               | pane-3 (bottom-right, active)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)

	// pane-3 is active (bottom-right). The vertical border between
	// pane-1 and the right column should be colored only in the bottom
	// half (adjacent to pane-3), and dim in the top half (adjacent to pane-2).

	// Capture with ANSI escapes
	out, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		t.Fatalf("capture-pane -e: %v", err)
	}

	// Find a row in the top half and bottom half of the vertical border.
	// The horizontal border between pane-2 and pane-3 should be roughly
	// in the middle. Check border colors above and below it.
	lines := strings.Split(string(out), "\n")

	// Find the horizontal border row (has ─ characters)
	hBorderRow := -1
	for i, line := range lines {
		if strings.Contains(line, "─") && !isGlobalBar(line) {
			hBorderRow = i
			break
		}
	}
	if hBorderRow < 0 {
		t.Fatal("no horizontal border found")
	}

	// Get the border color from a row ABOVE the horizontal border (pane-1 | pane-2)
	// and from a row BELOW it (pane-1 | pane-3)
	topRow := hBorderRow - 2
	bottomRow := hBorderRow + 2
	if topRow < 0 {
		topRow = 1
	}
	if bottomRow >= len(lines) {
		bottomRow = len(lines) - 2
	}

	topColors := extractBorderColors(lines[topRow])
	bottomColors := extractBorderColors(lines[bottomRow])

	if len(topColors) == 0 || len(bottomColors) == 0 {
		t.Fatalf("no border colors found: top=%v bottom=%v", topColors, bottomColors)
	}

	// Top border should be dim (pane-2 is not active)
	// Bottom border should be colored (pane-3 is active)
	if topColors[0] == bottomColors[0] {
		t.Errorf("vertical border should have different colors above and below:\n  top (row %d): %s\n  bottom (row %d): %s",
			topRow, topColors[0], bottomRow, bottomColors[0])
	}
}

// ---------------------------------------------------------------------------
// Golden rendering tests
// ---------------------------------------------------------------------------

func TestGoldenSinglePane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	frame := extractFrame(h.captureAmux(), h.session)
	assertGolden(t, "single_pane.golden", frame)
}

func TestGoldenVerticalSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Focus pane-1 so active state is deterministic
	h.sendKeys("C-a", "h")
	time.Sleep(300 * time.Millisecond)

	frame := extractFrame(h.captureAmux(), h.session)
	assertGolden(t, "vertical_split.golden", frame)

	// Color golden: pane-1 active, its borders should be Rosewater (R)
	ansi := h.runCmd("capture", "--ansi")
	colorMap := extractColorMap(ansi, 80, 24)
	assertGolden(t, "vertical_split.color", colorMap)
}

func TestGoldenHorizontalSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// pane-2 is active after split (Flamingo)
	frame := extractFrame(h.captureAmux(), h.session)
	assertGolden(t, "horizontal_split.golden", frame)

	ansi := h.runCmd("capture", "--ansi")
	colorMap := extractColorMap(ansi, 80, 24)
	assertGolden(t, "horizontal_split.color", colorMap)
}

func TestGoldenFourPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Build 2x2 grid:
	// pane-1 | pane-2
	// -------+-------
	// pane-3 | pane-4
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)
	h.sendKeys("C-a", "h")
	time.Sleep(300 * time.Millisecond)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-4]", 3*time.Second)

	// pane-4 is active (bottom-left)
	frame := extractFrame(h.captureAmux(), h.session)
	assertGolden(t, "four_pane.golden", frame)

	ansi := h.runCmd("capture", "--ansi")
	colorMap := extractColorMap(ansi, 80, 24)
	assertGolden(t, "four_pane.color", colorMap)
}

// ---------------------------------------------------------------------------
// Server hot-reload tests
// ---------------------------------------------------------------------------

func TestServerHotReload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type text in pane-1
	h.sendKeys("e", "c", "h", "o", " ", "B", "E", "F", "O", "R", "E", "R", "L", "D", "Enter")
	h.waitFor("BEFORERLD", 3*time.Second)

	// Split to create pane-2
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Trigger server reload via CLI command
	h.runCmd("reload-server")

	// Wait for client to reconnect (re-exec)
	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload-server\nScreen:\n%s", screen)
	}

	// Layout should be preserved: both panes visible
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	// PTY should still work: type in pane-2 and verify
	h.sendKeys("e", "c", "h", "o", " ", "A", "F", "T", "E", "R", "R", "L", "D", "Enter")
	if !h.waitFor("AFTERRLD", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("PTY should work after reload\nScreen:\n%s", screen)
	}

	// List should show both panes
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-1") || !strings.Contains(listOut, "pane-2") {
		t.Errorf("list should show both panes after reload, got:\n%s", listOut)
	}
}

func TestServerAutoReload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type text in pane-1
	h.sendKeys("e", "c", "h", "o", " ", "S", "R", "V", "A", "U", "T", "O", "Enter")
	h.waitFor("SRVAUTO", 3*time.Second)

	// Rebuild binary (triggers server-side binary watcher → auto-reload)
	out, err := exec.Command("go", "build", "-o", amuxBin, "..").CombinedOutput()
	if err != nil {
		t.Fatalf("rebuilding amux binary: %v\n%s", err, out)
	}

	// Wait for session to recover (both server and client reload)
	if !h.waitFor("[pane-", 15*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	// Previously typed text should still be visible
	if !h.waitFor("SRVAUTO", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("SRVAUTO should be visible after server auto-reload\nScreen:\n%s", screen)
	}
}

func TestServerReloadWithMinimizedPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Minimize pane-1
	h.runCmd("minimize", "pane-1")
	time.Sleep(500 * time.Millisecond)

	// Verify minimized state before reload
	statusBefore := h.runCmd("status")
	if !strings.Contains(statusBefore, "1 minimized") {
		t.Fatalf("expected 1 minimized pane before reload, got:\n%s", statusBefore)
	}

	// Trigger server reload
	h.runCmd("reload-server")

	// Wait for recovery
	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}

	// Wait for both panes to be visible
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	// Minimized state should be preserved
	statusAfter := h.runCmd("status")
	if !strings.Contains(statusAfter, "1 minimized") {
		t.Errorf("minimized state should be preserved after reload, got:\n%s", statusAfter)
	}
}

func TestServerReloadBorderColors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split to get two panes with a border
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Focus pane-1 so active state is deterministic
	h.sendKeys("C-a", "h")
	time.Sleep(500 * time.Millisecond)

	// Capture border colors BEFORE reload (via tmux capture-pane -e for ANSI)
	outBefore, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		t.Fatalf("capture-pane -e before: %v", err)
	}
	colorsBefore := extractBorderColors(pickContentLine(string(outBefore)))

	// Reload server
	h.runCmd("reload-server")

	// Wait for recovery
	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}
	time.Sleep(500 * time.Millisecond)

	// Capture border colors AFTER reload
	outAfter, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		t.Fatalf("capture-pane -e after: %v", err)
	}
	colorsAfter := extractBorderColors(pickContentLine(string(outAfter)))

	if len(colorsBefore) == 0 {
		t.Fatalf("no border colors found before reload\nScreen:\n%s", string(outBefore))
	}
	if len(colorsAfter) == 0 {
		t.Fatalf("no border colors found after reload\nScreen:\n%s", string(outAfter))
	}

	// Border colors should match before and after reload
	if colorsBefore[0] != colorsAfter[0] {
		t.Errorf("border color changed after reload:\n  before: %s\n  after:  %s", colorsBefore[0], colorsAfter[0])
	}
}

func TestServerReloadTUIRedraw(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Write a small TUI script that enters alternate screen buffer,
	// draws a marker, and redraws on SIGWINCH — simulates Claude Code, vim, etc.
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-tui-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
printf '\033[?1049h'
draw() { printf '\033[2J\033[H'; echo TUIMARK_OK; }
trap draw WINCH
draw
while true; do sleep 60; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	// Run the TUI script in the pane
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("TUIMARK_OK", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("TUI script did not start\nScreen:\n%s", screen)
	}

	// Reload server
	h.runCmd("reload-server")

	// Wait for recovery + SIGWINCH-triggered redraw
	// The server nudges PTY sizes after reload, triggering SIGWINCH
	// which causes the TUI script to redraw cleanly
	if !h.waitFor("TUIMARK_OK", 15*time.Second) {
		screen := h.capture()
		t.Fatalf("TUI marker should be visible after reload (SIGWINCH redraw)\nScreen:\n%s", screen)
	}
}

func TestCapture(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type a distinctive string so it appears in the pane
	h.sendKeys("e", "c", "h", "o", " ", "S", "C", "R", "E", "E", "N", "C", "A", "P", "Enter")
	h.waitFor("SCREENCAP", 3*time.Second)

	// amux capture should return the full composited screen with pane content
	out := h.runCmd("capture")
	if !strings.Contains(out, "SCREENCAP") {
		t.Errorf("amux capture should contain typed text, got:\n%s", out)
	}
	// Should include the pane status line
	if !strings.Contains(out, "[pane-") {
		t.Errorf("amux capture should contain pane status, got:\n%s", out)
	}
	// Should include the global session bar
	if !strings.Contains(out, "amux") {
		t.Errorf("amux capture should contain global bar, got:\n%s", out)
	}
}

func TestCaptureWithSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split and type distinctive text in each pane
	h.sendKeys("e", "c", "h", "o", " ", "L", "E", "F", "T", "P", "A", "N", "E", "Enter")
	h.waitFor("LEFTPANE", 3*time.Second)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("e", "c", "h", "o", " ", "R", "I", "G", "H", "T", "P", "A", "N", "E", "Enter")
	h.waitFor("RIGHTPANE", 3*time.Second)

	out := h.runCmd("capture")
	if !strings.Contains(out, "LEFTPANE") {
		t.Errorf("amux capture should contain left pane text, got:\n%s", out)
	}
	if !strings.Contains(out, "RIGHTPANE") {
		t.Errorf("amux capture should contain right pane text, got:\n%s", out)
	}
	// Both pane status lines should appear
	if !strings.Contains(out, "[pane-1]") || !strings.Contains(out, "[pane-2]") {
		t.Errorf("amux capture should contain both pane names, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Mouse support tests
// ---------------------------------------------------------------------------

func TestMouseClickFocus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a vertical split (left | right)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// pane-2 should be active (focus goes to new pane after split)
	h.waitForFunc(func(s string) bool {
		return isActivePaneLine(s, "pane-2")
	}, 3*time.Second)

	// Find the approximate center of pane-1 (left half of 80-col terminal)
	// pane-1 occupies roughly columns 1-39, pane-2 occupies columns 41-80
	// Click at column 10, row 5 (1-based) — well inside pane-1
	h.clickAt(10, 5)

	// pane-1 should now be active
	if !h.waitForFunc(func(s string) bool {
		return isActivePaneLine(s, "pane-1")
	}, 3*time.Second) {
		t.Errorf("after clicking left pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}

	// Click on pane-2 (column 60, row 5) to switch back
	h.clickAt(60, 5)

	if !h.waitForFunc(func(s string) bool {
		return isActivePaneLine(s, "pane-2")
	}, 3*time.Second) {
		t.Errorf("after clicking right pane, pane-2 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseClickFocusHorizontalSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a horizontal split (top / bottom)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// pane-2 should be active (bottom pane, after split)
	h.waitForFunc(func(s string) bool {
		return isActivePaneLine(s, "pane-2")
	}, 3*time.Second)

	// Click at top of screen (row 3) — inside pane-1
	h.clickAt(40, 3)

	if !h.waitForFunc(func(s string) bool {
		return isActivePaneLine(s, "pane-1")
	}, 3*time.Second) {
		t.Errorf("after clicking top pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseBorderDrag(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create vertical split
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Find the border column via capture
	borderCol := h.captureAmuxVerticalBorderCol()
	if borderCol < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	// Drag the border 5 columns to the right
	// Border is at borderCol (0-based in amux), need 1-based for SGR
	dragDelta := 5
	h.dragBorder(borderCol+1, 10, borderCol+1+dragDelta, 10)
	time.Sleep(300 * time.Millisecond)

	// Border should have moved to the right
	newBorderCol := h.captureAmuxVerticalBorderCol()
	if newBorderCol < 0 {
		t.Fatalf("no vertical border found after drag.\nScreen:\n%s", h.captureAmux())
	}
	if newBorderCol <= borderCol {
		t.Errorf("border should have moved right: was at %d, now at %d.\nScreen:\n%s",
			borderCol, newBorderCol, h.captureAmux())
	}
}

func TestMouseScrollWheel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Generate enough output to have scrollback
	for i := 0; i < 30; i++ {
		h.sendKeys(fmt.Sprintf("echo line-%d", i), "Enter")
		time.Sleep(30 * time.Millisecond)
	}
	h.waitFor("line-29", 3*time.Second)

	// Verify line-29 is visible
	screen := h.capture()
	if !strings.Contains(screen, "line-29") {
		t.Fatalf("expected line-29 visible before scroll.\nScreen:\n%s", screen)
	}

	// Scroll up at center of the single pane (40, 12)
	// Note: scroll wheel support requires the application in the pane to handle
	// mouse events, or amux to convert scroll to up/down arrow keys.
	// For now, we just verify the scroll event doesn't crash amux.
	h.scrollAt(40, 12, true)
	h.scrollAt(40, 12, true)
	h.scrollAt(40, 12, true)
	time.Sleep(200 * time.Millisecond)

	// amux should still be responsive
	if !h.waitFor("[pane-", 3*time.Second) {
		t.Errorf("amux should still be running after scroll.\nScreen:\n%s", h.capture())
	}
}

// isActivePaneLine returns true if the captured screen shows the named pane
// with the active indicator (● [name]).
func isActivePaneLine(screen, paneName string) bool {
	// In the raw tmux capture, the bullet character may render differently.
	// Check that the pane name appears and is the active one by verifying
	// the screen contains the active indicator pattern.
	// The amux capture (server-side) is more reliable for this.
	target := "[" + paneName + "]"
	for _, line := range strings.Split(screen, "\n") {
		idx := strings.Index(line, target)
		if idx < 0 {
			continue
		}
		// Check for active indicator (●) before the name on the same line
		prefix := line[:idx]
		if strings.Contains(prefix, "●") {
			return true
		}
	}
	return false
}
