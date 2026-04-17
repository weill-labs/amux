package render

import (
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// ScreenCell represents a single cell in the composited screen grid.
type ScreenCell struct {
	Char  string   // grapheme cluster ("" treated as space)
	Link  uv.Link  // OSC-8 hyperlink state for this cell
	Style uv.Style // foreground, background, attributes
	Width int      // 1=normal, 2=wide, 0=continuation
}

// Equal reports whether two cells are visually identical.
func (c ScreenCell) Equal(o ScreenCell) bool {
	return c.Char == o.Char && c.Width == o.Width && c.Style.Equal(&o.Style) && c.Link.Equal(&o.Link)
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

// Clone returns a deep copy of the grid cell contents.
func (g *ScreenGrid) Clone() *ScreenGrid {
	if g == nil {
		return nil
	}
	dup := &ScreenGrid{
		Width:  g.Width,
		Height: g.Height,
		Debug:  g.Debug,
		Cells:  make([]ScreenCell, len(g.Cells)),
	}
	copy(dup.Cells, g.Cells)
	return dup
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
		firstChanged, lastChanged := -1, -1
		for x := 0; x < next.Width; x++ {
			idx := y*next.Width + x
			if !next.Cells[idx].Equal(prev.Cells[idx]) {
				if firstChanged < 0 {
					firstChanged = x
				}
				lastChanged = x
			}
		}
		if firstChanged < 0 {
			continue
		}
		lastChanged = extendChangedStatusTail(next, y, firstChanged, lastChanged)
		for x := firstChanged; x <= lastChanged; x++ {
			idx := y*next.Width + x
			changes = append(changes, CellChange{
				X: x, Y: y, Cell: next.Cells[idx],
			})
		}
	}
	return changes
}

// Rows directly below a horizontal separator are pane-header rows. If a
// header update changes earlier metadata but leaves the clipped tail
// text/padding identical in the grid, the terminal can retain stale cells in
// that tail from a previous partial write. Extend the rewrite through the rest
// of the styled status-line segment so the lower-row header fully heals.
func extendChangedStatusTail(next *ScreenGrid, y, firstChanged, lastChanged int) int {
	if y <= 0 || !rowHasHorizontalSeparator(next, y-1) {
		return lastChanged
	}
	if next.Get(firstChanged, y).Style.Bg == nil {
		return lastChanged
	}

	end := lastChanged
	for x := lastChanged + 1; x < next.Width; x++ {
		cell := next.Get(x, y)
		if cell.Width == 0 {
			end = x
			continue
		}
		if cell.Style.Bg == nil {
			break
		}
		end = x
	}
	return end
}

func rowHasHorizontalSeparator(g *ScreenGrid, y int) bool {
	if y < 0 || y >= g.Height {
		return false
	}
	for x := 0; x < g.Width; x++ {
		if isHorizontalSeparatorChar(g.Get(x, y).Char) {
			return true
		}
	}
	return false
}

func isHorizontalSeparatorChar(ch string) bool {
	switch ch {
	case "─", "┬", "┴", "┼", "├", "┤":
		return true
	default:
		return false
	}
}

// EmitDiff produces minimal ANSI output for the given cell changes.
// Changes must be in row-major order (as returned by DiffGrid).
// Consecutive cells on the same row share a single CUP escape.
// Does not emit HideCursor/ShowCursor — the compositor handles those.
func EmitDiff(changes []CellChange) string {
	return emitDiffWithProfile(changes, defaultColorProfile)
}

func emitDiffWithProfile(changes []CellChange, profile termenv.Profile) string {
	if len(changes) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.Grow(len(changes) * 8)

	var state emittedCellState
	expectX, expectY := -1, -1

	for _, ch := range changes {
		if ch.Cell.Width == 0 {
			continue
		}

		// Emit CUP only when cursor is not at the expected position.
		if ch.Y != expectY || ch.X != expectX {
			writeCursorTo(&buf, ch.Y+1, ch.X+1)
		}

		state.transition(&buf, ch.Cell, profile)

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
	state.closeHyperlink(&buf)
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
	return ScreenCell{Char: char, Link: c.Link, Style: c.Style, Width: c.Width}
}

func (c *Compositor) buildGridWithOverlay(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState) (*ScreenGrid, int) {
	g := NewScreenGrid(c.width, c.height)
	g.Debug = c.debug
	layoutHeight := c.layoutHeightForHelpBar(overlay.HelpBar)

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
		pressed := overlay.IsPanePressed(pid)
		copyOverlay := pd.CopyModeOverlay()

		// Status line cells.
		buildStatusCellsPressed(g, cell, isActive, pressed, pd)

		// Pane content cells.
		contentH := c.visibleContentHeightForLayoutHeight(cell, layoutHeight)
		for row := 0; row < contentH; row++ {
			buildPaneContentCells(g, cell, row, isActive, pd, copyOverlay)
		}
	})

	// Border cells.
	if c.cachedBorderMap == nil || c.cachedBorderRoot != root || c.cachedBorderH != layoutHeight {
		c.cachedBorderMap = buildBorderMap(root, c.width, layoutHeight)
		c.cachedBorderRoot = root
		c.cachedBorderH = layoutHeight
	}
	buildBorderCells(g, c.cachedBorderMap, activePaneID, activeColorHex)

	if overlay.DropIndicator != nil {
		buildDropIndicatorCells(g, overlay.DropIndicator)
	}
	if len(overlay.PaneLabels) > 0 {
		buildPaneOverlayCells(g, root, lookup, overlay.PaneLabels)
	}

	// Global bar cells.
	buildGlobalBarCells(g, c.sessionName, paneCount, c.width, c.height-1, c.windows, overlay.Message, c.now())
	buildWindowDropIndicatorCell(g, overlay.WindowDropIndicator, c.height-1)
	if overlay.HelpBar != nil {
		buildHelpBarCells(g, overlay.HelpBar)
	}
	if overlay.Chooser != nil {
		buildChooserOverlayCells(g, overlay.Chooser)
	}
	if overlay.TextInput != nil {
		buildTextInputOverlayCells(g, overlay.TextInput)
	}

	return g, paneCount
}

