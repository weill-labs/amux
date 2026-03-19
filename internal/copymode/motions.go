package copymode

import (
	"strings"
	"unicode"
)

const tmuxWordSeparators = "!\"#$%&'()*+,-./:;<=>?@[\\]^`{|}~"

type wordClass uint8

const (
	wordClassWhitespace wordClass = iota
	wordClassWord
	wordClassSeparator
)

// moveDown moves the cursor down one row, scrolling if at the viewport bottom.
// Returns false if already at the absolute bottom (oy=0, cy=height-1).
func (cm *CopyMode) moveDown() bool {
	if cm.cy < cm.height-1 {
		cm.cy++
		return true
	}
	if cm.oy > 0 {
		cm.oy--
		return true
	}
	return false
}

// moveUp moves the cursor up one row, scrolling if at the viewport top.
// Returns false if already at the absolute top (oy=maxOY, cy=0).
func (cm *CopyMode) moveUp() bool {
	if cm.cy > 0 {
		cm.cy--
		return true
	}
	if cm.oy < cm.maxOY() {
		cm.oy++
		return true
	}
	return false
}

// lineEndCol returns the column of the last non-space character on the
// cursor's current line. Returns 0 for empty lines.
func (cm *CopyMode) lineEndCol() int {
	line := []rune(cm.lineText(cm.cursorAbsLine()))
	end := len(line) - 1
	for end >= 0 && unicode.IsSpace(line[end]) {
		end--
	}
	if end < 0 {
		return 0
	}
	return end
}

// firstNonBlankCol returns the column of the first non-whitespace character
// on the cursor's current line. Returns 0 if the line is blank.
func (cm *CopyMode) firstNonBlankCol() int {
	line := []rune(cm.lineText(cm.cursorAbsLine()))
	for i, r := range line {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return 0
}

// cursorLineRunes returns the runes of the current cursor line.
func (cm *CopyMode) cursorLineRunes() []rune {
	return []rune(cm.lineText(cm.cursorAbsLine()))
}

func classifyViWordRune(r rune) wordClass {
	switch {
	case unicode.IsSpace(r):
		return wordClassWhitespace
	case r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r):
		return wordClassWord
	case r < unicode.MaxASCII && strings.ContainsRune(tmuxWordSeparators, r):
		return wordClassSeparator
	default:
		return wordClassWord
	}
}

// moveWordForward implements the W (WORD forward) motion.
// A WORD is a sequence of non-whitespace characters separated by whitespace.
// Moves to the first character of the next WORD, wrapping across lines.
func (cm *CopyMode) moveWordForward() Action {
	savedCY, savedOY := cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := cm.cx

	// Phase 1: skip past non-whitespace (finish current word).
	for pos < len(line) && !unicode.IsSpace(line[pos]) {
		pos++
	}

	// Phase 2: skip whitespace (cross the gap).
	for {
		for pos < len(line) && unicode.IsSpace(line[pos]) {
			pos++
		}
		if pos < len(line) {
			// Found start of next WORD on this line.
			cm.cx = pos
			cm.updateSelection()
			return ActionRedraw
		}
		// Ran off the end — wrap to next line.
		if !cm.moveDown() {
			cm.cy, cm.oy = savedCY, savedOY
			return ActionNone
		}
		line = cm.cursorLineRunes()
		pos = 0
	}
}

// moveWordBackward implements the B (WORD backward) motion.
// Moves to the first character of the previous WORD, wrapping across lines.
func (cm *CopyMode) moveWordBackward() Action {
	savedCY, savedOY := cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := cm.cx

	// Step back one position to start searching behind the cursor.
	pos--

	// If we're before the start of the line, wrap to previous line.
	for pos < 0 {
		if !cm.moveUp() {
			cm.cy, cm.oy = savedCY, savedOY
			cm.cx = 0
			cm.updateSelection()
			return ActionRedraw
		}
		line = cm.cursorLineRunes()
		pos = len(line) - 1
	}

	// Skip whitespace backward, wrapping lines as needed.
	for pos >= 0 && (pos >= len(line) || unicode.IsSpace(line[pos])) {
		pos--
		if pos < 0 {
			if !cm.moveUp() {
				cm.cy, cm.oy = savedCY, savedOY
				cm.cx = 0
				cm.updateSelection()
				return ActionRedraw
			}
			line = cm.cursorLineRunes()
			pos = len(line) - 1
		}
	}

	// Skip non-whitespace backward to find the start of the WORD.
	for pos > 0 && !unicode.IsSpace(line[pos-1]) {
		pos--
	}

	cm.cx = max(0, pos)
	cm.updateSelection()
	return ActionRedraw
}

