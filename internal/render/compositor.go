package render

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
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

// RenderStats captures lightweight render counters alongside ANSI output.
type RenderStats struct {
	CellsDiffed     int
	PanesComposited int
}

// Compositor composes pane content into terminal output.
type Compositor struct {
	width       int
	height      int
	sessionName string
	windows     []WindowInfo
	debug       bool             // when true, BuildGrid creates grids with Debug=true
	TimeNow     func() time.Time // returns current time; nil defaults to time.Now

	// Cached border map — rebuilt only when layout root changes.
	cachedBorderMap  *borderMap
	cachedBorderRoot *mux.LayoutCell
	cachedBorderH    int

	// Previous frame's grid for diff rendering. Nil forces full paint.
	prevGrid     *ScreenGrid
	prevCursor   cursorRenderState
	colorProfile termenv.Profile
}

type cursorRenderState struct {
	visible    bool
	positioned bool
	col        int
	row        int
}

func (s cursorRenderState) equal(other cursorRenderState) bool {
	return s.visible == other.visible &&
		s.positioned == other.positioned &&
		s.col == other.col &&
		s.row == other.row
}

// SetWindows sets the window list for the global bar.
func (c *Compositor) SetWindows(windows []WindowInfo) {
	c.windows = windows
}

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(width, height int, sessionName string) *Compositor {
	return &Compositor{
		width:        width,
		height:       height,
		sessionName:  sessionName,
		colorProfile: defaultColorProfile,
	}
}

// now returns the current time using the compositor's clock or time.Now.
func (c *Compositor) now() time.Time {
	if c.TimeNow != nil {
		return c.TimeNow()
	}
	return time.Now()
}

// Resize updates the compositor's terminal dimensions.
func (c *Compositor) Resize(width, height int) {
	c.width = width
	c.height = height
	// Invalidate caches — dimensions changed.
	c.cachedBorderMap = nil
	c.cachedBorderRoot = nil
	c.cachedBorderH = 0
	c.prevGrid = nil // force full repaint
	c.prevCursor = cursorRenderState{}
}

// SetSessionName updates the session name shown in the global bar.
func (c *Compositor) SetSessionName(name string) {
	c.sessionName = name
}

// SetColorProfile updates the terminal color profile used for ANSI rendering.
func (c *Compositor) SetColorProfile(profile termenv.Profile) {
	if c.colorProfile == profile {
		return
	}
	c.colorProfile = profile
	c.prevGrid = nil
	c.prevCursor = cursorRenderState{}
}

// ColorProfile reports the compositor's current terminal color profile.
func (c *Compositor) ColorProfile() termenv.Profile {
	return c.colorProfile
}

// LayoutHeight returns the height available for the layout tree
// (terminal height minus the global status bar).
func (c *Compositor) LayoutHeight() int {
	return c.height - GlobalBarHeight
}

func (c *Compositor) layoutHeightForHelpBar(overlay *HelpBarOverlay) int {
	layoutHeight := c.LayoutHeight() - helpBarRowCount(overlay)
	if layoutHeight < 0 {
		return 0
	}
	return layoutHeight
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
	out, _ := c.RenderFullWithOverlayStats(root, activePaneID, lookup, overlay, clearScreen...)
	return out
}

