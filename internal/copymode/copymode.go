package copymode

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/render"
)

// TerminalEmulator is the subset of a pane's emulator that copy mode needs.
type TerminalEmulator interface {
	Size() (width, height int)
	ScrollbackLen() int
	ScrollbackLineText(y int) string // plain text of scrollback line y (0=oldest)
	ScreenLineText(y int) string     // plain text of screen line y (0=top row)
}

// Match represents a single search hit in the scrollback/screen buffer.
type Match struct {
	LineIdx int // absolute line index (0 = oldest scrollback line)
	Col     int // start column
	Len     int // match length
}

// Action tells the client what to do after HandleInput processes a key.
type Action int

const (
	ActionNone   Action = iota
	ActionRedraw        // need to re-render the viewport
	ActionExit          // exit copy mode
	ActionYank          // copy selection to clipboard
)

// CopyMode manages the state machine for scrollback browsing, search, and
// text selection within a single pane.
type CopyMode struct {
	emu    TerminalEmulator
	width  int // pane content width
	height int // pane content height (viewport rows)

	oy int // scroll offset from bottom (0 = live view)
	cx int // cursor column within viewport (0-indexed)
	cy int // cursor row within viewport (0-indexed)

	// Search state
	searching   bool
	searchBuf   string
	searchQuery string
	matches     []Match
	matchIdx    int // current match index (-1 = none)

	// Selection state
	selecting  bool
	lineSelect bool // true = full-line selection (V), false = character selection (v)
	selStartX  int  // start column (viewport-relative)
	selStartY  int  // start absolute line index
	selEndX    int
	selEndY    int

	// Character search state (f/F/t/T)
	pendingCharSearch byte // 0=none, 'f'/'F'/'t'/'T'=awaiting target
	lastCharSearch    byte // last f/F/t/T command (for ;/, repeat)
	lastCharTarget    byte // last target character

	scrollExit bool
}

// New creates a CopyMode for the given emulator and viewport size.
// cursorRow sets the initial cursor row (0-indexed from top of viewport).
func New(emu TerminalEmulator, width, height, cursorRow int) *CopyMode {
	return &CopyMode{
		emu:      emu,
		width:    width,
		height:   height,
		cy:       clamp(cursorRow, 0, max(0, height-1)),
		matchIdx: -1,
	}
}

// HandleInput processes raw input bytes and returns the action the client
// should take. When searching, printable keys build the query; otherwise
// vi-style keys control scrolling, search, and selection.
//
// All bytes in data are processed. ActionExit and ActionYank are returned
// immediately (remaining bytes are dropped). ActionRedraw is accumulated
// so that batched keystrokes (e.g. rapid "Vy") are fully handled.
func (cm *CopyMode) HandleInput(data []byte) Action {
	if len(data) == 0 {
		return ActionNone
	}

	result := ActionNone
	for len(data) > 0 {
		var action Action
		if cm.searching {
			cm.scrollExit = false
			var consumed int
			action, consumed = cm.handleSearchInput(data)
			data = data[consumed:]
		} else if cm.pendingCharSearch != 0 {
			cm.scrollExit = false
			if data[0] == 0x1b { // Escape cancels pending search
				cm.pendingCharSearch = 0
			} else {
				action = cm.executeCharSearch(cm.pendingCharSearch, data[0])
				cm.pendingCharSearch = 0
			}
			data = data[1:]
		} else {
			if !isScrollKey(data[0]) {
				cm.scrollExit = false
			}
			action = cm.handleNormalKey(data[0])
			data = data[1:]
		}
		switch action {
		case ActionExit, ActionYank:
			return action
		case ActionRedraw:
			result = ActionRedraw
		}
	}
	return result
}

func isScrollKey(b byte) bool {
	switch b {
	case 'j', 'k', 'g', 'G', 0x04, 0x15, 0x02, 0x06:
		return true
	default:
		return false
	}
}

func (cm *CopyMode) handleSearchInput(data []byte) (Action, int) {
	action := ActionNone
	for i, b := range data {
		switch {
		case b == '\r' || b == '\n': // Enter — confirm search
			cm.searchQuery = cm.searchBuf
			cm.searching = false
			cm.runSearch()
			return ActionRedraw, i + 1
		case b == 0x1b: // Escape — cancel search
			cm.searching = false
			cm.searchBuf = ""
			return ActionRedraw, i + 1
		case b == 0x7f: // Backspace
			if len(cm.searchBuf) > 0 {
				cm.searchBuf = cm.searchBuf[:len(cm.searchBuf)-1]
			}
			action = ActionRedraw
		default:
			if b >= 0x20 && b < 0x7f { // printable ASCII
				cm.searchBuf += string(rune(b))
				action = ActionRedraw
			}
		}
	}
	return action, len(data)
}

