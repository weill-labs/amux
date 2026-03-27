package render

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

// ScreenCell represents a single cell in the composited screen grid.
type ScreenCell struct {
	Char  string   // grapheme cluster ("" treated as space)
	Style uv.Style // foreground, background, attributes
	Width int      // 1=normal, 2=wide, 0=continuation
}

// Equal reports whether two cells are visually identical.
func (c ScreenCell) Equal(o ScreenCell) bool {
	return c.Char == o.Char && c.Width == o.Width && c.Style.Equal(&o.Style)
}

// OOBWrite records a single out-of-bounds Set() call.
type OOBWrite struct {
	X, Y int
}

// ScreenGrid is a 2D grid of screen cells in row-major order.
type ScreenGrid struct {
	Width, Height int
	Cells         []ScreenCell // Cells[y*Width + x]
	Debug         bool         // when true, OOB Set() calls are recorded for inspection in tests
	oobWrites     []OOBWrite
}

// NewScreenGrid creates a grid filled with space cells.
func NewScreenGrid(width, height int) *ScreenGrid {
	cells := make([]ScreenCell, width*height)
	for i := range cells {
		cells[i] = ScreenCell{Char: " ", Width: 1}
	}
	return &ScreenGrid{Width: width, Height: height, Cells: cells}
}

// Set writes a cell at (x, y). Out-of-bounds writes are silently ignored.
// When Debug is true, OOB writes are also recorded for later inspection.
func (g *ScreenGrid) Set(x, y int, cell ScreenCell) {
	if x >= 0 && x < g.Width && y >= 0 && y < g.Height {
		g.Cells[y*g.Width+x] = cell
		return
	}
	if g.Debug {
		g.oobWrites = append(g.oobWrites, OOBWrite{X: x, Y: y})
	}
}

// Get reads the cell at (x, y). Out-of-bounds returns a space cell.
func (g *ScreenGrid) Get(x, y int) ScreenCell {
	if x >= 0 && x < g.Width && y >= 0 && y < g.Height {
		return g.Cells[y*g.Width+x]
	}
	return ScreenCell{Char: " ", Width: 1}
}

// CellChange records a single cell that differs between two grids.
type CellChange struct {
	X, Y int
	Cell ScreenCell
}

// DiffGrid compares prev and next and returns changed cells in row-major order.
// If prev is nil, every cell in next is returned (initial full paint).
func DiffGrid(prev, next *ScreenGrid) []CellChange {
	if next == nil {
		return nil
	}
	if prev == nil {
		changes := make([]CellChange, 0, next.Width*next.Height)
		for y := 0; y < next.Height; y++ {
			for x := 0; x < next.Width; x++ {
				changes = append(changes, CellChange{
					X: x, Y: y,
					Cell: next.Cells[y*next.Width+x],
				})
			}
		}
		return changes
	}
	var changes []CellChange
	for y := 0; y < next.Height; y++ {
		for x := 0; x < next.Width; x++ {
			idx := y*next.Width + x
			if !next.Cells[idx].Equal(prev.Cells[idx]) {
				changes = append(changes, CellChange{
					X: x, Y: y, Cell: next.Cells[idx],
				})
			}
		}
	}
	return changes
}

// EmitDiff produces minimal ANSI output for the given cell changes.
// Changes must be in row-major order (as returned by DiffGrid).
// Consecutive cells on the same row share a single CUP escape.
// Does not emit HideCursor/ShowCursor — the compositor handles those.
func EmitDiff(changes []CellChange) string {
	if len(changes) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.Grow(len(changes) * 8)

	var prevStyle *uv.Style
	expectX, expectY := -1, -1

	for _, ch := range changes {
		// Emit CUP only when cursor is not at the expected position.
		if ch.Y != expectY || ch.X != expectX {
			writeCursorTo(&buf, ch.Y+1, ch.X+1)
		}

		// Minimal style transition.
		s := ch.Cell.Style
		if diff := uv.StyleDiff(prevStyle, &s); diff != "" {
			buf.WriteString(diff)
		}
		sCopy := s
		prevStyle = &sCopy

		// Write character.
		char := ch.Cell.Char
		if char == "" {
			char = " "
		}
		buf.WriteString(char)

		// Advance expected cursor position.
		expectY = ch.Y
		w := ch.Cell.Width
		if w <= 0 {
			w = 1
		}
		expectX = ch.X + w
	}
	return buf.String()
}