func (c *Compositor) RenderFullWithOverlayStats(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState, clearScreen ...bool) (string, RenderStats) {
	var buf strings.Builder
	buf.Grow(c.width * c.height * 4) // pre-allocate for typical ANSI output

	// Hide cursor during render to prevent flicker
	buf.WriteString(HideCursor)
	if len(clearScreen) > 0 && clearScreen[0] {
		buf.WriteString(ClearAll)
	}

	// Count panes for global bar
	paneCount := 0
	layoutHeight := c.layoutHeightForHelpBar(overlay.HelpBar)

	// Determine active pane color for borders
	var activeColor string
	if pd := lookup(activePaneID); pd != nil && pd.Color() != "" {
		activeColor = hexToANSIWithProfile(pd.Color(), c.colorProfile)
	} else {
		activeColor = fgHexSequence(config.BlueHex, c.colorProfile)
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
		pressed := overlay.IsPanePressed(pid)

		// Per-pane status line
		renderPaneStatusPressedWithProfile(&buf, cell, isActive, pressed, pd, c.colorProfile)

		// Pane content (shifted down by status line)
		c.renderPaneContentWithLayoutHeight(&buf, cell, isActive, pd, layoutHeight)
	})

	// Draw borders with proper junction characters.
	// Cache the border map — it only changes when the layout root changes.
	// Pointer identity is sufficient: RebuildLayout always allocates a new root,
	// and Resize() explicitly invalidates the cache.
	if c.cachedBorderMap == nil || c.cachedBorderRoot != root || c.cachedBorderH != layoutHeight {
		c.cachedBorderMap = buildBorderMap(root, c.width, layoutHeight)
		c.cachedBorderRoot = root
		c.cachedBorderH = layoutHeight
	}
	renderBordersWithProfile(&buf, c.cachedBorderMap, root, activePaneID, activeColor, c.colorProfile)

	if overlay.DropIndicator != nil {
		renderDropIndicator(&buf, overlay.DropIndicator)
	}
	if len(overlay.PaneLabels) > 0 {
		renderPaneOverlayWithProfile(&buf, root, lookup, overlay.PaneLabels, c.colorProfile)
	}

	// Global status bar at bottom
	renderGlobalBarWithProfile(&buf, c.sessionName, paneCount, c.width, c.height-1, c.windows, overlay.Message, c.now(), c.colorProfile)
	if overlay.WindowDropIndicator != nil {
		renderWindowDropIndicator(&buf, overlay.WindowDropIndicator, c.height-1, c.colorProfile)
	}
	if overlay.HelpBar != nil {
		renderHelpBarWithProfile(&buf, c.width, c.height, overlay.HelpBar, c.colorProfile)
	}
	if overlay.Chooser != nil {
		renderChooserOverlayWithProfile(&buf, c.width, c.height, overlay.Chooser, c.colorProfile)
	}
	if overlay.TextInput != nil {
		renderTextInputOverlayWithProfile(&buf, c.width, c.height, overlay.TextInput, c.colorProfile)
	}

	// Position cursor and respect the active pane's cursor visibility state.
	// If the application has hidden its cursor (e.g. during streaming output),
	// keep it hidden rather than showing it at a stale position.
	// If the application renders its own block cursor (reverse-video space),
	// hide the terminal cursor to avoid showing two cursors.
	c.renderCursorWithLayoutHeight(&buf, root, activePaneID, lookup, layoutHeight)

	return buf.String(), RenderStats{
		CellsDiffed:     c.width * c.height,
		PanesComposited: paneCount,
	}
}

// RenderDiffWithOverlay composes all panes into a cell grid, diffs against the
// previous frame, and returns minimal ANSI output for the changed cells plus
// optional client-local overlays. On the first call (or after Resize), prevGrid
// is nil and every cell is emitted.
func (c *Compositor) RenderDiffWithOverlay(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, overlay OverlayState) string {
	return c.RenderDiffWithOverlayDirty(root, activePaneID, lookup, overlay, nil, true)
}

// RenderDiffWithOverlayDirty composes all panes into a cell grid, reusing
// cached cells for clean panes when possible, diffs against the previous
// frame, and returns minimal ANSI output for the changed cells.
func (c *Compositor) RenderDiffWithOverlayDirty(
	root *mux.LayoutCell,
	activePaneID uint32,
	lookup func(uint32) PaneData,
	overlay OverlayState,
	dirtyPanes map[uint32]struct{},
	fullRedraw bool,
) string {
	out, _ := c.RenderDiffWithOverlayDirtyStats(root, activePaneID, lookup, overlay, dirtyPanes, fullRedraw)
	return out
}

