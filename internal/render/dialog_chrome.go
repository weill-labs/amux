package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
)

// dialogChrome is the shared, rounded-border modal frame used by the chooser
// (prefix+w / prefix+s) and the text-input prompt (prefix+, / prefix+.). It
// composites cells directly into a ScreenGrid so it overlays pane content and
// participates in the diff renderer. See docs in findings.md / LAB-1970.
//
// Vertical structure (top to bottom):
//
//	╭ title ──────────── toggle ╮   top border (title embedded)
//	│ > query                   │   query line   (showQuery)
//	│ subtitle                  │   subtitle     (subtitle != "")
//	│ rows...                   │   content rows
//	│ footer                    │   footer       (footer != "")
//	╰───────────────────────────╯   bottom border
type dialogChrome struct {
	title     string
	toggle    *dialogToggle // optional ◉/○ selector, right-aligned in title bar
	subtitle  string        // optional contextual line under the title
	showQuery bool          // render a "> query" filter line
	query     string
	rows      []dialogRow
	footer    []footerHint  // keybinding hints, drawn just above the bottom border
	scroll    *dialogScroll // optional scrollbar state for overflowing row sets
}

// dialogScroll describes the visible window into a larger row set so a
// scrollbar can be drawn. rows passed in dialogChrome are the visible slice.
type dialogScroll struct {
	total   int // total rows in the full set
	offset  int // index of the first visible row within the full set
	visible int // number of visible rows (== len(rows))
}

// footerHint is one "key label" pair in the footer. The key is rendered bold
// and bright; the label is dim — matching crush's footer styling.
type footerHint struct {
	key   string
	label string
}

// footerText renders the hints as a plain "key label • key label" string,
// used for width measurement.
func footerText(hints []footerHint) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = h.key + " " + h.label
	}
	return strings.Join(parts, " • ")
}

type dialogRowKind int

const (
	dialogRowNormal dialogRowKind = iota
	dialogRowSelected
	dialogRowDim
	dialogRowSection
	dialogRowHeader
)

// dialogRow is one content line. desc is an optional dimmed suffix.
type dialogRow struct {
	text string
	desc string
	kind dialogRowKind
}

// dialogToggle is the radio-style mode selector embedded in the title bar.
// Glyphs are sourced from an IconSet so they respect the configured theme.
type dialogToggle struct {
	options  []string
	selected int
	on, off  string // selected / unselected glyph (from IconSet)
}

// newDialogToggle builds a toggle using the default icon set's radio glyphs.
func newDialogToggle(selected int, options ...string) *dialogToggle {
	icons := DefaultIconSet()
	return &dialogToggle{options: options, selected: selected, on: icons.ToggleOn, off: icons.ToggleOff}
}

func (t dialogToggle) display() string {
	parts := make([]string, len(t.options))
	for i, opt := range t.options {
		glyph := t.off
		if i == t.selected {
			glyph = t.on
		}
		parts[i] = glyph + " " + opt
	}
	return strings.Join(parts, "  ")
}

// dialogRect is the placed bounding box of a chrome, in screen cells.
type dialogRect struct {
	X, Y, W, H int
}

// dialogStyles holds the per-element cell styles for a chrome.
type dialogStyles struct {
	border    uv.Style
	title     uv.Style
	text      uv.Style
	dim       uv.Style
	selected  uv.Style
	section   uv.Style
	header    uv.Style // bold grouping rows (window names in the tree)
	footer    uv.Style // dim label text and the • separator
	footerKey uv.Style // bold, bright key names (enter, esc, ↑/↓)
}