// CellFromUV converts a VT library cell to a ScreenCell.
func CellFromUV(c *uv.Cell) ScreenCell {
	if c == nil {
		return ScreenCell{Char: " ", Width: 1}
	}
	char := c.Content
	if char == "" {
		char = " "
	}
	return ScreenCell{Char: char, Style: c.Style, Width: c.Width}
}

func (c *Compositor) buildGridWithOverlay(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState) *ScreenGrid {
	g := NewScreenGrid(c.width, c.height)
	g.Debug = c.debug

	// Determine active pane color for borders.
	var activeColorHex string
	if pd := lookup(activePaneID); pd != nil && pd.Color() != "" {
		activeColorHex = pd.Color()
	}

	paneCount := 0

	// Render each pane's status line and content.
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

		// Status line cells.
		buildStatusCells(g, cell, isActive, pd)

		// Pane content cells.
		contentH := mux.PaneContentHeight(cell.H)
		for row := 0; row < contentH; row++ {
			for col := 0; col < cell.W; col++ {
				sc := pd.CellAt(col, row, isActive)
				g.Set(cell.X+col, cell.Y+mux.StatusLineRows+row, sc)
			}
		}
	})

	// Border cells.
	if c.cachedBorderMap == nil || c.cachedBorderRoot != root {
		c.cachedBorderMap = buildBorderMap(root, c.width, c.height)
		c.cachedBorderRoot = root
	}
	buildBorderCells(g, c.cachedBorderMap, activePaneID, activeColorHex)

	if len(overlay.PaneLabels) > 0 {
		buildPaneOverlayCells(g, root, lookup, overlay.PaneLabels)
	}

	// Global bar cells.
	buildGlobalBarCells(g, c.sessionName, paneCount, c.width, c.height-1, c.windows, overlay.Message, c.now())
	if overlay.Chooser != nil {
		buildChooserOverlayCells(g, overlay.Chooser)
	}

	return g
}

// styledChar represents a single character with styling for status/bar rendering.
type styledChar struct {
	ch    string
	style uv.Style
}

type paneStatusGridPalette struct {
	background    uv.Style
	pane          uv.Style
	paneBold      uv.Style
	dim           uv.Style
	text          uv.Style
	yellow        uv.Style
	green         uv.Style
	red           uv.Style
	completedMeta uv.Style
}

func newPaneStatusGridPalette(colorHex string, bg color.Color) paneStatusGridPalette {
	dimStyle := uv.Style{Fg: hexToColor(config.DimColorHex), Bg: bg}
	paneStyle := uv.Style{Fg: hexToColor(colorHex), Bg: bg}
	paneBold := paneStyle
	paneBold.Attrs |= uv.AttrBold
	completedMetaStyle := dimStyle
	completedMetaStyle.Attrs |= uv.AttrStrikethrough

	return paneStatusGridPalette{
		background:    uv.Style{Bg: bg},
		pane:          paneStyle,
		paneBold:      paneBold,
		dim:           dimStyle,
		text:          uv.Style{Fg: hexToColor(config.TextColorHex), Bg: bg},
		yellow:        uv.Style{Fg: hexToColor(config.YellowHex), Bg: bg},
		green:         uv.Style{Fg: hexToColor(config.GreenHex), Bg: bg},
		red:           uv.Style{Fg: hexToColor(config.RedHex), Bg: bg},
		completedMeta: completedMetaStyle,
	}
}

func (p paneStatusGridPalette) style(role paneStatusSegmentRole) uv.Style {
	switch role {
	case paneStatusSegmentPane:
		return p.pane
	case paneStatusSegmentPaneBold:
		return p.paneBold
	case paneStatusSegmentDim:
		return p.dim
	case paneStatusSegmentText:
		return p.text
	case paneStatusSegmentYellow:
		return p.yellow
	case paneStatusSegmentGreen:
		return p.green
	case paneStatusSegmentRed:
		return p.red
	case paneStatusSegmentCompletedMeta:
		return p.completedMeta
	default:
		return p.background
	}
}

// appendStyledStr appends each rune of s as a styledChar with the given style.
func appendStyledStr(chars []styledChar, s string, style uv.Style) []styledChar {
	for _, r := range s {
		chars = append(chars, styledChar{ch: string(r), style: style})
	}
	return chars
}