const dirtyPaneParallelThreshold = 4

type dirtyPaneComposite struct {
	cell        *mux.LayoutCell
	pd          PaneData
	isActive    bool
	pressed     bool
	copyOverlay *proto.ViewportOverlay
}

func (c *Compositor) composeDirtyPane(g *ScreenGrid, layoutHeight int, pane dirtyPaneComposite) {
	buildStatusCellsPressed(g, pane.cell, pane.isActive, pane.pressed, pane.pd)
	contentH := c.visibleContentHeightForLayoutHeight(pane.cell, layoutHeight)
	// Rebuild every row for dirty panes. TUI full-screen recomposes can
	// move or clear lines without producing a pane-local dirty report that
	// safely describes every changed row, so reusing cached rows here can
	// leave stale cells until the next full redraw.
	for row := 0; row < contentH; row++ {
		buildPaneContentCells(g, pane.cell, row, pane.isActive, pane.pd, pane.copyOverlay)
	}
	clearPaneContentRows(g, pane.cell, contentH, mux.PaneContentHeight(pane.cell.H))
}

func (c *Compositor) buildGridWithOverlayDirty(
	root *mux.LayoutCell,
	activePaneID uint32,
	lookup func(uint32) PaneData,
	overlay OverlayState,
	dirtyPanes map[uint32]struct{},
	fullRedraw bool,
) (*ScreenGrid, int) {
	if fullRedraw || c.prevGrid == nil {
		return c.buildGridWithOverlay(root, activePaneID, lookup, overlay)
	}

	g := c.prevGrid.Clone()
	g.Debug = c.debug
	layoutHeight := c.layoutHeightForHelpBar(overlay.HelpBar)

	paneCount := 0
	dirtyCells := make([]dirtyPaneComposite, 0, len(dirtyPanes))
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
		if _, ok := dirtyPanes[pid]; !ok {
			return
		}
		dirtyCells = append(dirtyCells, dirtyPaneComposite{
			cell:        cell,
			pd:          pd,
			isActive:    pid == activePaneID,
			pressed:     overlay.IsPanePressed(pid),
			copyOverlay: pd.CopyModeOverlay(),
		})
	})
	compositedPanes := len(dirtyCells)

	if len(dirtyCells) < dirtyPaneParallelThreshold {
		for _, pane := range dirtyCells {
			c.composeDirtyPane(g, layoutHeight, pane)
		}
	} else {
		var wg sync.WaitGroup
		wg.Add(len(dirtyCells))
		for _, pane := range dirtyCells {
			go func(pane dirtyPaneComposite) {
				defer wg.Done()
				c.composeDirtyPane(g, layoutHeight, pane)
			}(pane)
		}
		wg.Wait()
	}

	if overlay.DropIndicator != nil {
		buildDropIndicatorCells(g, overlay.DropIndicator)
	}
	if len(overlay.PaneLabels) > 0 {
		buildPaneOverlayCells(g, root, lookup, overlay.PaneLabels)
	}

	buildGlobalBarCells(g, c.sessionName, paneCount, c.width, c.height-1, c.windows, overlay.Message, c.now())
	buildWindowDropIndicatorCell(g, overlay.WindowDropIndicator, c.height-1)
	if overlay.HelpBar != nil {
		buildHelpBarCells(g, overlay.HelpBar)
	}
	if overlay.Chooser != nil {
		buildChooserOverlayCells(g, overlay.Chooser)
	}
	if overlay.TextInput != nil {
		buildTextInputOverlayCells(g, overlay.TextInput)
	}

	return g, compositedPanes
}