func (c *Compositor) RenderDiffWithOverlayDirtyStats(
	root *mux.LayoutCell,
	activePaneID uint32,
	lookup func(uint32) PaneData,
	overlay OverlayState,
	dirtyPanes map[uint32]struct{},
	fullRedraw bool,
) (string, RenderStats) {
	newGrid, panesComposited := c.buildGridWithOverlayDirty(root, activePaneID, lookup, overlay, dirtyPanes, fullRedraw)
	changes := DiffGrid(c.prevGrid, newGrid)
	c.prevGrid = newGrid
	layoutHeight := c.layoutHeightForHelpBar(overlay.HelpBar)
	nextCursor := c.cursorRenderStateForLayoutHeight(root, activePaneID, lookup, layoutHeight)
	cursorChanged := !c.prevCursor.equal(nextCursor)
	c.prevCursor = nextCursor

	if len(changes) == 0 && !cursorChanged {
		return "", RenderStats{
			CellsDiffed:     0,
			PanesComposited: panesComposited,
		}
	}

	var buf strings.Builder
	buf.Grow(c.width * c.height) // rough estimate

	if len(changes) > 0 || !nextCursor.visible {
		buf.WriteString(HideCursor)
	}
	// Reset all styles before emitting diffs. The terminal retains the
	// "current style" from the previous frame's last cell write (typically
	// the global bar with bg=surface0). Without a reset, EmitDiff's first
	// StyleDiff(nil → cell) only sets the needed attributes, leaving stale
	// bg/fg from the prior frame to bleed into content cells.
	if len(changes) > 0 {
		buf.WriteString(Reset)
	}
	buf.WriteString(emitDiffWithProfile(changes, c.colorProfile))

	// Position cursor.
	c.renderCursorTransition(&buf, nextCursor, len(changes) > 0)

	return buf.String(), RenderStats{
		CellsDiffed:     len(changes),
		PanesComposited: panesComposited,
	}
}

// ClearPrevGrid forces a full repaint on the next RenderDiff call.
func (c *Compositor) ClearPrevGrid() {
	c.prevGrid = nil
	c.prevCursor = cursorRenderState{}
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
			cell := g.Get(x, y)
			if cell.Width == 0 {
				continue
			}
			ch := cell.Char
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
	c.renderCursorDiffWithLayoutHeight(buf, root, activePaneID, lookup, c.LayoutHeight())
}

func (c *Compositor) renderCursorDiffWithLayoutHeight(buf *strings.Builder, root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, layoutHeight int) {
	c.renderCursorTransition(buf, c.cursorRenderStateForLayoutHeight(root, activePaneID, lookup, layoutHeight), false)
}

// renderCursor positions the terminal cursor at the active pane's cursor
// location, or hides it when the active pane has a hidden cursor or renders
// its own block cursor.
func (c *Compositor) renderCursor(buf *strings.Builder, root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) {
	c.renderCursorWithLayoutHeight(buf, root, activePaneID, lookup, c.LayoutHeight())
}

func (c *Compositor) renderCursorWithLayoutHeight(buf *strings.Builder, root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, layoutHeight int) {
	state := c.cursorRenderStateForLayoutHeight(root, activePaneID, lookup, layoutHeight)
	if !state.visible {
		return // keep cursor hidden (HideCursor was written at start of render)
	}
	if !state.positioned {
		buf.WriteString(ShowCursor)
		return
	}
	writeCursorTo(buf, state.row, state.col)
	buf.WriteString(ShowCursor)
}

func (c *Compositor) cursorRenderStateForLayoutHeight(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, layoutHeight int) cursorRenderState {
	if activePaneID == 0 {
		return cursorRenderState{visible: true}
	}
	cell := root.FindByPaneID(activePaneID)
	if cell == nil {
		return cursorRenderState{visible: true}
	}
	pd := lookup(activePaneID)
	if pd == nil {
		return cursorRenderState{visible: true}
	}
	if pd.CursorHidden() || pd.HasCursorBlock() {
		return cursorRenderState{}
	}
	col, row := pd.CursorPos()
	if row < 0 || row >= c.visibleContentHeightForLayoutHeight(cell, layoutHeight) {
		return cursorRenderState{}
	}
	return cursorRenderState{
		visible:    true,
		positioned: true,
		col:        cell.X + col + 1,
		row:        cell.Y + mux.StatusLineRows + row + 1,
	}
}

func (c *Compositor) renderCursorTransition(buf *strings.Builder, state cursorRenderState, hiddenCursorWritten bool) {
	if !state.visible {
		if !hiddenCursorWritten {
			buf.WriteString(HideCursor)
		}
		return
	}
	if !state.positioned {
		buf.WriteString(ShowCursor)
		return
	}
	buf.WriteString(Reset)
	writeCursorTo(buf, state.row, state.col)
	buf.WriteString(ShowCursor)
}

