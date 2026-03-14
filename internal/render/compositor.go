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
func (c *Compositor) RenderFull(root *mux.LayoutCell, activePane *mux.Pane) []byte {
	var buf strings.Builder

	// Hide cursor during render to prevent flicker
	buf.WriteString(HideCursor)
	// Clear screen
	buf.WriteString(ClearAll)

	// Count panes for global bar
	paneCount := 0

	// Render each pane's status line and content
	root.Walk(func(cell *mux.LayoutCell) {
		if cell.Pane == nil {
			return
		}
		paneCount++

		isActive := activePane != nil && activePane.ID == cell.Pane.ID

		// Per-pane status line
		renderPaneStatus(&buf, cell, isActive)

		// Pane content (shifted down by status line)
		rendered := cell.Pane.RenderScreen()
		c.blitPane(&buf, cell, rendered)
	})

	// Draw borders with proper junction characters
	bm := buildBorderMap(root, c.width, c.height)
	renderBorders(&buf, bm, root, activePane)

	// Global status bar at bottom
	renderGlobalBar(&buf, c.sessionName, paneCount, c.width, c.height-1)

	// Restore cursor to active pane's cursor position
	if activePane != nil {
		cell := root.FindPane(activePane.ID)
		if cell != nil {
			col, row := activePane.CursorPos()
			absRow := cell.Y + mux.StatusLineRows + row + 1
			absCol := cell.X + col + 1
			buf.WriteString(CursorTo(absRow, absCol))
		}
	}

	// Show cursor
	buf.WriteString(ShowCursor)

	return []byte(buf.String())
}

// blitPane writes a pane's rendered content below its status line.
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
			buf.WriteString(line)
		}
	}
}

// activePaneColor returns the ANSI color for the active pane's border.
func activePaneColor(p *mux.Pane) string {
	if p.Meta.Color != "" {
		return hexToANSI(p.Meta.Color)
	}
	return BlueFg
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
