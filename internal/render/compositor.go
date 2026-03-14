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

// PaneContentHeight returns the height available for pane content
// within a layout cell (cell height minus the per-pane status line).
func PaneContentHeight(cellH int) int {
	h := cellH - PaneStatusHeight
	if h < 1 {
		h = 1
	}
	return h
}

// ClearScreen returns ANSI sequences to clear the screen and home the cursor.
func ClearScreen() []byte {
	return []byte("\033[2J\033[H")
}

// RenderFull composes all panes, status lines, and borders into ANSI output.
func (c *Compositor) RenderFull(root *mux.LayoutCell, activePane *mux.Pane) []byte {
	var buf strings.Builder

	// Hide cursor during render to prevent flicker
	buf.WriteString("\033[?25l")
	// Clear screen
	buf.WriteString("\033[2J")

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

		// Pane content (shifted down by PaneStatusHeight)
		rendered := cell.Pane.RenderScreen()
		c.blitPane(&buf, cell, rendered)
	})

	// Draw borders between panes
	c.drawBorders(&buf, root, activePane)

	// Global status bar at bottom
	renderGlobalBar(&buf, c.sessionName, paneCount, c.width, c.height-1)

	// Restore cursor to active pane's cursor position
	if activePane != nil {
		cell := root.FindPane(activePane.ID)
		if cell != nil {
			col, row := activePane.CursorPos()
			// Offset by PaneStatusHeight for the status line
			absRow := cell.Y + PaneStatusHeight + row + 1
			absCol := cell.X + col + 1
			buf.WriteString(fmt.Sprintf("\033[%d;%dH", absRow, absCol))
		}
	}

	// Show cursor
	buf.WriteString("\033[?25h")

	return []byte(buf.String())
}

// blitPane writes a pane's rendered content below its status line.
func (c *Compositor) blitPane(buf *strings.Builder, cell *mux.LayoutCell, rendered string) {
	lines := strings.Split(rendered, "\n")
	contentH := PaneContentHeight(cell.H)

	for i, line := range lines {
		if i >= contentH {
			break
		}
		// Shift down by PaneStatusHeight for the status line
		row := cell.Y + PaneStatusHeight + i + 1
		buf.WriteString(fmt.Sprintf("\033[%d;%dH", row, cell.X+1))
		if len(line) > 0 {
			buf.WriteString(line)
		}
	}
}

// drawBorders draws separator lines between panes.
func (c *Compositor) drawBorders(buf *strings.Builder, cell *mux.LayoutCell, activePane *mux.Pane) {
	if cell.IsLeaf() {
		return
	}

	children := cell.Children
	for i := 0; i < len(children)-1; i++ {
		child := children[i]

		if cell.Dir == mux.SplitHorizontal {
			borderX := child.X + child.W
			c.drawVerticalBorder(buf, borderX, cell.Y, cell.H, cell, activePane)
		} else {
			borderY := child.Y + child.H
			c.drawHorizontalBorder(buf, borderY, cell.X, cell.W, cell, activePane)
		}
	}

	for _, child := range children {
		c.drawBorders(buf, child, activePane)
	}
}

func (c *Compositor) drawVerticalBorder(buf *strings.Builder, x, y, h int, parent *mux.LayoutCell, activePane *mux.Pane) {
	color := borderColor(parent, activePane)
	for row := 0; row < h; row++ {
		buf.WriteString(fmt.Sprintf("\033[%d;%dH", y+row+1, x+1))
		buf.WriteString(color)
		buf.WriteString("│")
	}
	buf.WriteString("\033[0m")
}

func (c *Compositor) drawHorizontalBorder(buf *strings.Builder, y, x, w int, parent *mux.LayoutCell, activePane *mux.Pane) {
	color := borderColor(parent, activePane)
	buf.WriteString(fmt.Sprintf("\033[%d;%dH", y+1, x+1))
	buf.WriteString(color)
	for col := 0; col < w; col++ {
		buf.WriteString("─")
	}
	buf.WriteString("\033[0m")
}

// borderColor returns the active pane's color if the active pane is
// anywhere under this parent, otherwise dim gray.
func borderColor(parent *mux.LayoutCell, activePane *mux.Pane) string {
	if activePane == nil {
		return "\033[38;5;240m"
	}
	if containsPane(parent, activePane.ID) {
		color := activePane.Meta.Color
		if color != "" {
			return hexToANSI(color)
		}
		return "\033[38;5;75m"
	}
	return "\033[38;5;240m"
}

// containsPane returns true if the cell or any descendant contains the pane.
func containsPane(cell *mux.LayoutCell, paneID uint32) bool {
	return cell.FindPane(paneID) != nil
}

// hexToANSI converts a 6-digit hex color to an ANSI truecolor escape.
func hexToANSI(hex string) string {
	if len(hex) < 6 {
		return "\033[38;5;240m"
	}
	var r, g, b int
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}