func (c *Compositor) renderPaneContent(buf *strings.Builder, cell *mux.LayoutCell, active bool, pd PaneData) {
	c.renderPaneContentWithLayoutHeight(buf, cell, active, pd, c.LayoutHeight())
}

func (c *Compositor) renderPaneContentWithLayoutHeight(buf *strings.Builder, cell *mux.LayoutCell, active bool, pd PaneData, layoutHeight int) {
	copyOverlay := pd.CopyModeOverlay()
	if c.colorProfile == defaultColorProfile && copyOverlay == nil {
		rendered := pd.RenderScreen(active)
		if canFastBlitPaneContent(rendered, c.visibleContentHeightForLayoutHeight(cell, layoutHeight)) {
			c.blitPaneWithLayoutHeight(buf, cell, rendered, layoutHeight)
			return
		}
	}

	contentH := c.visibleContentHeightForLayoutHeight(cell, layoutHeight)
	for row := 0; row < contentH; row++ {
		rowCells := paneContentRowCells(cell.W, row, active, pd, copyOverlay)
		lastCol := lastPaneContentColumn(rowCells)
		if lastCol < 0 {
			continue
		}

		writeCursorTo(buf, cell.Y+mux.StatusLineRows+row+1, cell.X+1)

		var state emittedCellState
		for col := 0; col <= lastCol; {
			sc := rowCells[col]
			if sc.Width == 0 {
				col++
				continue
			}

			state.transition(buf, sc, c.colorProfile)

			char := sc.Char
			if char == "" {
				char = " "
			}
			buf.WriteString(char)

			w := sc.Width
			if w <= 0 {
				w = 1
			}
			col += w
		}

		state.closeHyperlink(buf)
		// Reset after each rendered row so styled cells cannot bleed when the
		// compositor jumps to a later row with an explicit CUP sequence.
		buf.WriteString(Reset)
	}
}

// Fast replay is safe only when the rendered pane content already matches the
// exact ANSI we want to emit: full-height rows, ASCII text, and plain SGR
// styling. Shortened repaints, OSC-8 hyperlinks, underline metadata, and
// non-ASCII grapheme assembly still need cell-by-cell recomposition.
func canFastBlitPaneContent(rendered string, visibleRows int) bool {
	if visibleRows <= 0 || strings.Count(rendered, "\n")+1 != visibleRows {
		return false
	}
	for i := 0; i < len(rendered); i++ {
		if rendered[i] != '\033' {
			if rendered[i] >= utf8.RuneSelf {
				return false
			}
			continue
		}
		if i+1 < len(rendered) && rendered[i+1] == ']' {
			return false
		}

		cmd, params, n := decodeANSISequence(rendered, i)
		if n <= 0 {
			return false
		}
		if cmd.Final() != 'm' || sgrNeedsPaneContentReserialize(params) {
			return false
		}
		i += n - 1
	}
	return true
}

func sgrNeedsPaneContentReserialize(params ansi.Params) bool {
	for _, param := range params {
		switch param.Param(-1) {
		case 4, 21, 24, 58, 59:
			return true
		}
	}
	return false
}

// blitPaneWithLayoutHeight writes pre-rendered pane rows below the status
// line. This is the fast path for default-profile panes without copy-mode
// overlays, where the pane already has the exact ANSI we want to replay.
func (c *Compositor) blitPaneWithLayoutHeight(buf *strings.Builder, cell *mux.LayoutCell, rendered string, layoutHeight int) {
	lines := strings.Split(rendered, "\n")
	contentH := c.visibleContentHeightForLayoutHeight(cell, layoutHeight)

	for i, line := range lines {
		if i >= contentH {
			break
		}
		if line == "" {
			continue
		}
		row := cell.Y + mux.StatusLineRows + i + 1
		writeCursorTo(buf, row, cell.X+1)
		buf.WriteString(clipLine(line, cell.W))
		buf.WriteString(Reset)
	}
}