// buildStatusCells writes the per-pane status line into the grid cell-by-cell.
func buildStatusCells(g *ScreenGrid, cell *mux.LayoutCell, isActive bool, pd PaneData) {
	y := cell.Y
	bg := hexToColor(config.Surface0Hex)
	colorHex := paneStatusColorHex(pd)
	palette := newPaneStatusGridPalette(colorHex, bg)
	var chars []styledChar
	for _, segment := range buildPaneStatusSegments(cell.W, isActive, pd) {
		chars = appendStyledStr(chars, segment.text, palette.style(segment.role))
	}

	// Write chars to grid, fill remaining with spaces.
	fillCell := ScreenCell{Char: " ", Width: 1, Style: uv.Style{Bg: bg}}
	for i := 0; i < cell.W; i++ {
		sc := fillCell
		if i < len(chars) {
			sc = ScreenCell{Char: chars[i].ch, Width: 1, Style: chars[i].style}
		}
		g.Set(cell.X+i, y, sc)
	}
}

// buildBorderCells writes border characters into the grid with proper colors.
func buildBorderCells(g *ScreenGrid, bm *borderMap, activePaneID uint32, activeColorHex string) {
	activeColorFg := hexToColor(activeColorHex)
	dimFgColor := hexToColor(config.DimColorHex)

	for _, pos := range bm.positions {
		x, y := pos.x, pos.y

		up := bm.has(x, y-1)
		down := bm.has(x, y+1)
		left := bm.has(x-1, y)
		right := bm.has(x+1, y)
		ch := junctionChar(up, down, left, right)

		bc := bm.get(x, y)
		isJunction := (up || down) && (left || right)
		fg := dimFgColor
		if borderAdjacentToActive(bc.left, bc.right, x, y, isJunction, activePaneID) {
			fg = activeColorFg
		}

		g.Set(x, y, ScreenCell{Char: ch, Width: 1, Style: uv.Style{Fg: fg}})
	}
}

// buildGlobalBarCells writes the global status bar into the grid.
func buildGlobalBarCells(g *ScreenGrid, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string, now time.Time) {
	bg := hexToColor(config.Surface0Hex)
	textFg := hexToColor(config.TextColorHex)
	redFg := hexToColor(config.RedHex)
	baseStyle := uv.Style{Fg: textFg, Bg: bg}
	boldStyle := baseStyle
	boldStyle.Attrs |= uv.AttrBold
	errorStyle := uv.Style{Fg: redFg, Bg: bg}

	var chars []styledChar

	// " amux │ "
	chars = appendStyledStr(chars, " ", baseStyle)
	chars = appendStyledStr(chars, "amux", boldStyle)
	chars = appendStyledStr(chars, " │ ", baseStyle)

	if tabs := buildGlobalBarWindowTabs(windows); len(tabs) > 0 {
		for _, tab := range tabs {
			style := baseStyle
			if tab.window.IsActive {
				style = boldStyle
			}
			style.Fg = hexToColor(globalBarTabColorHex(tab.window))
			chars = appendStyledStr(chars, tab.display, style)
			chars = appendStyledStr(chars, " ", baseStyle)
		}
		chars = appendStyledStr(chars, "│ ", baseStyle)
	} else {
		chars = appendStyledStr(chars, sessionName+" ", baseStyle)
	}

	rightText := ""
	rightStyle := baseStyle
	if message != "" {
		maxText := width - len(chars) - 2
		rightText = " " + truncateRunes(message, maxText) + " "
		rightStyle = errorStyle
		message = ""
	} else {
		paneCountStr := strconv.Itoa(paneCount)
		now := now.Format("15:04")
		rightText = " " + paneCountStr + " panes │ " + now + " "
	}

	// Fill middle.
	leftLen := len(chars)
	rightLen := len([]rune(rightText))
	fill := width - leftLen - rightLen
	if fill > 0 {
		messageRunes := []rune(message)
		if len(messageRunes) > fill {
			messageRunes = messageRunes[:fill]
		}
		chars = appendStyledStr(chars, string(messageRunes), baseStyle)
		for i := len(messageRunes); i < fill; i++ {
			chars = append(chars, styledChar{ch: " ", style: baseStyle})
		}
	}
	chars = appendStyledStr(chars, rightText, rightStyle)

	// Write to grid.
	for i := 0; i < width && i < len(chars); i++ {
		g.Set(i, yPos, ScreenCell{Char: chars[i].ch, Width: 1, Style: chars[i].style})
	}
}

// hexToColor converts a 6-digit hex color string to a color.Color.
// Returns nil for invalid or short input.
func hexToColor(hex string) color.Color {
	if len(hex) < 6 {
		return nil
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return ansi.RGBColor{R: uint8(r), G: uint8(g), B: uint8(b)}
}