// moveWordEnd implements the E (end of WORD) motion.
// Moves to the last character of the next WORD, wrapping across lines.
func (cm *CopyMode) moveWordEnd() Action {
	savedCY, savedOY := cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := cm.cx
	lineLen := len(line)

	// Step 1: advance past current position.
	if pos < lineLen-1 {
		pos++
	} else {
		// At end of line — wrap to next line.
		if !cm.moveDown() {
			return ActionNone
		}
		line = cm.cursorLineRunes()
		lineLen = len(line)
		pos = 0
	}

	// Step 2: skip whitespace.
	for pos < lineLen && unicode.IsSpace(line[pos]) {
		pos++
		if pos >= lineLen {
			if !cm.moveDown() {
				cm.cy, cm.oy = savedCY, savedOY
				cm.cx = max(0, lineLen-1)
				cm.updateSelection()
				return ActionRedraw
			}
			line = cm.cursorLineRunes()
			lineLen = len(line)
			pos = 0
		}
	}

	// Step 3: advance through non-whitespace to find end of WORD.
	for pos < lineLen-1 && !unicode.IsSpace(line[pos+1]) {
		pos++
	}

	cm.cx = clamp(pos, 0, cm.width-1)
	cm.updateSelection()
	return ActionRedraw
}

// moveViWordForward implements tmux's vi-mode next-word motion (w).
func (cm *CopyMode) moveViWordForward() Action {
	savedCX, savedCY, savedOY := cm.cx, cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := min(cm.cx, len(line))

	if pos < len(line) {
		class := classifyViWordRune(line[pos])
		if class != wordClassWhitespace {
			for pos < len(line) && classifyViWordRune(line[pos]) == class {
				pos++
			}
		}
	}

	for {
		for pos < len(line) && classifyViWordRune(line[pos]) == wordClassWhitespace {
			pos++
		}
		if pos < len(line) {
			cm.cx = pos
			cm.updateSelection()
			return ActionRedraw
		}
		if !cm.moveDown() {
			cm.cx, cm.cy, cm.oy = savedCX, savedCY, savedOY
			return ActionNone
		}
		line = cm.cursorLineRunes()
		pos = 0
	}
}

// moveViWordBackward implements tmux's vi-mode previous-word motion (b).
func (cm *CopyMode) moveViWordBackward() Action {
	savedCX, savedCY, savedOY := cm.cx, cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := min(cm.cx, len(line))

	pos--
	for {
		for pos >= 0 {
			class := classifyViWordRune(line[pos])
			if class == wordClassWhitespace {
				pos--
				continue
			}
			for pos > 0 && classifyViWordRune(line[pos-1]) == class {
				pos--
			}
			cm.cx = pos
			cm.updateSelection()
			return ActionRedraw
		}
		if !cm.moveUp() {
			cm.cx, cm.cy, cm.oy = savedCX, savedCY, savedOY
			return ActionNone
		}
		line = cm.cursorLineRunes()
		pos = len(line) - 1
	}
}

// moveViWordEnd implements tmux's vi-mode next-word-end motion (e).
func (cm *CopyMode) moveViWordEnd() Action {
	savedCX, savedCY, savedOY := cm.cx, cm.cy, cm.oy
	line := cm.cursorLineRunes()
	pos := min(cm.cx, len(line))

	if pos < len(line) && classifyViWordRune(line[pos]) != wordClassWhitespace {
		pos++
		if pos >= len(line) {
			if !cm.moveDown() {
				cm.cx, cm.cy, cm.oy = savedCX, savedCY, savedOY
				return ActionNone
			}
			line = cm.cursorLineRunes()
			pos = 0
		}
	}

	for {
		for pos < len(line) && classifyViWordRune(line[pos]) == wordClassWhitespace {
			pos++
		}
		if pos < len(line) {
			break
		}
		if !cm.moveDown() {
			cm.cx, cm.cy, cm.oy = savedCX, savedCY, savedOY
			return ActionNone
		}
		line = cm.cursorLineRunes()
		pos = 0
	}

	class := classifyViWordRune(line[pos])
	for pos < len(line)-1 && classifyViWordRune(line[pos+1]) == class {
		pos++
	}

	cm.cx = clamp(pos, 0, cm.width-1)
	cm.updateSelection()
	return ActionRedraw
}

// executeCharSearch performs an f/F/t/T character search on the current line.
// Returns ActionNone if the target is not found (cursor stays, last search unchanged).
func (cm *CopyMode) executeCharSearch(cmd, target byte, count int) Action {
	if count <= 0 {
		count = 1
	}
	savedCX := cm.cx
	for i := 0; i < count; i++ {
		col, ok := cm.findCharOnLine(cmd, rune(target))
		if !ok {
			cm.cx = savedCX
			return ActionNone
		}
		cm.cx = col
	}
	cm.lastCharSearch = cmd
	cm.lastCharTarget = target
	cm.updateSelection()
	return ActionRedraw
}

