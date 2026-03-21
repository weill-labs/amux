package copymode

import (
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
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

// RenderViewport returns the viewport content as a newline-separated string
// (no trailing newline), suitable for the compositor's blitPane.
func (cm *CopyMode) RenderViewport() string {
	var buf strings.Builder
	buf.Grow(cm.width * cm.height * 2)

	var prevStyle *uv.Style
	for row := 0; row < cm.height; row++ {
		if row > 0 {
			buf.WriteByte('\n')
		}
		for col := 0; col < cm.width; col++ {
			cell := cm.CellAt(col, row)
			if cell.Width == 0 {
				continue
			}
			if diff := uv.StyleDiff(prevStyle, &cell.Style); diff != "" {
				buf.WriteString(diff)
			}
			styleCopy := cell.Style
			prevStyle = &styleCopy

			char := cell.Char
			buf.WriteString(char)
		}
	}
	if prevStyle != nil {
		if diff := uv.StyleDiff(prevStyle, &uv.Style{}); diff != "" {
			buf.WriteString(diff)
		}
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