func defaultDialogStyles() dialogStyles {
	surface := hexToColor(config.Surface0Hex)
	text := hexToColor(config.TextColorHex)
	mauve := hexToColor(config.MauveHex)
	dim := hexToColor(config.DimColorHex)
	return dialogStyles{
		border:    uv.Style{Fg: mauve, Bg: surface, Attrs: uv.AttrBold},
		title:     uv.Style{Fg: mauve, Bg: surface, Attrs: uv.AttrBold},
		text:      uv.Style{Fg: text, Bg: surface},
		dim:       uv.Style{Fg: dim, Bg: surface},
		selected:  uv.Style{Fg: surface, Bg: mauve, Attrs: uv.AttrBold},
		section:   uv.Style{Fg: hexToColor(config.BlueHex), Bg: surface, Attrs: uv.AttrBold},
		header:    uv.Style{Fg: text, Bg: surface, Attrs: uv.AttrBold},
		footer:    uv.Style{Fg: dim, Bg: surface},
		footerKey: uv.Style{Fg: hexToColor(config.FooterKeyHex), Bg: surface, Attrs: uv.AttrBold},
	}
}

// runeDisplayWidth returns the terminal column width of a single rune.
func runeDisplayWidth(r rune) int {
	return ansi.StringWidth(string(r))
}

// placeRunes writes s into the grid starting at (x, y), advancing by each
// rune's *display width* (not byte length) and emitting a continuation cell
// after wide glyphs. It stops at maxX (exclusive). Returns the ending x.
//
// This is the fix for the historical byte-indexed placement loop: multi-byte
// border/box glyphs land at their true display column.
func placeRunes(g *ScreenGrid, x, y, maxX int, s string, style uv.Style) int {
	for _, r := range s {
		w := runeDisplayWidth(r)
		if w == 0 {
			continue
		}
		if x+w > maxX {
			break
		}
		g.Set(x, y, ScreenCell{Char: string(r), Width: w, Style: style})
		if w == 2 {
			g.Set(x+1, y, ScreenCell{Char: "", Width: 0, Style: style})
		}
		x += w
	}
	return x
}

// computeLayout sizes and centers the chrome for a screen of the given
// dimensions. It returns the bounding rect, the screen-y of the first list
// row (bodyTop), and ok=false if the chrome does not fit. place() and the
// mouse hit-test both go through here so rendered and clickable geometry match.
func (d dialogChrome) computeLayout(screenW, screenH int) (dialogRect, int, bool) {
	if screenW <= 0 || screenH <= 0 {
		return dialogRect{}, 0, false
	}

	contentW := d.contentWidth()
	margin := chooserModalMinMargin
	maxContent := screenW - margin*2 - 4
	if maxContent < 6 {
		return dialogRect{}, 0, false
	}
	if maxByCap := chooserModalMaxWidth - 4; contentW > maxByCap {
		contentW = maxByCap
	}
	if contentW > maxContent {
		contentW = maxContent
	}

	// Blank separator rows around the list give it breathing room. They only
	// appear when there is a list, so the text-input prompt stays compact.
	hasHeader := d.showQuery || d.subtitle != ""
	spaceBeforeList := len(d.rows) > 0 && hasHeader
	spaceAfterList := len(d.rows) > 0 && len(d.footer) > 0

	w := contentW + 4 // border + pad on each side
	h := 2            // top + bottom borders
	if d.showQuery {
		h++
	}
	if d.subtitle != "" {
		h++
	}
	if spaceBeforeList {
		h++
	}
	h += len(d.rows)
	if spaceAfterList {
		h++
	}
	if len(d.footer) > 0 {
		h++
	}
	if h > screenH-margin*2 {
		return dialogRect{}, 0, false
	}

	rect := dialogRect{X: max((screenW-w)/2, 0), Y: max((screenH-h)/2, 0), W: w, H: h}

	bodyTop := rect.Y + 1
	if d.showQuery {
		bodyTop++
	}
	if d.subtitle != "" {
		bodyTop++
	}
	if spaceBeforeList {
		bodyTop++
	}
	return rect, bodyTop, true
}