// findCharOnLine searches for rune r on the current line using the given
// command ('f'/'F'/'t'/'T'). Returns the destination column and true if
// found, or (0, false) if not found or the "till" offset would not move.
func (cm *CopyMode) findCharOnLine(cmd byte, r rune) (int, bool) {
	line := cm.cursorLineRunes()

	switch cmd {
	case 'f': // find forward — land ON target
		for i := cm.cx + 1; i < len(line); i++ {
			if line[i] == r {
				return i, true
			}
		}
	case 'F': // find backward — land ON target
		for i := cm.cx - 1; i >= 0; i-- {
			if line[i] == r {
				return i, true
			}
		}
	case 't': // till forward — land one BEFORE target
		for i := cm.cx + 1; i < len(line); i++ {
			if line[i] == r {
				if i-1 > cm.cx {
					return i - 1, true
				}
				return 0, false
			}
		}
	case 'T': // till backward — land one AFTER target
		for i := cm.cx - 1; i >= 0; i-- {
			if line[i] == r {
				if i+1 < cm.cx {
					return i + 1, true
				}
				return 0, false
			}
		}
	}
	return 0, false
}

// repeatCharSearch repeats the last f/F/t/T search. If reverse is true,
// the direction is flipped (f↔F, t↔T).
func (cm *CopyMode) repeatCharSearch(reverse bool) Action {
	if cm.lastCharSearch == 0 {
		return ActionNone
	}
	cmd := cm.lastCharSearch
	if reverse {
		switch cmd {
		case 'f':
			cmd = 'F'
		case 'F':
			cmd = 'f'
		case 't':
			cmd = 'T'
		case 'T':
			cmd = 't'
		}
	}
	// Save/restore lastCharSearch so executeCharSearch doesn't overwrite
	// the original direction when repeating in reverse.
	savedCmd := cm.lastCharSearch
	savedTarget := cm.lastCharTarget
	result := cm.executeCharSearch(cmd, cm.lastCharTarget, 1)
	cm.lastCharSearch = savedCmd
	cm.lastCharTarget = savedTarget
	return result
}

func (cm *CopyMode) previousParagraph() Action {
	y := cm.cursorAbsLine()
	origY := y
	for y > 0 && strings.TrimSpace(cm.lineText(y)) == "" {
		y--
	}
	for y > 0 && strings.TrimSpace(cm.lineText(y)) != "" {
		y--
	}
	if y == origY {
		return ActionNone
	}
	cm.scrollToAbsolute(y, 0)
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) nextParagraph() Action {
	y := cm.cursorAbsLine()
	origY := y
	maxY := cm.TotalLines() - 1
	for y < maxY && strings.TrimSpace(cm.lineText(y)) == "" {
		y++
	}
	for y < maxY && strings.TrimSpace(cm.lineText(y)) != "" {
		y++
	}
	if y == origY {
		return ActionNone
	}
	cm.scrollToAbsolute(y, 0)
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) matchingBracket() Action {
	type pair struct {
		open  rune
		close rune
	}
	pairs := []pair{{'(', ')'}, {'[', ']'}, {'{', '}'}}

	y, x, r, ok := cm.findBracketCandidate()
	if !ok {
		return ActionNone
	}
	for _, p := range pairs {
		switch r {
		case p.open:
			if my, mx, found := cm.findMatchingForward(y, x, p.open, p.close); found {
				cm.scrollToAbsolute(my, mx)
				cm.updateSelection()
				return ActionRedraw
			}
			return ActionNone
		case p.close:
			if my, mx, found := cm.findMatchingBackward(y, x, p.open, p.close); found {
				cm.scrollToAbsolute(my, mx)
				cm.updateSelection()
				return ActionRedraw
			}
			return ActionNone
		}
	}
	return ActionNone
}

func (cm *CopyMode) findBracketCandidate() (int, int, rune, bool) {
	y := cm.cursorAbsLine()
	line := []rune(cm.lineText(y))
	if cm.cx < len(line) {
		if r := line[cm.cx]; strings.ContainsRune("()[]{}", r) {
			return y, cm.cx, r, true
		}
	}
	for yy := y; yy < cm.TotalLines(); yy++ {
		line = []rune(cm.lineText(yy))
		startX := 0
		if yy == y {
			startX = min(cm.cx+1, len(line))
		}
		for xx := startX; xx < len(line); xx++ {
			if r := line[xx]; strings.ContainsRune("()[]{}", r) {
				return yy, xx, r, true
			}
		}
	}
	return 0, 0, 0, false
}

func (cm *CopyMode) findMatchingForward(y, x int, open, close rune) (int, int, bool) {
	depth := 1
	for yy := y; yy < cm.TotalLines(); yy++ {
		line := []rune(cm.lineText(yy))
		startX := 0
		if yy == y {
			startX = x + 1
		}
		for xx := startX; xx < len(line); xx++ {
			switch line[xx] {
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return yy, xx, true
				}
			}
		}
	}
	return 0, 0, false
}

func (cm *CopyMode) findMatchingBackward(y, x int, open, close rune) (int, int, bool) {
	depth := 1
	for yy := y; yy >= 0; yy-- {
		line := []rune(cm.lineText(yy))
		startX := len(line) - 1
		if yy == y {
			startX = min(x-1, len(line)-1)
		}
		for xx := startX; xx >= 0; xx-- {
			switch line[xx] {
			case close:
				depth++
			case open:
				depth--
				if depth == 0 {
					return yy, xx, true
				}
			}
		}
	}
	return 0, 0, false
}
