package render

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/mux"
)

// WindowInfo holds metadata about a window for rendering in the global bar.
type WindowInfo struct {
	Index    int
	Name     string
	IsActive bool
	Panes    int
}

// Compositor composes pane content into terminal output.
type Compositor struct {
	width       int
	height      int
	sessionName string
	windows     []WindowInfo

	// Cached border map — rebuilt only when layout root changes.
	cachedBorderMap  *borderMap
	cachedBorderRoot *mux.LayoutCell
}

// SetWindows sets the window list for the global bar.
func (c *Compositor) SetWindows(windows []WindowInfo) {
	c.windows = windows
}

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(width, height int, sessionName string) *Compositor {
	return &Compositor{width: width, height: height, sessionName: sessionName}
}

// Resize updates the compositor's terminal dimensions.
func (c *Compositor) Resize(width, height int) {
	c.width = width
	c.height = height
	// Invalidate border map cache — dimensions changed.
	c.cachedBorderMap = nil
	c.cachedBorderRoot = nil
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
func ClearScreen() string {
	return ClearAll + CursorHome
}

// RenderFull composes all panes, status lines, and borders into ANSI output.
// lookup maps pane IDs to their rendering data. Client provides emulator-backed
// adapters; server could provide Pane wrappers.
func (c *Compositor) RenderFull(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) string {
	var buf strings.Builder
	buf.Grow(c.width * c.height * 4) // pre-allocate for typical ANSI output

	// Hide cursor during render to prevent flicker
	buf.WriteString(HideCursor)
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
		rendered := pd.RenderScreen(isActive)
		c.blitPane(&buf, cell, rendered)
	})

	// Draw borders with proper junction characters.
	// Cache the border map — it only changes when the layout root changes.
	// Pointer identity is sufficient: RebuildLayout always allocates a new root,
	// and Resize() explicitly invalidates the cache.
	if c.cachedBorderMap == nil || c.cachedBorderRoot != root {
		c.cachedBorderMap = buildBorderMap(root, c.width, c.height)
		c.cachedBorderRoot = root
	}
	renderBorders(&buf, c.cachedBorderMap, root, activePaneID, activeColor)

	// Global status bar at bottom
	renderGlobalBar(&buf, c.sessionName, paneCount, c.width, c.height-1, c.windows)

	// Position cursor and respect the active pane's cursor visibility state.
	// If the application has hidden its cursor (e.g. during streaming output),
	// keep it hidden rather than showing it at a stale position.
	// If the application renders its own block cursor (reverse-video space),
	// hide the terminal cursor to avoid showing two cursors.
	c.renderCursor(&buf, root, activePaneID, lookup)

	return buf.String()
}

// renderCursor positions the terminal cursor at the active pane's cursor
// location, or hides it when the active pane is minimized, has a hidden
// cursor, or renders its own block cursor.
func (c *Compositor) renderCursor(buf *strings.Builder, root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) {
	if activePaneID == 0 {
		buf.WriteString(ShowCursor)
		return
	}
	cell := root.FindByPaneID(activePaneID)
	if cell == nil {
		buf.WriteString(ShowCursor)
		return
	}
	pd := lookup(activePaneID)
	if pd == nil {
		buf.WriteString(ShowCursor)
		return
	}
	if pd.Minimized() || pd.CursorHidden() || pd.HasCursorBlock() {
		return // keep cursor hidden (HideCursor was written at start of render)
	}
	col, row := pd.CursorPos()
	absRow := cell.Y + mux.StatusLineRows + row + 1
	absCol := cell.X + col + 1
	writeCursorTo(buf, absRow, absCol)
	buf.WriteString(ShowCursor)
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
		writeCursorTo(buf, row, cell.X+1)
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

		// Skip escape sequences — zero visible width
		if b == '\033' && i+1 < len(line) {
			next := line[i+1]
			// CSI: \033[ params final_byte
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
			// OSC: \033] ... BEL(\007) or ST(\033\\)
			if next == ']' {
				j := i + 2
				for j < len(line) {
					if line[j] == '\007' {
						j++
						break
					}
					if line[j] == '\033' && j+1 < len(line) && line[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}
			i += 2
			continue
		}

		// Skip control characters
		if b < 0x20 {
			i++
			continue
		}

		if visible >= maxWidth {
			return line[:i]
		}
		visible++

		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
	}
	return line
}

// hexColorCache maps hex color strings (e.g. "f5e0dc") to precomputed
// ANSI truecolor escapes. The cache is package-level since colors are
// drawn from a fixed palette (~14 Catppuccin colors).
var hexColorCache = make(map[string]string)

// hexToANSI converts a 6-digit hex color to an ANSI truecolor escape.
// Results are cached — repeated calls for the same hex value are free.
func hexToANSI(hex string) string {
	if len(hex) < 6 {
		return DimFg
	}
	if cached, ok := hexColorCache[hex]; ok {
		return cached
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	result := fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	hexColorCache[hex] = result
	return result
}