func (cm *CopyMode) handleNormalKey(b byte) Action {
	switch b {
	case 'q', 0x1b: // quit / Escape
		return ActionExit

	case 'j': // move cursor down
		if !cm.moveDown() {
			return ActionNone
		}
		cm.updateSelection()
		return ActionRedraw

	case 'k': // move cursor up
		if !cm.moveUp() {
			return ActionNone
		}
		cm.updateSelection()
		return ActionRedraw

	case 'h': // move cursor left
		if cm.cx > 0 {
			cm.cx--
			cm.updateSelection()
			return ActionRedraw
		}
		return ActionNone

	case 'l': // move cursor right
		if cm.cx < cm.width-1 {
			cm.cx++
			cm.updateSelection()
			return ActionRedraw
		}
		return ActionNone

	case 0x04: // Ctrl-d — half page down
		half := cm.height / 2
		cm.oy = clamp(cm.oy-half, 0, cm.maxOY())
		cm.updateSelection()
		return ActionRedraw

	case 0x15: // Ctrl-u — half page up
		half := cm.height / 2
		cm.oy = clamp(cm.oy+half, 0, cm.maxOY())
		cm.updateSelection()
		return ActionRedraw

	case 'g': // scroll to top
		cm.oy = cm.maxOY()
		cm.cy = 0
		cm.updateSelection()
		return ActionRedraw

	case 'G': // scroll to bottom
		cm.oy = 0
		cm.cy = cm.height - 1
		cm.updateSelection()
		return ActionRedraw

	case '/': // enter search mode
		cm.searching = true
		cm.searchBuf = ""
		return ActionRedraw

	case 'n': // next search match
		cm.nextMatch()
		return ActionRedraw

	case 'N': // previous search match
		cm.prevMatch()
		return ActionRedraw

	case 'v': // toggle character selection
		if cm.lineSelect {
			// Switch from line-select to character-select at cursor position.
			cm.lineSelect = false
			absY := cm.cursorAbsLine()
			cm.selStartX = cm.cx
			cm.selStartY = absY
			cm.selEndX = cm.cx
			cm.selEndY = absY
		} else {
			cm.selecting = !cm.selecting
			if cm.selecting {
				absY := cm.cursorAbsLine()
				cm.selStartX = cm.cx
				cm.selStartY = absY
				cm.selEndX = cm.cx
				cm.selEndY = absY
			}
		}
		return ActionRedraw

	case 'V': // toggle line selection
		cm.lineSelect = !cm.lineSelect
		cm.selecting = cm.lineSelect
		if cm.selecting {
			absY := cm.cursorAbsLine()
			cm.selStartX = 0
			cm.selStartY = absY
			cm.selEndX = cm.width - 1
			cm.selEndY = absY
		}
		return ActionRedraw

	case 'y': // yank selection
		if cm.selecting {
			return ActionYank
		}
		return ActionNone

	case '0': // beginning of line
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw

	case '$': // end of line
		cm.cx = cm.lineEndCol()
		cm.updateSelection()
		return ActionRedraw

	case '^': // first non-blank character
		cm.cx = cm.firstNonBlankCol()
		cm.updateSelection()
		return ActionRedraw

	case 'W': // forward WORD
		return cm.moveWordForward()

	case 'B': // backward WORD
		return cm.moveWordBackward()

	case 'E': // end of WORD
		return cm.moveWordEnd()

	case 0x06: // Ctrl-f — full page down
		cm.oy = clamp(cm.oy-cm.height, 0, cm.maxOY())
		cm.updateSelection()
		return ActionRedraw

	case 0x02: // Ctrl-b — full page up
		cm.oy = clamp(cm.oy+cm.height, 0, cm.maxOY())
		cm.updateSelection()
		return ActionRedraw

	case 'f', 'F', 't', 'T': // character search — await target
		cm.pendingCharSearch = b
		return ActionNone

	case ';': // repeat last character search
		return cm.repeatCharSearch(false)

	case ',': // repeat last character search (reversed)
		return cm.repeatCharSearch(true)
	}
	return ActionNone
}

// IsSearching returns true when the user is typing a search query.
func (cm *CopyMode) IsSearching() bool {
	return cm.searching
}

// SearchQuery returns the last confirmed search query.
func (cm *CopyMode) SearchQuery() string {
	return cm.searchQuery
}

// SetScrollExit enables or disables auto-exit when scrolling back to live view.
func (cm *CopyMode) SetScrollExit(enabled bool) {
	cm.scrollExit = enabled
}

// ScrollExit reports whether auto-exit-on-bottom is currently armed.
func (cm *CopyMode) ScrollExit() bool {
	return cm.scrollExit
}