// buildPaneContentCells writes a pane row into the compositor grid using the
// same visual grapheme grouping that RenderScreen's row serializer produces.
// Some Unicode sequences are assembled incrementally at later cursor columns;
// the VT buffer then stores the fragments as separate cells even though the
// rendered row collapses them into a single grapheme cluster. Re-pack the row
// before diffing so RenderDiff matches the RenderFull path.
func buildPaneContentCells(g *ScreenGrid, cell *mux.LayoutCell, row int, active bool, pd PaneData, copyOverlay *proto.ViewportOverlay) {
	rowCells := paneContentRowCells(cell.W, row, active, pd, copyOverlay)
	for col, sc := range rowCells {
		g.Set(cell.X+col, cell.Y+mux.StatusLineRows+row, sc)
	}
}

// clearPaneContentRows blanks rows in the cloned dirty grid that are no longer
// visible for this pane so clipped panes cannot retain stale content from the
// previous frame.
func clearPaneContentRows(g *ScreenGrid, cell *mux.LayoutCell, startRow, endRow int) {
	if startRow >= endRow {
		return
	}
	for row := startRow; row < endRow; row++ {
		y := cell.Y + mux.StatusLineRows + row
		if y < 0 || y >= g.Height {
			continue
		}
		for col := 0; col < cell.W; col++ {
			g.Set(cell.X+col, y, ScreenCell{Char: " ", Width: 1})
		}
	}
}

func paneContentRowCells(width, row int, active bool, pd PaneData, copyOverlay *proto.ViewportOverlay) []ScreenCell {
	rowCells := make([]ScreenCell, width)
	for i := range rowCells {
		rowCells[i] = ScreenCell{Char: " ", Width: 1}
	}

	dstCol := 0
	for srcCol := 0; srcCol < width && dstCol < width; {
		sc := paneContentCellAt(row, srcCol, active, pd, copyOverlay)
		if sc.Width == 0 && sc.Char == " " {
			srcCol++
			continue
		}

		rendered, renderedWidth, nextSrc := compactRowCell(width, row, active, pd, copyOverlay, srcCol, sc)
		if renderedWidth <= 0 {
			renderedWidth = 1
		}
		if renderedWidth > width-dstCol {
			break
		}

		rowCells[dstCol] = rendered
		for i := 1; i < renderedWidth && dstCol+i < width; i++ {
			rowCells[dstCol+i] = ScreenCell{
				Link:  rendered.Link,
				Style: rendered.Style,
				Width: 0,
			}
		}

		dstCol += renderedWidth
		srcCol = nextSrc
	}
	return rowCells
}

