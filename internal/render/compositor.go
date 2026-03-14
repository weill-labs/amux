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

	// Draw borders between panes
	c.drawBorders(&buf, root, activePane)

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

// drawBorders draws separator lines between panes.
func (c *Compositor) drawBorders(buf *strings.Builder, cell *mux.LayoutCell, activePane *mux.Pane) {
	if cell.IsLeaf() {
		return
	}

	children := cell.Children
	for i := 0; i < len(children)-1; i++ {
		child := children[i]
		// Pass the two children adjacent to this border for color determination
		left := children[i]
		right := children[i+1]

		if cell.Dir == mux.SplitHorizontal {
			borderX := child.X + child.W
			c.drawVerticalBorder(buf, borderX, cell.Y, cell.H, left, right, activePane)
		} else {
			borderY := child.Y + child.H
			c.drawHorizontalBorder(buf, borderY, cell.X, cell.W, left, right, activePane)
		}
	}

	for _, child := range children {
		c.drawBorders(buf, child, activePane)
	}
}

func (c *Compositor) drawVerticalBorder(buf *strings.Builder, x, y, h int, left, right *mux.LayoutCell, activePane *mux.Pane) {
	// Color each row independently — the leaf pane adjacent to the border
	// may differ at different Y positions (e.g., stacked panes on one side).
	lastColor := ""
	for row := 0; row < h; row++ {
		absY := y + row
		color := borderColorAtY(left, right, absY, activePane)
		if color != lastColor {
			if lastColor != "" {
				buf.WriteString(Reset)
			}
			buf.WriteString(color)
			lastColor = color
		}
		buf.WriteString(CursorTo(absY+1, x+1))
		buf.WriteString("│")
	}
	buf.WriteString(Reset)
}

func (c *Compositor) drawHorizontalBorder(buf *strings.Builder, y, x, w int, top, bottom *mux.LayoutCell, activePane *mux.Pane) {
	// Color each column independently for the same reason as vertical.
	lastColor := ""
	for col := 0; col < w; col++ {
		absX := x + col
		color := borderColorAtX(top, bottom, absX, activePane)
		if color != lastColor {
			if lastColor != "" {
				buf.WriteString(Reset)
			}
			buf.WriteString(color)
			lastColor = color
		}
		buf.WriteString(CursorTo(y+1, absX+1))
		buf.WriteString("─")
	}
	buf.WriteString(Reset)
}

// borderColorAtY returns the border color for a vertical border at a given Y position.
// It finds the leaf pane on each side at that Y and colors only if the active pane
// is one of those leaves.
func borderColorAtY(left, right *mux.LayoutCell, y int, activePane *mux.Pane) string {
	if activePane == nil {
		return DimFg
	}
	leftLeaf := findLeafAtY(left, y)
	rightLeaf := findLeafAtY(right, y)
	if (leftLeaf != nil && leftLeaf.Pane != nil && leftLeaf.Pane.ID == activePane.ID) ||
		(rightLeaf != nil && rightLeaf.Pane != nil && rightLeaf.Pane.ID == activePane.ID) {
		return activePaneColor(activePane)
	}
	return DimFg
}

// borderColorAtX returns the border color for a horizontal border at a given X position.
func borderColorAtX(top, bottom *mux.LayoutCell, x int, activePane *mux.Pane) string {
	if activePane == nil {
		return DimFg
	}
	topLeaf := findLeafAtX(top, x)
	bottomLeaf := findLeafAtX(bottom, x)
	if (topLeaf != nil && topLeaf.Pane != nil && topLeaf.Pane.ID == activePane.ID) ||
		(bottomLeaf != nil && bottomLeaf.Pane != nil && bottomLeaf.Pane.ID == activePane.ID) {
		return activePaneColor(activePane)
	}
	return DimFg
}

func activePaneColor(p *mux.Pane) string {
	if p.Meta.Color != "" {
		return hexToANSI(p.Meta.Color)
	}
	return BlueFg
}

// findLeafAtY returns the leaf cell at a given Y coordinate within a cell subtree.
func findLeafAtY(cell *mux.LayoutCell, y int) *mux.LayoutCell {
	if cell.IsLeaf() {
		if y >= cell.Y && y < cell.Y+cell.H {
			return cell
		}
		return nil
	}
	for _, child := range cell.Children {
		if y >= child.Y && y < child.Y+child.H {
			return findLeafAtY(child, y)
		}
	}
	return nil
}

// findLeafAtX returns the leaf cell at a given X coordinate within a cell subtree.
func findLeafAtX(cell *mux.LayoutCell, x int) *mux.LayoutCell {
	if cell.IsLeaf() {
		if x >= cell.X && x < cell.X+cell.W {
			return cell
		}
		return nil
	}
	for _, child := range cell.Children {
		if x >= child.X && x < child.X+child.W {
			return findLeafAtX(child, x)
		}
	}
	return nil
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
