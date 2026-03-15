package copymode

import (
	"strings"
	"unicode"
)

// TerminalEmulator is the subset of a pane's emulator that copy mode needs.
type TerminalEmulator interface {
	Render() string
	Size() (width, height int)
	ScrollbackLen() int
	ScrollbackLineText(y int) string // plain text of scrollback line y (0=oldest)
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
	selecting bool
	selStartX int // start column (viewport-relative)
	selStartY int // start absolute line index
	selEndX   int
	selEndY   int
}

// New creates a CopyMode for the given emulator and viewport size.
// The cursor starts at the top-left of the viewport.
func New(emu TerminalEmulator, width, height int) *CopyMode {
	return &CopyMode{
		emu:      emu,
		width:    width,
		height:   height,
		oy:       0,
		cx:       0,
		cy:       0,
		matchIdx: -1,
	}
}

// HandleInput processes raw input bytes and returns the action the client
// should take. When searching, printable keys build the query; otherwise
// vi-style keys control scrolling, search, and selection.
func (cm *CopyMode) HandleInput(data []byte) Action {
	if len(data) == 0 {
		return ActionNone
	}

	if cm.searching {
		return cm.handleSearchInput(data)
	}
	return cm.handleNormalInput(data)
}

func (cm *CopyMode) handleSearchInput(data []byte) Action {
	action := ActionNone
	for _, b := range data {
		switch {
		case b == '\r' || b == '\n': // Enter — confirm search
			cm.searchQuery = cm.searchBuf
			cm.searching = false
			cm.runSearch()
			return ActionRedraw
		case b == 0x1b: // Escape — cancel search
			cm.searching = false
			cm.searchBuf = ""
			return ActionRedraw
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
	return action
}

func (cm *CopyMode) handleNormalInput(data []byte) Action {
	if len(data) == 0 {
		return ActionNone
	}

	b := data[0]
	switch b {
	case 'q', 0x1b: // quit / Escape
		return ActionExit

	case 'j': // scroll viewport down one line
		if cm.oy > 0 {
			cm.oy--
			return ActionRedraw
		}
		return ActionNone

	case 'k': // scroll viewport up one line
		if cm.oy < cm.maxOY() {
			cm.oy++
			return ActionRedraw
		}
		return ActionNone

	case 0x04: // Ctrl-d — half page down
		half := cm.height / 2
		cm.oy -= half
		if cm.oy < 0 {
			cm.oy = 0
		}
		return ActionRedraw

	case 0x15: // Ctrl-u — half page up
		half := cm.height / 2
		cm.oy += half
		if cm.oy > cm.maxOY() {
			cm.oy = cm.maxOY()
		}
		return ActionRedraw

	case 'g': // scroll to top
		cm.oy = cm.maxOY()
		return ActionRedraw

	case 'G': // scroll to bottom
		cm.oy = 0
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

	case 'v': // toggle selection
		cm.selecting = !cm.selecting
		if cm.selecting {
			absY := cm.TotalLines() - cm.height - cm.oy + cm.cy
			cm.selStartX = cm.cx
			cm.selStartY = absY
			cm.selEndX = cm.cx
			cm.selEndY = absY
		}
		return ActionRedraw

	case 'y': // yank selection
		if cm.selecting {
			return ActionYank
		}
		return ActionNone
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

// ScrollOffset returns the current scroll offset from the bottom.
func (cm *CopyMode) ScrollOffset() int {
	return cm.oy
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
	// Clamp scroll offset and cursor after resize.
	if cm.oy > cm.maxOY() {
		cm.oy = cm.maxOY()
	}
	if cm.cy >= cm.height {
		cm.cy = cm.height - 1
	}
	if cm.cx >= cm.width {
		cm.cx = cm.width - 1
	}
}

// SelectedText returns the text between the selection start and end.
// Returns empty string when no selection is active.
func (cm *CopyMode) SelectedText() string {
	if !cm.selecting {
		return ""
	}

	startY, startX := cm.selStartY, cm.selStartX
	endY, endX := cm.selEndY, cm.selEndX

	// Normalize so start <= end.
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	if startY == endY {
		line := cm.lineText(startY)
		if startX >= len(line) {
			return ""
		}
		end := endX + 1
		if end > len(line) {
			end = len(line)
		}
		return line[startX:end]
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
			end := endX + 1
			if end > len(line) {
				end = len(line)
			}
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
	// Screen line: parse from Render() output.
	screenRow := absIdx - sbLen
	rendered := cm.emu.Render()
	lines := strings.Split(rendered, "\n")
	if screenRow < len(lines) {
		return stripANSI(lines[screenRow])
	}
	return ""
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
	cursorAbs := cm.TotalLines() - cm.height - cm.oy + cm.cy
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
	cm.matchIdx--
	if cm.matchIdx < 0 {
		cm.matchIdx = len(cm.matches) - 1
	}
	cm.scrollToMatch()
}

// scrollToMatch adjusts oy so the current match is visible in the viewport.
func (cm *CopyMode) scrollToMatch() {
	if cm.matchIdx < 0 || cm.matchIdx >= len(cm.matches) {
		return
	}
	m := cm.matches[cm.matchIdx]
	// Convert absolute line index to the required scroll offset.
	// firstVisible = totalLines - height - oy => oy = totalLines - height - firstVisible
	// We want the match line visible, placing it at the cursor row (cy=0, top of viewport).
	cm.oy = cm.TotalLines() - cm.height - m.LineIdx
	if cm.oy < 0 {
		cm.oy = 0
	}
	if cm.oy > cm.maxOY() {
		cm.oy = cm.maxOY()
	}
}

// stripANSI removes ANSI escape sequences from a string, returning plain text.
func stripANSI(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			// Skip CSI sequences: ESC [ ... final_byte
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && !isFinalByte(s[j]) {
					j++
				}
				if j < len(s) {
					j++ // skip final byte
				}
				i = j
				continue
			}
			// Skip OSC sequences: ESC ] ... ST
			if i+1 < len(s) && s[i+1] == ']' {
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 { // BEL terminator
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST terminator
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}
			// Skip other two-byte escapes
			i += 2
			continue
		}
		if unicode.IsPrint(rune(s[i])) || s[i] == '\t' {
			buf.WriteByte(s[i])
		}
		i++
	}
	return buf.String()
}

// isFinalByte returns true if b is a CSI final byte (0x40-0x7e).
func isFinalByte(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}