// place composites the chrome centered in g and returns its bounding rect.
// A zero rect means the chrome did not fit and nothing was drawn.
func (d dialogChrome) place(g *ScreenGrid, st dialogStyles) dialogRect {
	if g == nil {
		return dialogRect{}
	}
	rect, _, ok := d.computeLayout(g.Width, g.Height)
	if !ok {
		return dialogRect{}
	}

	spaceBeforeList := len(d.rows) > 0 && (d.showQuery || d.subtitle != "")
	spaceAfterList := len(d.rows) > 0 && len(d.footer) > 0

	d.drawTopBorder(g, rect, st)
	cy := rect.Y + 1
	if d.showQuery {
		d.drawContentRow(g, rect, cy, "> "+d.query, "", st.text, st)
		cy++
	}
	if d.subtitle != "" {
		d.drawContentRow(g, rect, cy, d.subtitle, "", st.dim, st)
		cy++
	}
	if spaceBeforeList {
		d.drawContentRow(g, rect, cy, "", "", st.text, st)
		cy++
	}
	bodyTop := cy
	for _, row := range d.rows {
		rowStyle := st.text
		switch row.kind {
		case dialogRowSelected:
			rowStyle = st.selected
		case dialogRowDim:
			rowStyle = st.dim
		case dialogRowSection:
			rowStyle = st.section
		case dialogRowHeader:
			rowStyle = st.header
		}
		d.drawContentRow(g, rect, cy, row.text, row.desc, rowStyle, st)
		cy++
	}
	d.drawScrollbar(g, rect, bodyTop, len(d.rows), st)
	if spaceAfterList {
		d.drawContentRow(g, rect, cy, "", "", st.text, st)
		cy++
	}
	if len(d.footer) > 0 {
		d.drawFooter(g, rect, cy, st)
	}
	d.drawBottomBorder(g, rect, st)
	return rect
}

// emitDialogChrome places the chrome into a scratch grid and writes its rect to
// buf as styled ANSI. This lets the direct-ANSI render path (RenderFull) reuse
// the exact same cell placement as the grid/diff path, so both stay in sync.
func emitDialogChrome(buf *strings.Builder, screenW, screenH int, d dialogChrome, profile termenv.Profile) {
	if buf == nil || screenW <= 0 || screenH <= 0 {
		return
	}
	g := NewScreenGrid(screenW, screenH)
	rect := d.place(g, defaultDialogStyles())
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	changes := make([]CellChange, 0, rect.W*rect.H)
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			changes = append(changes, CellChange{X: x, Y: y, Cell: g.Get(x, y)})
		}
	}
	state := emittedCellState{}
	buf.WriteString(emitDiffWithProfileState(changes, profile, &state, true))
	buf.WriteString(Reset)
}

// contentWidth returns the display width needed for the interior content.
func (d dialogChrome) contentWidth() int {
	w := 0
	consider := func(s string) {
		if cw := ansi.StringWidth(s); cw > w {
			w = cw
		}
	}
	if d.showQuery {
		consider("> " + d.query)
	}
	consider(d.subtitle)
	for _, row := range d.rows {
		consider(rowDisplay(row))
	}
	consider(footerText(d.footer))
	// The title sits in the top border row; reserve room for it (+ toggle).
	titleBar := d.title
	if d.toggle != nil {
		titleBar = d.title + "  " + d.toggle.display()
	}
	consider(titleBar)
	return w
}

func rowDisplay(r dialogRow) string {
	if r.desc != "" {
		return r.text + "  " + r.desc
	}
	return r.text
}

func (d dialogChrome) drawTopBorder(g *ScreenGrid, rect dialogRect, st dialogStyles) {
	right := rect.X + rect.W - 1
	g.Set(rect.X, rect.Y, ScreenCell{Char: "╭", Width: 1, Style: st.border})
	for x := rect.X + 1; x < right; x++ {
		g.Set(x, rect.Y, ScreenCell{Char: "─", Width: 1, Style: st.border})
	}
	g.Set(right, rect.Y, ScreenCell{Char: "╮", Width: 1, Style: st.border})

	// Title, padded with spaces, overlays the fill starting after the corner.
	titleEnd := placeRunes(g, rect.X+1, rect.Y, right, " "+d.title+" ", st.title)

	// Toggle, right-aligned with a one-cell gap before the corner.
	if d.toggle != nil {
		disp := d.toggle.display()
		start := right - 1 - ansi.StringWidth(disp)
		if start > titleEnd {
			placeRunes(g, start, rect.Y, right, disp, st.title)
		}
	}
}

