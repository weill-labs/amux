package render

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
)

// Compositor composes pane content into terminal output.
type Compositor struct {
	width       int
	height      int
	sessionName string
}

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(width, height int, sessionName string) *Compositor {
	return &Compositor{width: width, height: height, sessionName: sessionName}
}

// Resize updates the compositor's terminal dimensions.
func (c *Compositor) Resize(width, height int) {
	c.width = width
	c.height = height
}

// SetSessionName updates the session name shown in the global bar.
func (c *Compositor) SetSessionName(name string) {
	c.sessionName = name
}

// LayoutHeight returns the height available for the layout tree
// (terminal height minus the global status bar).
func (c *Compositor) LayoutHeight() int {
	return c.height - GlobalBarHeight
}

// ClearScreen returns ANSI sequences to clear the screen and home the cursor.
func ClearScreen() []byte {
	return []byte(ClearAll + CursorHome)
}

// RenderFull composes all panes, status lines, and borders into ANSI output.
// lookup maps pane IDs to their rendering data. Client provides emulator-backed
// adapters; server could provide Pane wrappers.
func (c *Compositor) RenderFull(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) []byte {
	var buf strings.Builder

	// Hide cursor during render to prevent flicker
	buf.WriteString(HideCursor)
	// Clear screen
	buf.WriteString(ClearAll)

	// Count panes for global bar
	paneCount := 0

	// Determine active pane color for borders
	var activeColor string
	if pd := lookup(activePaneID); pd != nil && pd.Color() != "" {
		activeColor = hexToANSI(pd.Color())
	} else {
		activeColor = BlueFg
	}

	// Render each pane's status line and content
	root.Walk(func(cell *mux.LayoutCell) {
		pid := cell.CellPaneID()
		if pid == 0 {
			return
		}
		pd := lookup(pid)
		if pd == nil {
			return
		}
		paneCount++

		isActive := pid == activePaneID

		// Per-pane status line
		renderPaneStatus(&buf, cell, isActive, pd)

		// Pane content (shifted down by status line)
		rendered := pd.RenderScreen()
		c.blitPane(&buf, cell, rendered)
	})

	// Draw borders with proper junction characters
	bm := buildBorderMap(root, c.width, c.height)
	renderBorders(&buf, bm, root, activePaneID, activeColor)

	// Global status bar at bottom
	renderGlobalBar(&buf, c.sessionName, paneCount, c.width, c.height-1)

	// Position cursor and respect the active pane's cursor visibility state.
	// If the application has hidden its cursor (e.g. during streaming output),
	// keep it hidden rather than showing it at a stale position.
	showCursor := true
	if activePaneID != 0 {
		if cell := root.FindByPaneID(activePaneID); cell != nil {
			if pd := lookup(activePaneID); pd != nil {
				if pd.CursorHidden() {
					showCursor = false
				} else {
					col, row := pd.CursorPos()
					absRow := cell.Y + mux.StatusLineRows + row + 1
					absCol := cell.X + col + 1
					buf.WriteString(CursorTo(absRow, absCol))
				}
			}
		}
	}
	if showCursor {
		buf.WriteString(ShowCursor)
	}

	return []byte(buf.String())
}

// blitPane writes a pane's rendered content below its status line.
// Lines are clipped to cell.W visible characters to prevent content
// from bleeding into adjacent panes.
func (c *Compositor) blitPane(buf *strings.Builder, cell *mux.LayoutCell, rendered string) {
	lines := strings.Split(rendered, "\n")
	contentH := mux.PaneContentHeight(cell.H)

	for i, line := range lines {
		if i >= contentH {
			break
		}
		row := cell.Y + mux.StatusLineRows + i + 1
		buf.WriteString(CursorTo(row, cell.X+1))
		if len(line) > 0 {
			buf.WriteString(clipLine(line, cell.W))
		}
	}
}

// clipLine truncates an ANSI-escaped line to at most maxWidth visible
// characters, preserving escape sequences that precede the cutoff.
func clipLine(line string, maxWidth int) string {
	visible := 0
	i := 0
	for i < len(line) {
		b := line[i]

		// Skip ESC sequences (they have zero visible width)
		if b == '\033' && i+1 < len(line) {
			next := line[i+1]
			if next == '[' {
				j := i + 2
				for j < len(line) && line[j] >= 0x20 && line[j] <= 0x3F {
					j++
				}
				if j < len(line) {
					i = j + 1
					continue
				}
			}
			i += 2
			continue
		}

		if b < 0x20 {
			i++
			continue
		}

		// Visible character — check if we've hit the width limit
		if visible >= maxWidth {
			return line[:i]
		}
		visible++

		// Advance past UTF-8 rune
		if b < 0x80 {
			i++
		} else if b < 0xE0 {
			i += 2
		} else if b < 0xF0 {
			i += 3
		} else {
			i += 4
		}
	}
	return line
}

// hexToANSI converts a 6-digit hex color to an ANSI truecolor escape.
func hexToANSI(hex string) string {
	if len(hex) < 6 {
		return DimFg
	}
	var r, g, b int
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}