func paneContentCellAt(row, col int, active bool, pd PaneData, copyOverlay *proto.ViewportOverlay) ScreenCell {
	return applyCopyModeOverlay(pd.CellAt(col, row, active), copyOverlay, col, row)
}

func compactRowCell(width, row int, active bool, pd PaneData, copyOverlay *proto.ViewportOverlay, srcCol int, base ScreenCell) (ScreenCell, int, int) {
	baseWidth := base.Width
	if baseWidth <= 0 {
		baseWidth = 1
	}

	merged := base
	mergedWidth := baseWidth
	nextSrc := srcCol + baseWidth
	candidate := base.Char

	for nextSrc < width {
		next := paneContentCellAt(row, nextSrc, active, pd, copyOverlay)
		if next.Width == 0 && next.Char == " " {
			nextSrc++
			continue
		}
		if next.Char == " " || !merged.Style.Equal(&next.Style) || !merged.Link.Equal(&next.Link) {
			break
		}

		concat := candidate + next.Char
		cluster, clusterWidth := ansi.FirstGraphemeCluster(concat, ansi.GraphemeWidth)
		if cluster != concat {
			// Some cursor-assembled emoji suffixes depend on an implicit ZWJ that
			// never occupied its own source cell. Reinsert it when that produces
			// the visible grapheme cluster the terminal rendered.
			zwjConcat := candidate + "\u200d" + next.Char
			cluster, clusterWidth = ansi.FirstGraphemeCluster(zwjConcat, ansi.GraphemeWidth)
			if cluster != zwjConcat {
				break
			}
			concat = zwjConcat
		}

		candidate = concat
		merged.Char = candidate
		merged.Width = clusterWidth
		mergedWidth = clusterWidth

		nextWidth := next.Width
		if nextWidth <= 0 {
			nextWidth = 1
		}
		nextSrc += nextWidth
	}

	return merged, mergedWidth, nextSrc
}

type emittedCellState struct {
	hasStyle bool
	style    uv.Style
	link     uv.Link
}

func (s *emittedCellState) stylePtr() *uv.Style {
	if !s.hasStyle {
		return nil
	}
	return &s.style
}

func (s *emittedCellState) transition(buf *strings.Builder, cell ScreenCell, profile termenv.Profile) {
	if diff := styleDiffWithProfile(s.stylePtr(), cell.Style, profile); diff != "" {
		buf.WriteString(diff)
	}
	if !cell.Link.Equal(&s.link) {
		if !linkIsZero(s.link) {
			buf.WriteString(ansi.ResetHyperlink())
		}
		if !linkIsZero(cell.Link) {
			buf.WriteString(hyperlinkSequence(cell.Link))
		}
	}

	s.style = cell.Style
	s.hasStyle = true
	s.link = cell.Link
}

func (s *emittedCellState) closeHyperlink(buf *strings.Builder) {
	if !linkIsZero(s.link) {
		buf.WriteString(ansi.ResetHyperlink())
		s.link = uv.Link{}
	}
}

func hyperlinkSequence(link uv.Link) string {
	if link.Params == "" {
		return ansi.SetHyperlink(link.URL)
	}
	return ansi.SetHyperlink(link.URL, link.Params)
}

