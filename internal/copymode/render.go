package copymode

import (
	"fmt"
	"strings"
)

// ANSI escapes for copy mode highlighting.
const (
	reverseOn  = "\033[7m"  // reverse video (cursor line)
	reverseOff = "\033[27m" // normal video

	matchBg        = "\033[43m"    // yellow background (search match)
	matchCurrentBg = "\033[43;1m"  // yellow background + bold (current match)
	matchOff       = "\033[49;22m" // reset background + bold
)

// RenderViewport returns the viewport content as a newline-separated string
// (no trailing newline), suitable for the compositor's blitPane.
func (cm *CopyMode) RenderViewport() string {
	total := cm.TotalLines()
	firstVisible := total - cm.height - cm.oy
	if firstVisible < 0 {
		firstVisible = 0
	}

	lines := make([]string, cm.height)
	for row := 0; row < cm.height; row++ {
		absIdx := firstVisible + row
		var line string
		if absIdx < total {
			line = cm.lineText(absIdx)
		}

		// Pad or truncate to viewport width.
		line = padOrTruncate(line, cm.width)

		// Apply search match highlighting before cursor highlight,
		// so cursor reverse-video is applied on top.
		line = cm.highlightMatches(line, absIdx)

		// Cursor line: apply reverse video to the entire line.
		if row == cm.cy {
			line = reverseOn + line + reverseOff
		}

		lines[row] = line
	}

	return strings.Join(lines, "\n")
}

// highlightMatches wraps search match text in ANSI highlight escapes.
func (cm *CopyMode) highlightMatches(line string, absIdx int) string {
	if len(cm.matches) == 0 {
		return line
	}

	// Collect matches on this line.
	var lineMatches []int // indices into cm.matches
	for i, m := range cm.matches {
		if m.LineIdx == absIdx {
			lineMatches = append(lineMatches, i)
		}
	}
	if len(lineMatches) == 0 {
		return line
	}

	// Build the highlighted line by inserting escapes around each match.
	// Process matches right-to-left so byte offsets remain valid.
	runes := []rune(line)
	for j := len(lineMatches) - 1; j >= 0; j-- {
		mi := lineMatches[j]
		m := cm.matches[mi]

		start := m.Col
		end := m.Col + m.Len
		if start >= len(runes) {
			continue
		}
		if end > len(runes) {
			end = len(runes)
		}

		bg := matchBg
		if mi == cm.matchIdx {
			bg = matchCurrentBg
		}

		highlighted := bg + string(runes[start:end]) + matchOff
		runes = append(runes[:start], append([]rune(highlighted), runes[end:]...)...)
	}
	return string(runes)
}

// SearchBarText returns the search prompt to display in the status bar.
// Returns empty string when not actively searching.
func (cm *CopyMode) SearchBarText() string {
	if !cm.searching {
		return ""
	}
	return fmt.Sprintf("/%s", cm.searchBuf)
}

// padOrTruncate ensures s is exactly width characters (rune-based),
// padding with spaces or truncating as needed.
func padOrTruncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return s + strings.Repeat(" ", width-len(runes))
}
