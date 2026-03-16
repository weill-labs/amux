package test

import (
	"strings"
	"testing"
)

func TestOnlyActivePaneBordersColored(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Create 3 panes side by side: pane-1 | pane-2 | pane-3
	h.splitV()
	h.splitV()

	// Focus pane-1 (leftmost)
	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)
	gen = h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	colorLine := pickContentLine(h.captureANSI())
	borders := extractBorderColors(colorLine)

	if len(borders) < 2 {
		t.Fatalf("expected 2 borders, found %d in line: %q", len(borders), colorLine)
	}

	// Border-A (adjacent to active pane-1) and border-B (between pane-2
	// and pane-3) should have DIFFERENT colors.
	if borders[0] == borders[1] {
		t.Errorf("borders should have different colors: border-A=%s, border-B=%s", borders[0], borders[1])
	}
}

func TestJunctionNotColoredOnInactiveBorder(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Build layout:
	//   pane-1 │ pane-2
	//   ───────┤
	//   pane-3 │ pane-2
	//   ───────┤
	//   pane-4 │ pane-2
	h.splitV()

	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)
	h.splitH()
	h.splitH()

	// pane-4 is active (bottom-left). The junction at the TOP horizontal
	// border is NOT adjacent to pane-4, so it should be DIM.
	lines := strings.Split(h.captureANSI(), "\n")

	hBorderRows := findANSIHorizontalBorderRows(lines)
	if len(hBorderRows) < 2 {
		t.Fatalf("expected 2 horizontal border rows, found %d\nScreen:\n%s",
			len(hBorderRows), strings.Join(lines, "\n"))
	}

	topJunctionColor := extractJunctionColor(lines[hBorderRows[0]])
	bottomJunctionColor := extractJunctionColor(lines[hBorderRows[1]])

	if topJunctionColor == "" || bottomJunctionColor == "" {
		t.Fatalf("could not extract junction colors: top=%q bottom=%q\n  topLine: %q\n  bottomLine: %q",
			topJunctionColor, bottomJunctionColor,
			lines[hBorderRows[0]], lines[hBorderRows[1]])
	}

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

// findANSIHorizontalBorderRows returns row indices containing horizontal
// border characters (─) in ANSI-escaped lines, excluding the global bar.
func findANSIHorizontalBorderRows(lines []string) []int {
	var rows []int
	for i, line := range lines {
		if strings.Contains(line, "─") && !isGlobalBar(line) {
			rows = append(rows, i)
		}
	}
	return rows
}

func TestVerticalBorderPartialColor(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Vertical split then horizontal split on the right side:
	// pane-1 (left) | pane-2 (top-right)
	//               | pane-3 (bottom-right, active)
	h.splitV()
	h.splitH()

	lines := strings.Split(h.captureANSI(), "\n")

	hBorderRows := findANSIHorizontalBorderRows(lines)
	if len(hBorderRows) == 0 {
		t.Fatal("no horizontal border found")
	}
	hBorderRow := hBorderRows[0]

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