func linkIsZero(link uv.Link) bool {
	return link.URL == "" && link.Params == ""
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

// appendStyledCells expands s into styled screen cells so diff rendering honors
// the display width of wide glyphs the same way the ANSI full-render path does.
func appendStyledCells(cells []ScreenCell, s string, style uv.Style) []ScreenCell {
	for len(s) > 0 {
		cluster, clusterWidth := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		if clusterWidth <= 0 {
			clusterWidth = 1
		}
		cells = append(cells, ScreenCell{Char: cluster, Width: clusterWidth, Style: style})
		for i := 1; i < clusterWidth; i++ {
			cells = append(cells, ScreenCell{Char: " ", Width: 0, Style: style})
		}
		s = s[len(cluster):]
	}
	return cells
}

// buildStatusCells writes the per-pane status line into the grid cell-by-cell.
func buildStatusCells(g *ScreenGrid, cell *mux.LayoutCell, isActive bool, pd PaneData) {
	buildStatusCellsPressed(g, cell, isActive, false, pd)
}

func buildStatusCellsPressed(g *ScreenGrid, cell *mux.LayoutCell, isActive, pressed bool, pd PaneData) {
	y := cell.Y
	bgHex := config.Surface0Hex
	if pressed {
		bgHex = config.Surface1Hex
	}
	bg := hexToColor(bgHex)
	colorHex := paneStatusColorHex(pd)
	palette := newPaneStatusGridPalette(colorHex, bg)
	var cells []ScreenCell
	for _, segment := range buildPaneStatusSegments(cell.W, isActive, pd) {
		cells = appendStyledCells(cells, segment.text, palette.style(segment.role))
	}

	// Write cells to grid, fill remaining with spaces.
	fillCell := ScreenCell{Char: " ", Width: 1, Style: uv.Style{Bg: bg}}
	for i := 0; i < cell.W; i++ {
		sc := fillCell
		if i < len(cells) {
			sc = cells[i]
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
	focusedStyle := boldStyle
	focusedStyle.Fg = hexToColor(config.BlueHex)
	errorStyle := uv.Style{Fg: redFg, Bg: bg}

	var cells []ScreenCell
	showHelp := globalBarShowsHelp(width, sessionName, paneCount, windows, message, now)

	// " amux │ "
	cells = appendStyledCells(cells, " ", baseStyle)
	cells = appendStyledCells(cells, "amux", boldStyle)
	cells = appendStyledCells(cells, " │ ", baseStyle)

	if tabs := buildGlobalBarWindowTabs(windows); len(tabs) > 0 {
		for _, tab := range tabs {
			style := baseStyle
			if tab.window.IsActive {
				style = boldStyle
			}
			style.Fg = hexToColor(globalBarTabColorHex(tab.window))
			cells = appendStyledCells(cells, tab.display, style)
			cells = appendStyledCells(cells, " ", baseStyle)
		}
		cells = appendStyledCells(cells, "│ ", baseStyle)
	} else {
		cells = appendStyledCells(cells, sessionName+" ", baseStyle)
	}

	rightText := ""
	rightStyle := baseStyle
	if message != "" {
		maxText := width - len(cells) - 2
		rightText = " " + truncateRunes(message, maxText) + " "
		rightStyle = errorStyle
		message = ""
	} else {
		rightText = globalBarStatusRightText(paneCount, showHelp, now)
	}

	// Fill middle.
	leftLen := len(cells)
	rightLen := len([]rune(rightText))
	fill := width - leftLen - rightLen
	if fill > 0 {
		messageRunes := []rune(message)
		if len(messageRunes) > fill {
			messageRunes = messageRunes[:fill]
		}
		cells = appendStyledCells(cells, string(messageRunes), baseStyle)
		for i := len(messageRunes); i < fill; i++ {
			cells = append(cells, ScreenCell{Char: " ", Width: 1, Style: baseStyle})
		}
	}
	cells = appendStyledCells(cells, rightText, rightStyle)

	// Write to grid.
	for i := 0; i < width && i < len(cells); i++ {
		g.Set(i, yPos, cells[i])
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
