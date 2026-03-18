package copymode

import "unicode"

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
	for end >= 0 && line[end] == ' ' {
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

// moveWordForward implements the W (WORD forward) motion.
// A WORD is a sequence of non-whitespace characters separated by whitespace.
// Moves to the first character of the next WORD, wrapping across lines.
func (cm *CopyMode) moveWordForward() Action {
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
			return ActionNone
		}
		line = cm.cursorLineRunes()
		pos = 0
	}
}

// moveWordBackward implements the B (WORD backward) motion.
// Moves to the first character of the previous WORD, wrapping across lines.
func (cm *CopyMode) moveWordBackward() Action {
	line := cm.cursorLineRunes()
	pos := cm.cx

	// Step back one position to start searching behind the cursor.
	pos--

	// If we're before the start of the line, wrap to previous line.
	for pos < 0 {
		if !cm.moveUp() {
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

// executeCharSearch performs an f/F/t/T character search on the current line.
// Returns ActionNone if the target is not found (cursor stays, last search unchanged).
func (cm *CopyMode) executeCharSearch(cmd, target byte) Action {
	line := cm.cursorLineRunes()
	r := rune(target)

	switch cmd {
	case 'f': // find forward — land ON target
		for i := cm.cx + 1; i < len(line); i++ {
			if line[i] == r {
				cm.cx = i
				cm.lastCharSearch = cmd
				cm.lastCharTarget = target
				cm.updateSelection()
				return ActionRedraw
			}
		}
	case 'F': // find backward — land ON target
		for i := cm.cx - 1; i >= 0; i-- {
			if line[i] == r {
				cm.cx = i
				cm.lastCharSearch = cmd
				cm.lastCharTarget = target
				cm.updateSelection()
				return ActionRedraw
			}
		}
	case 't': // till forward — land one BEFORE target
		for i := cm.cx + 1; i < len(line); i++ {
			if line[i] == r {
				if i-1 > cm.cx {
					cm.cx = i - 1
					cm.lastCharSearch = cmd
					cm.lastCharTarget = target
					cm.updateSelection()
					return ActionRedraw
				}
				return ActionNone
			}
		}
	case 'T': // till backward — land one AFTER target
		for i := cm.cx - 1; i >= 0; i-- {
			if line[i] == r {
				if i+1 < cm.cx {
					cm.cx = i + 1
					cm.lastCharSearch = cmd
					cm.lastCharTarget = target
					cm.updateSelection()
					return ActionRedraw
				}
				return ActionNone
			}
		}
	}
	return ActionNone
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
	result := cm.executeCharSearch(cmd, cm.lastCharTarget)
	cm.lastCharSearch = savedCmd
	cm.lastCharTarget = savedTarget
	return result
}
