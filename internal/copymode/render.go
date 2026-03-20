package copymode

import (
	"strconv"
	"strings"
)

// ANSI escapes for copy mode highlighting.
const (
	reverseOn  = "\033[7m"  // reverse video (cursor character)
	reverseOff = "\033[27m" // normal video

	selectionBg  = "\033[44m" // blue background (selection)
	selectionOff = "\033[49m" // reset background

	matchBg        = "\033[43m"    // yellow background (search match)
	matchCurrentBg = "\033[43;1m"  // yellow background + bold (current match)
	matchOff       = "\033[49;22m" // reset background + bold
)

// style flags for per-character highlighting.
type charStyle uint8

const (
	styleNone         charStyle = 0
	styleSelection    charStyle = 1 << 0
	styleMatch        charStyle = 1 << 1
	styleCurrentMatch charStyle = 1 << 2
	styleCursor       charStyle = 1 << 3
)

// RenderViewport returns the viewport content as a newline-separated string
// (no trailing newline), suitable for the compositor's blitPane.
func (cm *CopyMode) RenderViewport() string {
	total := cm.TotalLines()
	firstVisible := max(0, total-cm.height-cm.oy)

	// Precompute normalized selection range for highlighting.
	selStartY, selStartX, selEndY, selEndX := cm.normalizedSelection()

	lines := make([]string, cm.height)
	for row := 0; row < cm.height; row++ {
		absIdx := firstVisible + row
		var line string
		if absIdx < total {
			line = cm.lineText(absIdx)
		}

		// Pad or truncate to viewport width.
		line = padOrTruncate(line, cm.width)
		runes := []rune(line)

		// Build per-character style flags on the plain text, then render
		// all ANSI escapes in a single pass. This avoids corruption from
		// earlier highlighting inserting escapes that shift column indices.
		styles := make([]charStyle, len(runes))

		// Mark search matches.
		for i, m := range cm.matches {
			if m.LineIdx != absIdx {
				continue
			}
			end := min(m.Col+m.Len, len(runes))
			flag := styleMatch
			if i == cm.matchIdx {
				flag = styleCurrentMatch
			}
			for col := m.Col; col < end; col++ {
				styles[col] |= flag
			}
		}

		// Mark selection.
		if cm.selecting && absIdx >= selStartY && absIdx <= selEndY {
			colStart, colEnd := 0, len(runes)
			switch {
			case cm.rectSelect:
				colStart = min(selStartX, len(runes))
				colEnd = min(selEndX+1, len(runes))
			default:
				if absIdx == selStartY {
					colStart = selStartX
				}
				if absIdx == selEndY {
					colEnd = min(selEndX+1, len(runes))
				}
			}
			for col := colStart; col < colEnd; col++ {
				styles[col] |= styleSelection
			}
		}

		// Mark cursor character.
		if row == cm.cy && cm.cx < len(runes) {
			styles[cm.cx] |= styleCursor
		}

		// Render with ANSI escapes.
		lines[row] = renderStyledLine(runes, styles)
	}

	return strings.Join(lines, "\n")
}

// renderStyledLine emits a line with ANSI escapes based on per-character styles.
// Minimizes escape sequences by tracking the current style state.
func renderStyledLine(runes []rune, styles []charStyle) string {
	var buf strings.Builder
	buf.Grow(len(runes) * 2) // rough estimate

	var cur charStyle
	for i, r := range runes {
		s := styles[i]
		if s != cur {
			// Close previous style.
			if cur != styleNone {
				if cur&styleCursor != 0 {
					buf.WriteString(reverseOff)
				}
				if cur&(styleSelection|styleMatch|styleCurrentMatch) != 0 {
					buf.WriteString(matchOff)
				}
			}
			// Open new style. Cursor (reverse video) takes visual priority;
			// search match bg takes priority over selection bg.
			if s&styleCursor != 0 {
				buf.WriteString(reverseOn)
			} else if s&styleCurrentMatch != 0 {
				buf.WriteString(matchCurrentBg)
			} else if s&styleMatch != 0 {
				buf.WriteString(matchBg)
			} else if s&styleSelection != 0 {
				buf.WriteString(selectionBg)
			}
			cur = s
		}
		buf.WriteRune(r)
	}
	// Close trailing style.
	if cur&styleCursor != 0 {
		buf.WriteString(reverseOff)
	}
	if cur&(styleSelection|styleMatch|styleCurrentMatch) != 0 {
		buf.WriteString(matchOff)
	}
	return buf.String()
}

// SearchBarText returns the search prompt to display in the status bar.
// Returns empty string when not actively searching.
func (cm *CopyMode) SearchBarText() string {
	var parts []string
	switch cm.prompt {
	case promptSearchForward:
		parts = append(parts, "/"+cm.promptBuf)
	case promptSearchBackward:
		parts = append(parts, "?"+cm.promptBuf)
	case promptGotoLine:
		parts = append(parts, ":"+cm.promptBuf)
	}
	if cm.pendingCount > 0 && cm.prompt == promptNone {
		parts = append(parts, strconv.Itoa(cm.pendingCount))
	}
	if cm.showPosition {
		parts = append(parts, "["+strconv.Itoa(cm.oy)+"/"+strconv.Itoa(cm.maxOY())+"]")
	}
	return strings.Join(parts, " ")
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
