package copymode

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