// ScrollOffset returns the current scroll offset from the bottom.
func (cm *CopyMode) ScrollOffset() int {
	return cm.oy
}

// WheelScrollUp scrolls the viewport upward without moving the copy-mode cursor.
func (cm *CopyMode) WheelScrollUp(lines int) Action {
	if lines <= 0 {
		return ActionNone
	}
	next := clamp(cm.oy+lines, 0, cm.maxOY())
	if next == cm.oy {
		return ActionNone
	}
	cm.oy = next
	return ActionRedraw
}

// WheelScrollDown scrolls the viewport downward without moving the cursor.
// When scroll-exit is armed, reaching live view exits copy mode.
func (cm *CopyMode) WheelScrollDown(lines int) Action {
	if lines <= 0 {
		return ActionNone
	}
	next := clamp(cm.oy-lines, 0, cm.maxOY())
	if next == cm.oy {
		if cm.scrollExit && cm.oy == 0 {
			return ActionExit
		}
		return ActionNone
	}
	cm.oy = next
	if cm.scrollExit && cm.oy == 0 {
		return ActionExit
	}
	return ActionRedraw
}

// CursorPos returns the cursor position within the viewport (0-indexed).
func (cm *CopyMode) CursorPos() (cx, cy int) {
	return cm.cx, cm.cy
}

// TotalLines returns the total number of lines available (scrollback + screen).
func (cm *CopyMode) TotalLines() int {
	return cm.emu.ScrollbackLen() + cm.height
}

// Resize updates the viewport dimensions.
func (cm *CopyMode) Resize(width, height int) {
	cm.width = width
	cm.height = height
	cm.oy = clamp(cm.oy, 0, cm.maxOY())
	cm.cy = clamp(cm.cy, 0, cm.height-1)
	cm.cx = clamp(cm.cx, 0, cm.width-1)
}

