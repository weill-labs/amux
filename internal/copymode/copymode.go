package copymode

import (
	"strconv"
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

type promptMode uint8

const (
	promptNone promptMode = iota
	promptSearchForward
	promptSearchBackward
	promptGotoLine
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

	// Prompt/search state.
	prompt            promptMode
	promptBuf         string
	searchQuery       string
	lastSearchForward bool
	matches           []Match
	matchIdx          int // current match index (-1 = none)

	// Selection state
	selecting  bool
	lineSelect bool
	rectSelect bool
	selStartX  int // start column (viewport-relative)
	selStartY  int // start absolute line index
	selEndX    int
	selEndY    int

	// Character search state (f/F/t/T)
	pendingCharSearch      byte // 0=none, 'f'/'F'/'t'/'T'=awaiting target
	pendingCharSearchCount int
	lastCharSearch         byte // last f/F/t/T command (for ;/, repeat)
	lastCharTarget         byte // last target character

	// Repeat counts and copy payload.
	pendingCount int
	copyText     string
	appendCopy   bool

	// Mark and position indicator.
	markSet      bool
	markX, markY int
	showPosition bool

	scrollExit bool
}

// New creates a CopyMode for the given emulator and viewport size.
// cursorRow sets the initial cursor row (0-indexed from top of viewport).
func New(emu TerminalEmulator, width, height, cursorRow int) *CopyMode {
	return &CopyMode{
		emu:               emu,
		width:             width,
		height:            height,
		cy:                clamp(cursorRow, 0, max(0, height-1)),
		matchIdx:          -1,
		lastSearchForward: true,
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
		action, consumed := cm.handleInputChunk(data)
		data = data[consumed:]
		switch action {
		case ActionExit, ActionYank:
			return action
		case ActionRedraw:
			result = ActionRedraw
		}
	}
	return result
}

const (
	keyUp = 0x100 + iota
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyCtrlUp
	keyCtrlDown
	keyAltX
)

func (cm *CopyMode) handleInputChunk(data []byte) (Action, int) {
	if cm.prompt != promptNone {
		cm.scrollExit = false
		return cm.handlePromptInput(data)
	}

	if cm.pendingCharSearch != 0 {
		cm.scrollExit = false
		key, consumed := decodeCopyModeKey(data)
		if key == 0x1b {
			cm.pendingCharSearch = 0
			cm.pendingCharSearchCount = 0
			return ActionNone, consumed
		}
		if key < 0 || key > 0xff {
			cm.pendingCharSearch = 0
			cm.pendingCharSearchCount = 0
			return ActionNone, consumed
		}
		action := cm.executeCharSearch(cm.pendingCharSearch, byte(key), cm.consumePendingCharCount())
		cm.pendingCharSearch = 0
		return action, consumed
	}

	key, consumed := decodeCopyModeKey(data)
	if !isScrollKey(key) {
		cm.scrollExit = false
	}
	return cm.handleNormalKey(key), consumed
}

func decodeCopyModeKey(data []byte) (int, int) {
	if len(data) == 0 {
		return -1, 0
	}
	if data[0] != 0x1b {
		return int(data[0]), 1
	}
	if len(data) == 1 {
		return 0x1b, 1
	}
	if data[1] != '[' && data[1] != 'O' {
		if data[1] == 'x' || data[1] == 'X' {
			return keyAltX, 2
		}
		return 0x1b, 1
	}
	if data[1] == 'O' {
		if len(data) < 3 {
			return 0x1b, 1
		}
		switch data[2] {
		case 'H':
			return keyHome, 3
		case 'F':
			return keyEnd, 3
		default:
			return 0x1b, 1
		}
	}
	if len(data) < 3 {
		return 0x1b, 1
	}
	switch data[2] {
	case 'A':
		return keyUp, 3
	case 'B':
		return keyDown, 3
	case 'C':
		return keyRight, 3
	case 'D':
		return keyLeft, 3
	case 'H':
		return keyHome, 3
	case 'F':
		return keyEnd, 3
	}

	end := 2
	for end < len(data) && ((data[end] >= '0' && data[end] <= '9') || data[end] == ';') {
		end++
	}
	if end >= len(data) {
		return 0x1b, 1
	}
	params := string(data[2:end])
	final := data[end]
	switch {
	case final == '~' && params == "5":
		return keyPageUp, end + 1
	case final == '~' && params == "6":
		return keyPageDown, end + 1
	case final == 'A' && (params == "1;5" || params == "5"):
		return keyCtrlUp, end + 1
	case final == 'B' && (params == "1;5" || params == "5"):
		return keyCtrlDown, end + 1
	default:
		return 0x1b, 1
	}
}

func isScrollKey(key int) bool {
	switch key {
	case 'j', 'k', 'g', 'G', 'J', 'K', keyUp, keyDown, keyPageUp, keyPageDown,
		keyCtrlUp, keyCtrlDown, 0x04, 0x15, 0x02, 0x06:
		return true
	default:
		return false
	}
}

func (cm *CopyMode) handlePromptInput(data []byte) (Action, int) {
	key := int(data[0])
	switch key {
	case '\r', '\n':
		buf := cm.promptBuf
		mode := cm.prompt
		cm.prompt = promptNone
		cm.promptBuf = ""
		switch mode {
		case promptSearchForward:
			cm.searchQuery = buf
			cm.lastSearchForward = true
			cm.runSearch(true)
		case promptSearchBackward:
			cm.searchQuery = buf
			cm.lastSearchForward = false
			cm.runSearch(false)
		case promptGotoLine:
			if line, err := strconv.Atoi(buf); err == nil {
				cm.oy = clamp(line, 0, cm.maxOY())
				cm.updateSelection()
			}
		}
		return ActionRedraw, 1
	case 0x1b:
		cm.prompt = promptNone
		cm.promptBuf = ""
		return ActionRedraw, 1
	case 0x7f, 0x08:
		if len(cm.promptBuf) > 0 {
			cm.promptBuf = cm.promptBuf[:len(cm.promptBuf)-1]
			return ActionRedraw, 1
		}
		return ActionNone, 1
	default:
		if key >= 0x20 && key < 0x7f {
			if cm.prompt == promptGotoLine && (key < '0' || key > '9') {
				return ActionNone, 1
			}
			cm.promptBuf += string(rune(key))
			return ActionRedraw, 1
		}
		return ActionNone, 1
	}
}

func (cm *CopyMode) handleNormalKey(key int) Action {
	if key >= '1' && key <= '9' {
		cm.pendingCount = cm.pendingCount*10 + (key - '0')
		return ActionRedraw
	}
	if key == '0' && cm.pendingCount > 0 {
		cm.pendingCount *= 10
		return ActionRedraw
	}

	count := cm.consumeCount()
	switch key {
	case 'q', 0x03: // q / Ctrl-c
		return ActionExit

	case 0x1b: // Escape clears selection in tmux vi copy mode.
		cm.pendingCount = 0
		return cm.ClearSelection()

	case 'j', keyDown:
		return cm.repeatMotion(count, cm.moveDown)

	case 'k', keyUp:
		return cm.repeatMotion(count, cm.moveUp)

	case 'h', keyLeft, 0x08, 0x7f:
		return cm.repeatAction(count, func() Action {
			if cm.cx == 0 {
				return ActionNone
			}
			cm.cx--
			cm.updateSelection()
			return ActionRedraw
		})

	case 'l', keyRight:
		return cm.repeatAction(count, func() Action {
			if cm.cx >= cm.width-1 {
				return ActionNone
			}
			cm.cx++
			cm.updateSelection()
			return ActionRedraw
		})

	case 0x04: // Ctrl-d — half page down
		return cm.scrollBy(-(cm.height/2)*count, false)

	case 0x15: // Ctrl-u — half page up
		return cm.scrollBy((cm.height/2)*count, false)

	case 'g': // scroll to top
		cm.oy = cm.maxOY()
		cm.cy = 0
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw

	case 'G': // scroll to bottom
		cm.oy = 0
		cm.cy = cm.height - 1
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw

	case '/':
		cm.prompt = promptSearchForward
		cm.promptBuf = ""
		return ActionRedraw

	case '?':
		cm.prompt = promptSearchBackward
		cm.promptBuf = ""
		return ActionRedraw

	case ':':
		cm.prompt = promptGotoLine
		cm.promptBuf = ""
		return ActionRedraw

	case 'n':
		cm.searchAgain(false)
		return ActionRedraw

	case 'N':
		cm.searchAgain(true)
		return ActionRedraw

	case '*':
		query := cm.wordUnderCursor()
		if query == "" {
			return ActionNone
		}
		cm.searchQuery = query
		cm.lastSearchForward = true
		cm.runSearch(true)
		return ActionRedraw

	case '#':
		query := cm.wordUnderCursor()
		if query == "" {
			return ActionNone
		}
		cm.searchQuery = query
		cm.lastSearchForward = false
		cm.runSearch(false)
		return ActionRedraw

	case ' ':
		return cm.StartSelection()

	case 'v', 0x16:
		return cm.ToggleRectangleSelection()

	case 'V':
		action := cm.SelectLine()
		for i := 1; i < count; i++ {
			if !cm.moveDown() {
				break
			}
		}
		cm.updateSelection()
		if action == ActionNone && count > 1 {
			return ActionRedraw
		}
		return ActionRedraw

	case '\r', '\n':
		cm.queueCopyText(cm.SelectedText(), false)
		return ActionYank

	case 'A':
		cm.queueCopyText(cm.SelectedText(), true)
		return ActionYank

	case 'D':
		cm.queueCopyText(cm.endOfLineText(), false)
		return ActionYank

	case 'o':
		if count%2 == 0 {
			return ActionNone
		}
		return cm.OtherEnd()

	case '0', keyHome:
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw

	case '$', keyEnd:
		cm.cx = cm.lineEndCol()
		cm.updateSelection()
		return ActionRedraw

	case '^': // first non-blank character
		cm.cx = cm.firstNonBlankCol()
		cm.updateSelection()
		return ActionRedraw

	case 'H':
		cm.cx = 0
		cm.cy = 0
		cm.updateSelection()
		return ActionRedraw

	case 'L':
		cm.cx = 0
		cm.cy = cm.height - 1
		cm.updateSelection()
		return ActionRedraw

	case 'M':
		cm.cx = 0
		cm.cy = (cm.height - 1) / 2
		cm.updateSelection()
		return ActionRedraw

	case 'z':
		return cm.scrollCursorToRow((cm.height - 1) / 2)

	case 'J', 0x05, keyCtrlDown:
		return cm.WheelScrollDown(count)

	case 'K', 0x19, keyCtrlUp:
		return cm.WheelScrollUp(count)

	case 'P':
		cm.showPosition = !cm.showPosition
		return ActionRedraw

	case 'r':
		if cm.searchQuery != "" {
			cm.runSearch(cm.lastSearchForward)
		}
		return ActionRedraw

	case 'X':
		cm.markSet = true
		cm.markX = cm.cx
		cm.markY = cm.cursorAbsLine()
		return ActionRedraw

	case 'W': // forward WORD
		return cm.repeatAction(count, cm.moveWordForward)

	case 'B': // backward WORD
		return cm.repeatAction(count, cm.moveWordBackward)

	case 'E': // end of WORD
		return cm.repeatAction(count, cm.moveWordEnd)

	case 'w':
		return cm.repeatAction(count, cm.moveViWordForward)

	case 'b':
		return cm.repeatAction(count, cm.moveViWordBackward)

	case 'e':
		return cm.repeatAction(count, cm.moveViWordEnd)

	case 0x06, keyPageDown:
		return cm.scrollBy(-cm.height*count, false)

	case 0x02, keyPageUp:
		return cm.scrollBy(cm.height*count, false)

	case 'f', 'F', 't', 'T':
		cm.pendingCharSearch = byte(key)
		cm.pendingCharSearchCount = count
		return ActionNone

	case ';':
		return cm.repeatAction(count, func() Action { return cm.repeatCharSearch(false) })

	case ',':
		return cm.repeatAction(count, func() Action { return cm.repeatCharSearch(true) })

	case '{':
		return cm.repeatAction(count, cm.previousParagraph)

	case '}':
		return cm.repeatAction(count, cm.nextParagraph)

	case '%':
		return cm.repeatAction(count, cm.matchingBracket)

	case keyAltX:
		if !cm.markSet {
			return ActionNone
		}
		return cm.jumpToMark()

	case 'y': // amux keeps y as an extra copy shortcut
		cm.queueCopyText(cm.SelectedText(), false)
		return ActionYank
	}
	return ActionNone
}

// IsSearching returns true when the user is typing a search query.
func (cm *CopyMode) IsSearching() bool {
	return cm.prompt == promptSearchForward || cm.prompt == promptSearchBackward
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
	cm.updateSelection()
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
	cm.updateSelection()
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
		return cm.currentMatchText()
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

	if cm.rectSelect {
		var buf strings.Builder
		for y := startY; y <= endY; y++ {
			line := []rune(cm.lineText(y))
			if startX < len(line) {
				buf.WriteString(string(line[startX:min(endX+1, len(line))]))
			}
			if y != endY {
				buf.WriteByte('\n')
			}
		}
		return buf.String()
	}

	if startY == endY {
		line := []rune(cm.lineText(startY))
		if startX >= len(line) {
			return ""
		}
		return string(line[startX:min(endX+1, len(line))])
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
	if cm.rectSelect {
		if startY > endY {
			startY, endY = endY, startY
		}
		if startX > endX {
			startX, endX = endX, startX
		}
		return
	}
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
func (cm *CopyMode) runSearch(directions ...bool) {
	cm.matches = nil
	cm.matchIdx = -1

	if cm.searchQuery == "" {
		return
	}
	forward := cm.lastSearchForward
	if len(directions) > 0 {
		forward = directions[0]
	}

	queryRunes := []rune(strings.ToLower(cm.searchQuery))
	queryLen := len(queryRunes)
	if queryLen == 0 {
		return
	}
	total := cm.TotalLines()

	for i := 0; i < total; i++ {
		lineRunes := []rune(strings.ToLower(cm.lineText(i)))
		for idx := 0; idx+queryLen <= len(lineRunes); idx++ {
			if !runeSliceHasPrefix(lineRunes[idx:], queryRunes) {
				continue
			}
			cm.matches = append(cm.matches, Match{
				LineIdx: i,
				Col:     idx,
				Len:     queryLen,
			})
			idx += queryLen - 1
		}
	}

	if len(cm.matches) == 0 {
		return
	}

	cursorAbs := cm.cursorAbsLine()
	cursorCol := cm.cx
	cm.lastSearchForward = forward
	if forward {
		cm.matchIdx = 0
		for i, m := range cm.matches {
			if m.LineIdx > cursorAbs || (m.LineIdx == cursorAbs && m.Col >= cursorCol) {
				cm.matchIdx = i
				break
			}
		}
	} else {
		cm.matchIdx = len(cm.matches) - 1
		for i := len(cm.matches) - 1; i >= 0; i-- {
			m := cm.matches[i]
			if m.LineIdx < cursorAbs || (m.LineIdx == cursorAbs && m.Col <= cursorCol) {
				cm.matchIdx = i
				break
			}
		}
	}
	cm.scrollToMatch()
}

func runeSliceHasPrefix(line, query []rune) bool {
	if len(query) > len(line) {
		return false
	}
	for i := range query {
		if line[i] != query[i] {
			return false
		}
	}
	return true
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
			colStart, colEnd := 0, cm.width
			switch {
			case cm.rectSelect:
				colStart, colEnd = startX, endX+1
			default:
				if absIdx == startY {
					colStart = startX
				}
				if absIdx == endY {
					colEnd = endX + 1
				}
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

// SetCursor moves the copy-mode cursor to a viewport-relative position.
func (cm *CopyMode) SetCursor(col, viewportRow int) Action {
	row := clamp(viewportRow, 0, cm.height-1)
	line := []rune(cm.lineText(cm.FirstVisibleLine() + row))
	maxCol := 0
	if len(line) > 0 {
		maxCol = min(len(line)-1, cm.width-1)
	}
	col = clamp(col, 0, maxCol)
	if cm.cx == col && cm.cy == row {
		return ActionNone
	}
	cm.cx = col
	cm.cy = row
	cm.updateSelection()
	return ActionRedraw
}

// StartSelection begins a character selection at the current cursor position.
func (cm *CopyMode) StartSelection() Action {
	absY := cm.cursorAbsLine()
	cm.lineSelect = false
	cm.rectSelect = false
	cm.selecting = true
	cm.selStartX = cm.cx
	cm.selStartY = absY
	cm.selEndX = cm.cx
	cm.selEndY = absY
	return ActionRedraw
}

// ClearSelection removes the current selection without exiting copy mode.
func (cm *CopyMode) ClearSelection() Action {
	if !cm.selecting && !cm.lineSelect && !cm.rectSelect {
		return ActionNone
	}
	cm.selecting = false
	cm.lineSelect = false
	cm.rectSelect = false
	return ActionRedraw
}

// SelectLine begins a tmux-style line selection at the current cursor line.
func (cm *CopyMode) SelectLine() Action {
	absY := cm.cursorAbsLine()
	cm.selecting = true
	cm.lineSelect = true
	cm.rectSelect = false
	cm.selStartX = 0
	cm.selStartY = absY
	cm.selEndX = cm.width - 1
	cm.selEndY = absY
	return ActionRedraw
}

// SelectWord begins a tmux-style word selection around the cursor.
func (cm *CopyMode) SelectWord() Action {
	line := cm.cursorLineRunes()
	if len(line) == 0 {
		return ActionNone
	}
	pos := clamp(cm.cx, 0, len(line)-1)
	class := classifyViWordRune(line[pos])
	if class == wordClassWhitespace {
		for pos < len(line) && classifyViWordRune(line[pos]) == wordClassWhitespace {
			pos++
		}
		if pos >= len(line) {
			return ActionNone
		}
		class = classifyViWordRune(line[pos])
	}
	start, end := pos, pos
	for start > 0 && classifyViWordRune(line[start-1]) == class {
		start--
	}
	for end < len(line)-1 && classifyViWordRune(line[end+1]) == class {
		end++
	}
	absY := cm.cursorAbsLine()
	cm.selecting = true
	cm.lineSelect = false
	cm.rectSelect = false
	cm.selStartX = start
	cm.selStartY = absY
	cm.selEndX = end
	cm.selEndY = absY
	cm.cx = end
	return ActionRedraw
}

// ToggleRectangleSelection toggles tmux rectangle mode.
func (cm *CopyMode) ToggleRectangleSelection() Action {
	absY := cm.cursorAbsLine()
	if !cm.selecting {
		cm.selecting = true
		cm.selStartX = cm.cx
		cm.selStartY = absY
		cm.selEndX = cm.cx
		cm.selEndY = absY
	}
	cm.lineSelect = false
	cm.rectSelect = !cm.rectSelect
	cm.updateSelection()
	return ActionRedraw
}

// OtherEnd swaps the active and anchor selection endpoints.
func (cm *CopyMode) OtherEnd() Action {
	if !cm.selecting {
		return ActionNone
	}
	oldStartX, oldStartY := cm.selStartX, cm.selStartY
	cm.selStartX, cm.selStartY = cm.selEndX, cm.selEndY
	cm.selEndX, cm.selEndY = oldStartX, oldStartY
	cm.scrollToAbsolute(oldStartY, oldStartX)
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) queueCopyText(text string, append bool) {
	cm.copyText = text
	cm.appendCopy = append
}

// ConsumeCopyText returns and clears any queued copy payload.
func (cm *CopyMode) ConsumeCopyText() (string, bool) {
	text, appendCopy := cm.copyText, cm.appendCopy
	cm.copyText = ""
	cm.appendCopy = false
	return text, appendCopy
}

func (cm *CopyMode) consumeCount() int {
	if cm.pendingCount <= 0 {
		return 1
	}
	count := cm.pendingCount
	cm.pendingCount = 0
	return count
}

func (cm *CopyMode) consumePendingCharCount() int {
	if cm.pendingCharSearchCount <= 0 {
		return 1
	}
	count := cm.pendingCharSearchCount
	cm.pendingCharSearchCount = 0
	return count
}

func (cm *CopyMode) repeatMotion(count int, move func() bool) Action {
	moved := false
	for i := 0; i < count; i++ {
		if !move() {
			break
		}
		moved = true
	}
	if !moved {
		return ActionNone
	}
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) repeatAction(count int, fn func() Action) Action {
	result := ActionNone
	for i := 0; i < count; i++ {
		action := fn()
		if action == ActionExit || action == ActionYank {
			return action
		}
		if action == ActionRedraw {
			result = ActionRedraw
		}
	}
	return result
}

func (cm *CopyMode) scrollBy(delta int, scrollExit bool) Action {
	next := clamp(cm.oy+delta, 0, cm.maxOY())
	if next == cm.oy {
		if scrollExit && cm.scrollExit && cm.oy == 0 {
			return ActionExit
		}
		return ActionNone
	}
	cm.oy = next
	cm.updateSelection()
	if scrollExit && cm.scrollExit && cm.oy == 0 {
		return ActionExit
	}
	return ActionRedraw
}

func (cm *CopyMode) scrollCursorToRow(row int) Action {
	absY := cm.cursorAbsLine()
	cm.oy = clamp(cm.TotalLines()-cm.height-absY+row, 0, cm.maxOY())
	cm.cy = clamp(row, 0, cm.height-1)
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) searchAgain(reverse bool) {
	if len(cm.matches) == 0 {
		return
	}
	forward := cm.lastSearchForward
	if reverse {
		forward = !forward
	}
	if forward {
		cm.nextMatch()
	} else {
		cm.prevMatch()
	}
}

func (cm *CopyMode) wordUnderCursor() string {
	line := cm.cursorLineRunes()
	if len(line) == 0 {
		return ""
	}
	pos := clamp(cm.cx, 0, len(line)-1)
	class := classifyViWordRune(line[pos])
	if class == wordClassWhitespace {
		for pos < len(line) && classifyViWordRune(line[pos]) == wordClassWhitespace {
			pos++
		}
		if pos >= len(line) {
			return ""
		}
		class = classifyViWordRune(line[pos])
	}
	start, end := pos, pos
	for start > 0 && classifyViWordRune(line[start-1]) == class {
		start--
	}
	for end < len(line)-1 && classifyViWordRune(line[end+1]) == class {
		end++
	}
	return string(line[start : end+1])
}

func (cm *CopyMode) currentMatchText() string {
	if cm.matchIdx < 0 || cm.matchIdx >= len(cm.matches) {
		return ""
	}
	m := cm.matches[cm.matchIdx]
	if m.LineIdx != cm.cursorAbsLine() || cm.cx < m.Col || cm.cx >= m.Col+m.Len {
		return ""
	}
	line := []rune(cm.lineText(m.LineIdx))
	if m.Col >= len(line) {
		return ""
	}
	return string(line[m.Col:min(m.Col+m.Len, len(line))])
}

func (cm *CopyMode) endOfLineText() string {
	line := []rune(cm.lineText(cm.cursorAbsLine()))
	if cm.cx >= len(line) {
		return ""
	}
	return string(line[cm.cx:])
}

func (cm *CopyMode) scrollToAbsolute(absY, col int) {
	cm.oy = clamp(cm.TotalLines()-cm.height-absY+cm.cy, 0, cm.maxOY())
	firstVisible := cm.FirstVisibleLine()
	cm.cy = clamp(absY-firstVisible, 0, cm.height-1)
	cm.cx = clamp(col, 0, cm.width-1)
}

func (cm *CopyMode) jumpToMark() Action {
	if !cm.markSet {
		return ActionNone
	}
	oldX, oldY := cm.cx, cm.cursorAbsLine()
	targetX, targetY := cm.markX, cm.markY
	cm.scrollToAbsolute(targetY, targetX)
	cm.markX, cm.markY = oldX, oldY
	cm.updateSelection()
	return ActionRedraw
}

// clamp returns v clamped to the range [lo, hi].
func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}