func (d dialogChrome) drawBottomBorder(g *ScreenGrid, rect dialogRect, st dialogStyles) {
	right := rect.X + rect.W - 1
	bottom := rect.Y + rect.H - 1
	g.Set(rect.X, bottom, ScreenCell{Char: "╰", Width: 1, Style: st.border})
	for x := rect.X + 1; x < right; x++ {
		g.Set(x, bottom, ScreenCell{Char: "─", Width: 1, Style: st.border})
	}
	g.Set(right, bottom, ScreenCell{Char: "╯", Width: 1, Style: st.border})
}

// drawContentRow draws one interior row: vertical borders, a full-width
// background fill (so accent bars reach edge to edge), the padded text, and an
// optional dimmed description suffix.
func (d dialogChrome) drawContentRow(g *ScreenGrid, rect dialogRect, y int, text, desc string, rowStyle uv.Style, st dialogStyles) {
	right := rect.X + rect.W - 1
	g.Set(rect.X, y, ScreenCell{Char: "│", Width: 1, Style: st.border})
	g.Set(right, y, ScreenCell{Char: "│", Width: 1, Style: st.border})
	for x := rect.X + 1; x < right; x++ {
		g.Set(x, y, ScreenCell{Char: " ", Width: 1, Style: rowStyle})
	}
	// One-space gutter after the left border, then the text.
	endX := placeRunes(g, rect.X+2, y, right, text, rowStyle)
	if desc != "" {
		descStyle := uv.Style{Fg: hexToColor(config.DimColorHex), Bg: rowStyle.Bg}
		placeRunes(g, endX+2, y, right, desc, descStyle)
	}
}

// drawScrollbar draws a track (│) with a proportional thumb (┃) in the gutter
// just inside the right border, spanning the bodyRows body rows starting at
// bodyTop. It is a no-op unless the row set overflows the visible window.
func (d dialogChrome) drawScrollbar(g *ScreenGrid, rect dialogRect, bodyTop, bodyRows int, st dialogStyles) {
	s := d.scroll
	if s == nil || bodyRows <= 0 || s.total <= s.visible || s.total <= 0 {
		return
	}
	col := rect.X + rect.W - 2 // one cell inside the right border

	thumbLen := min(max(bodyRows*s.visible/s.total, 1), bodyRows)
	thumbPos := 0
	if denom := s.total - s.visible; denom > 0 {
		thumbPos = (bodyRows - thumbLen) * s.offset / denom
	}

	for i := range bodyRows {
		glyph := "│"
		style := st.dim
		if i >= thumbPos && i < thumbPos+thumbLen {
			glyph = "┃"
			style = st.border // accent-colored thumb
		}
		g.Set(col, bodyTop+i, ScreenCell{Char: glyph, Width: 1, Style: style})
	}
}

// drawFooter renders the keybinding hints with bold/bright keys, dim labels,
// and a dim " • " between hints — matching crush's footer styling.
func (d dialogChrome) drawFooter(g *ScreenGrid, rect dialogRect, y int, st dialogStyles) {
	right := rect.X + rect.W - 1
	g.Set(rect.X, y, ScreenCell{Char: "│", Width: 1, Style: st.border})
	g.Set(right, y, ScreenCell{Char: "│", Width: 1, Style: st.border})
	for x := rect.X + 1; x < right; x++ {
		g.Set(x, y, ScreenCell{Char: " ", Width: 1, Style: st.footer})
	}
	x := rect.X + 2
	for i, h := range d.footer {
		if i > 0 {
			x = placeRunes(g, x, y, right, " • ", st.footer)
		}
		x = placeRunes(g, x, y, right, h.key, st.footerKey)
		x = placeRunes(g, x, y, right, " ", st.footer)
		x = placeRunes(g, x, y, right, h.label, st.footer)
	}
}