func lastPaneContentColumn(row []ScreenCell) int {
	for col := len(row) - 1; col >= 0; col-- {
		cell := row[col]
		if cell.Width == 0 {
			continue
		}
		if cell.Char != " " || !cell.Style.IsZero() {
			return col
		}
	}
	return -1
}

func (c *Compositor) visibleContentHeight(cell *mux.LayoutCell) int {
	return c.visibleContentHeightForLayoutHeight(cell, c.LayoutHeight())
}

func (c *Compositor) visibleContentHeightForLayoutHeight(cell *mux.LayoutCell, layoutHeight int) int {
	// Match the content rows the PTY was sized to in mux.Window. The status
	// line is rendered separately above the pane content.
	contentH := mux.PaneContentHeight(cell.H)
	maxVisible := layoutHeight - cell.Y - mux.StatusLineRows
	if maxVisible < 0 {
		return 0
	}
	if contentH > maxVisible {
		return maxVisible
	}
	return contentH
}

// clipLine truncates an ANSI-escaped line to at most maxWidth display
// columns, preserving escape sequences that precede the cutoff.
func clipLine(line string, maxWidth int) string {
	displayCols := 0
	i := 0
	for i < len(line) {
		b := line[i]

		// Skip escape sequences — zero visible width
		if b == '\033' {
			i = skipANSISequence(line, i)
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
		if width > 0 && displayCols+width > maxWidth {
			return line[:i]
		}
		displayCols += width
		i += size
	}
	return line
}

// hexColorCache maps hex color strings (e.g. "f5e0dc") to precomputed
// ANSI truecolor escapes. Read-only after init — unknown colors are
// computed inline via computeANSI (cheap: three ParseUint + one Sprintf).
var hexColorCache = buildHexColorCache()

// lipGlossColorCache maps hex color strings to reusable Lip Gloss colors.
var lipGlossColorCache = buildLipGlossColorCache()

func buildHexColorCache() map[string]string {
	m := make(map[string]string, len(config.AccentColors())+2)
	for _, hex := range config.AccentColors() {
		m[hex] = computeANSI(hex)
	}
	m[config.DimColorHex] = computeANSI(config.DimColorHex)
	m[config.TextColorHex] = computeANSI(config.TextColorHex)
	return m
}

func buildLipGlossColorCache() map[string]lipgloss.Color {
	m := make(map[string]lipgloss.Color, len(config.AccentColors())+4)
	for _, hex := range config.AccentColors() {
		m[hex] = lipgloss.Color("#" + hex)
	}
	m[config.Surface0Hex] = lipgloss.Color("#" + config.Surface0Hex)
	m[config.Surface1Hex] = lipgloss.Color("#" + config.Surface1Hex)
	m[config.DimColorHex] = lipgloss.Color("#" + config.DimColorHex)
	m[config.TextColorHex] = lipgloss.Color("#" + config.TextColorHex)
	return m
}

// computeANSI converts a 6-digit hex color to an ANSI truecolor escape.
func computeANSI(hex string) string {
	return ansi.NewStyle().ForegroundColor(hexToColor(hex)).String()
}

func computeANSIBg(hex string) string {
	return ansi.NewStyle().BackgroundColor(hexToColor(hex)).String()
}

// hexToANSI converts a 6-digit hex color to an ANSI truecolor escape.
// Palette colors are pre-cached at init; unknown colors are computed inline.
func hexToANSI(hex string) string {
	if len(hex) < 6 {
		return DimFg
	}
	if cached, ok := hexColorCache[hex]; ok {
		return cached
	}
	return computeANSI(hex)
}

func hexToANSIWithProfile(hex string, profile termenv.Profile) string {
	if profile == defaultColorProfile {
		return hexToANSI(hex)
	}
	if len(hex) < 6 {
		return fgHexSequence(config.DimColorHex, profile)
	}
	return fgHexSequence(hex, profile)
}

func hexToANSIBg(hex string) string {
	if len(hex) < 6 {
		return Surface0Bg
	}
	return computeANSIBg(hex)
}

func hexToLipGlossColor(hex string) lipgloss.Color {
	if len(hex) < 6 {
		return lipGlossColorCache[config.DimColorHex]
	}
	if cached, ok := lipGlossColorCache[hex]; ok {
		return cached
	}
	return lipgloss.Color("#" + hex)
}
