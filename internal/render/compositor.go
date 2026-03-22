package render

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/weill-labs/amux/internal/config"
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
	debug       bool // when true, BuildGrid creates grids with Debug=true

	// Cached border map — rebuilt only when layout root changes.
	cachedBorderMap  *borderMap
	cachedBorderRoot *mux.LayoutCell

	// Previous frame's grid for diff rendering. Nil forces full paint.
	prevGrid *ScreenGrid
}

// SetWindows sets the window list for the global bar.
func (c *Compositor) SetWindows(windows []WindowInfo) {
	c.windows = windows
}

// debugDefault controls whether new compositors start with debug mode enabled.
// Tests set this to true in init() so all compositors record OOB grid writes.
var debugDefault bool

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(width, height int, sessionName string) *Compositor {
	return &Compositor{width: width, height: height, sessionName: sessionName, debug: debugDefault}
}

// Resize updates the compositor's terminal dimensions.
func (c *Compositor) Resize(width, height int) {
	c.width = width
	c.height = height
	// Invalidate caches — dimensions changed.
	c.cachedBorderMap = nil
	c.cachedBorderRoot = nil
	c.prevGrid = nil // force full repaint
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

// RenderFullWithOverlay composes all panes, status lines, and borders into ANSI
// output plus optional client-local overlays. lookup maps pane IDs to their
// rendering data. Client provides emulator-backed adapters; server could
// provide Pane wrappers.
//
// When clearScreen is true the entire terminal is erased before drawing. This
// is required after layout changes (panes move/resize) but should be skipped
// for incremental updates (pane output, copy mode navigation) to avoid flicker.
func (c *Compositor) RenderFullWithOverlay(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState, clearScreen ...bool) string {
	var buf strings.Builder
	buf.Grow(c.width * c.height * 4) // pre-allocate for typical ANSI output

	// Hide cursor during render to prevent flicker
	buf.WriteString(HideCursor)
	if len(clearScreen) > 0 && clearScreen[0] {
		buf.WriteString(ClearAll)
	}

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

	if len(overlay.PaneLabels) > 0 {
		renderPaneOverlay(&buf, root, lookup, overlay.PaneLabels)
	}

	// Global status bar at bottom
	renderGlobalBar(&buf, c.sessionName, paneCount, c.width, c.height-1, c.windows, overlay.Message)
	if overlay.Chooser != nil {
		renderChooserOverlay(&buf, c.width, c.height, overlay.Chooser)
	}

	// Position cursor and respect the active pane's cursor visibility state.
	// If the application has hidden its cursor (e.g. during streaming output),
	// keep it hidden rather than showing it at a stale position.
	// If the application renders its own block cursor (reverse-video space),
	// hide the terminal cursor to avoid showing two cursors.
	c.renderCursor(&buf, root, activePaneID, lookup)

	return buf.String()
}

// RenderDiffWithOverlay composes all panes into a cell grid, diffs against the
// previous frame, and returns minimal ANSI output for the changed cells plus
// optional client-local overlays. On the first call (or after Resize), prevGrid
// is nil and every cell is emitted.
func (c *Compositor) RenderDiffWithOverlay(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState) string {
	newGrid := c.buildGridWithOverlay(root, activePaneID, lookup, overlay)
	changes := DiffGrid(c.prevGrid, newGrid)
	c.prevGrid = newGrid

	var buf strings.Builder
	buf.Grow(c.width * c.height) // rough estimate

	buf.WriteString(HideCursor)
	// Reset all styles before emitting diffs. The terminal retains the
	// "current style" from the previous frame's last cell write (typically
	// the global bar with bg=surface0). Without a reset, EmitDiff's first
	// StyleDiff(nil → cell) only sets the needed attributes, leaving stale
	// bg/fg from the prior frame to bleed into content cells.
	if len(changes) > 0 {
		buf.WriteString(Reset)
	}
	buf.WriteString(EmitDiff(changes))

	// Position cursor.
	c.renderCursorDiff(&buf, root, activePaneID, lookup)

	return buf.String()
}

// ClearPrevGrid forces a full repaint on the next RenderDiff call.
func (c *Compositor) ClearPrevGrid() {
	c.prevGrid = nil
}

// PrevGridText returns the previous frame's grid as plain text (no ANSI).
// Each row is newline-separated; trailing spaces are trimmed.
// Returns empty string if no previous grid exists (before first render).
func (c *Compositor) PrevGridText() string {
	if c.prevGrid == nil {
		return ""
	}
	return gridToText(c.prevGrid)
}

// gridToText converts a ScreenGrid to plain text with trailing spaces trimmed.
func gridToText(g *ScreenGrid) string {
	var buf strings.Builder
	row := make([]byte, 0, g.Width)
	for y := 0; y < g.Height; y++ {
		if y > 0 {
			buf.WriteByte('\n')
		}
		row = row[:0]
		for x := 0; x < g.Width; x++ {
			ch := g.Get(x, y).Char
			if ch == "" {
				ch = " "
			}
			row = append(row, ch...)
		}
		buf.WriteString(strings.TrimRight(string(row), " "))
	}
	return buf.String()
}

// renderCursorDiff positions the cursor for the diff path — same logic as
// renderCursor but writes to a builder that already has HideCursor.
func (c *Compositor) renderCursorDiff(buf *strings.Builder, root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) {
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
		return // keep cursor hidden
	}
	col, row := pd.CursorPos()
	if row < 0 || row >= c.visibleContentHeight(cell) {
		return
	}
	absRow := cell.Y + mux.StatusLineRows + row + 1
	absCol := cell.X + col + 1
	buf.WriteString(Reset)
	writeCursorTo(buf, absRow, absCol)
	buf.WriteString(ShowCursor)
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
	if row < 0 || row >= c.visibleContentHeight(cell) {
		return
	}
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
	contentH := c.visibleContentHeight(cell)

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

func (c *Compositor) visibleContentHeight(cell *mux.LayoutCell) int {
	contentH := mux.PaneContentHeight(cell.H)
	maxVisible := c.LayoutHeight() - cell.Y - mux.StatusLineRows
	if maxVisible < 0 {
		return 0
	}
	if contentH > maxVisible {
		return maxVisible
	}
	return contentH
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

		r, size := utf8.DecodeRuneInString(line[i:])
		width := runewidth.RuneWidth(r)
		if width < 0 {
			width = 0
		}
		if width > 0 && visible+width > maxWidth {
			return line[:i]
		}
		visible += width
		i += size
	}
	return line
}

// hexColorCache maps hex color strings (e.g. "f5e0dc") to precomputed
// ANSI truecolor escapes. Uses sync.Map for thread-safe concurrent access
// from multiple rendering goroutines.
var hexColorCache sync.Map

func init() {
	for _, hex := range config.CatppuccinMocha {
		hexColorCache.Store(hex, computeANSI(hex))
	}
	hexColorCache.Store(config.DimColorHex, computeANSI(config.DimColorHex))
	hexColorCache.Store(config.TextColorHex, computeANSI(config.TextColorHex))
}

// computeANSI converts a 6-digit hex color to an ANSI truecolor escape.
func computeANSI(hex string) string {
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// hexToANSI converts a 6-digit hex color to an ANSI truecolor escape.
// Results are cached — repeated calls for the same hex value are free.
// Pre-populated at init with the Catppuccin Mocha palette; unknown colors
// are computed on first use and cached thread-safely.
func hexToANSI(hex string) string {
	if len(hex) < 6 {
		return DimFg
	}
	if cached, ok := hexColorCache.Load(hex); ok {
		return cached.(string)
	}
	result := computeANSI(hex)
	hexColorCache.Store(hex, result)
	return result
}