// SelectedText returns the text between the selection start and end.
// Returns empty string when no selection is active.
func (cm *CopyMode) SelectedText() string {
	if !cm.selecting {
		return ""
	}

	startY, startX, endY, endX := cm.normalizedSelection()

	// Line-select mode: grab full lines with trailing newline.
	if cm.lineSelect {
		var buf strings.Builder
		for y := startY; y <= endY; y++ {
			buf.WriteString(cm.lineText(y))
			buf.WriteByte('\n')
		}
		return buf.String()
	}

	if startY == endY {
		line := cm.lineText(startY)
		if startX >= len(line) {
			return ""
		}
		return line[startX:min(endX+1, len(line))]
	}

	var buf strings.Builder
	for y := startY; y <= endY; y++ {
		line := cm.lineText(y)
		switch {
		case y == startY:
			if startX < len(line) {
				buf.WriteString(line[startX:])
			}
			buf.WriteByte('\n')
		case y == endY:
			end := min(endX+1, len(line))
			if end > 0 {
				buf.WriteString(line[:end])
			}
		default:
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// normalizedSelection returns the selection bounds with start <= end.
func (cm *CopyMode) normalizedSelection() (startY, startX, endY, endX int) {
	startY, startX = cm.selStartY, cm.selStartX
	endY, endX = cm.selEndY, cm.selEndX
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}
	if cm.lineSelect {
		startX = 0
		endX = cm.width - 1
	}
	return
}

// updateSelection updates the selection end point to the current cursor position.
func (cm *CopyMode) updateSelection() {
	if cm.selecting {
		cm.selEndY = cm.cursorAbsLine()
		if cm.lineSelect {
			cm.selEndX = cm.width - 1
		} else {
			cm.selEndX = cm.cx
		}
	}
}

// cursorAbsLine returns the absolute line index the cursor is on.
func (cm *CopyMode) cursorAbsLine() int {
	return cm.TotalLines() - cm.height - cm.oy + cm.cy
}

// maxOY returns the maximum scroll offset (fully scrolled to top).
func (cm *CopyMode) maxOY() int {
	return cm.emu.ScrollbackLen()
}

// lineText returns the plain text for an absolute line index.
// Lines 0..scrollbackLen-1 come from the emulator's scrollback buffer.
// Lines scrollbackLen..totalLines-1 come from the current screen.
func (cm *CopyMode) lineText(absIdx int) string {
	sbLen := cm.emu.ScrollbackLen()
	if absIdx < sbLen {
		return cm.emu.ScrollbackLineText(absIdx)
	}
	screenRow := absIdx - sbLen
	return cm.emu.ScreenLineText(screenRow)
}

// runSearch finds all case-insensitive occurrences of searchQuery across
// all lines and jumps to the nearest match at or below the current cursor.
func (cm *CopyMode) runSearch() {
	cm.matches = nil
	cm.matchIdx = -1

	if cm.searchQuery == "" {
		return
	}

	query := strings.ToLower(cm.searchQuery)
	total := cm.TotalLines()

	for i := 0; i < total; i++ {
		line := strings.ToLower(cm.lineText(i))
		off := 0
		for {
			idx := strings.Index(line[off:], query)
			if idx < 0 {
				break
			}
			cm.matches = append(cm.matches, Match{
				LineIdx: i,
				Col:     off + idx,
				Len:     len(cm.searchQuery),
			})
			off += idx + len(query)
		}
	}

	if len(cm.matches) == 0 {
		return
	}

	// Jump to the nearest match at or below the cursor's absolute position.
	cursorAbs := cm.cursorAbsLine()
	cm.matchIdx = 0
	for i, m := range cm.matches {
		if m.LineIdx >= cursorAbs {
			cm.matchIdx = i
			break
		}
	}
	cm.scrollToMatch()
}

// nextMatch advances to the next search match (wrapping around).
func (cm *CopyMode) nextMatch() {
	if len(cm.matches) == 0 {
		return
	}
	cm.matchIdx = (cm.matchIdx + 1) % len(cm.matches)
	cm.scrollToMatch()
}

// prevMatch moves to the previous search match (wrapping around).
func (cm *CopyMode) prevMatch() {
	if len(cm.matches) == 0 {
		return
	}
	cm.matchIdx = (cm.matchIdx - 1 + len(cm.matches)) % len(cm.matches)
	cm.scrollToMatch()
}

// scrollToMatch adjusts oy and cy so the current match is visible in the viewport
// and the cursor is positioned on the match line.
func (cm *CopyMode) scrollToMatch() {
	if cm.matchIdx < 0 || cm.matchIdx >= len(cm.matches) {
		return
	}
	m := cm.matches[cm.matchIdx]
	// Place the match line in the center of the viewport.
	center := cm.height / 2
	cm.oy = clamp(cm.TotalLines()-cm.height-m.LineIdx+center, 0, cm.maxOY())
	// Position cursor on the match line within the viewport.
	firstVisible := cm.TotalLines() - cm.height - cm.oy
	cm.cy = clamp(m.LineIdx-firstVisible, 0, cm.height-1)
	cm.cx = m.Col
}

// ViewportHeight returns the viewport height in rows.
func (cm *CopyMode) ViewportHeight() int {
	return cm.height
}

// FirstVisibleLine returns the absolute line index of the first visible row.
func (cm *CopyMode) FirstVisibleLine() int {
	return max(0, cm.TotalLines()-cm.height-cm.oy)
}

// LineText returns the plain text for an absolute line index (exported wrapper).
func (cm *CopyMode) LineText(absIdx int) string {
	return cm.lineText(absIdx)
}

// Copy mode cell colors — using basic ANSI colors to match terminal themes.
var (
	copySelectionBg = ansi.BasicColor(4)  // blue
	copyMatchBg     = ansi.BasicColor(3)  // yellow
	copyCurrentBg   = ansi.BasicColor(11) // bright yellow
)

// CellAt returns the cell at (col, viewportRow) with copy mode overlays applied.
// The base character comes from the line text. Selection, search matches, and
// the cursor are overlaid as style changes.
func (cm *CopyMode) CellAt(col, viewportRow int) render.ScreenCell {
	absIdx := cm.FirstVisibleLine() + viewportRow
	line := cm.lineText(absIdx)
	runes := []rune(line)

	char := " "
	if col < len(runes) {
		char = string(runes[col])
	}

	sc := render.ScreenCell{Char: char, Width: 1}

	// Selection overlay.
	if cm.selecting {
		startY, startX, endY, endX := cm.normalizedSelection()
		if absIdx >= startY && absIdx <= endY {
			colStart := 0
			colEnd := cm.width
			if absIdx == startY {
				colStart = startX
			}
			if absIdx == endY {
				colEnd = endX + 1
			}
			if col >= colStart && col < colEnd {
				sc.Style.Bg = copySelectionBg
			}
		}
	}

	// Search match overlay (takes priority over selection).
	for i, m := range cm.matches {
		if m.LineIdx != absIdx {
			continue
		}
		if col >= m.Col && col < m.Col+m.Len {
			if i == cm.matchIdx {
				sc.Style.Bg = copyCurrentBg
				sc.Style.Attrs |= uv.AttrBold
			} else {
				sc.Style.Bg = copyMatchBg
			}
		}
	}

	// Cursor overlay (takes priority over everything).
	if viewportRow == cm.cy && col == cm.cx {
		sc.Style.Attrs |= uv.AttrReverse
	}

	return sc
}

// clamp returns v clamped to the range [lo, hi].
func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}
