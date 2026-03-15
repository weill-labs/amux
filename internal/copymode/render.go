package copymode

import "strings"

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
	firstVisible := max(0, total-cm.height-cm.oy)

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

	// Build the highlighted line left-to-right, inserting ANSI escapes
	// around each match.
	runes := []rune(line)
	var buf strings.Builder
	pos := 0
	for _, mi := range lineMatches {
		m := cm.matches[mi]
		start := m.Col
		if start >= len(runes) {
			continue
		}
		end := min(m.Col+m.Len, len(runes))

		buf.WriteString(string(runes[pos:start]))
		bg := matchBg
		if mi == cm.matchIdx {
			bg = matchCurrentBg
		}
		buf.WriteString(bg)
		buf.WriteString(string(runes[start:end]))
		buf.WriteString(matchOff)
		pos = end
	}
	buf.WriteString(string(runes[pos:]))
	return buf.String()
}

// SearchBarText returns the search prompt to display in the status bar.
// Returns empty string when not actively searching.
func (cm *CopyMode) SearchBarText() string {
	if !cm.searching {
		return ""
	}
	return "/" + cm.searchBuf
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
