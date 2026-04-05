package copymode

import "strings"

func (cm *CopyMode) handleMotionKey(key, count int) (Action, bool) {
	if action, handled := cm.handleLineMotionKey(key, count); handled {
		return action, true
	}
	if action, handled := cm.handleWordMotionKey(key, count); handled {
		return action, true
	}
	if action, handled := cm.handleParagraphMotionKey(key, count); handled {
		return action, true
	}
	if action, handled := cm.handleSearchJumpMotionKey(key, count); handled {
		return action, true
	}
	return ActionNone, false
}

func (cm *CopyMode) handleLineMotionKey(key, count int) (Action, bool) {
	switch key {
	case 'j', keyDown:
		return cm.repeatMotion(count, cm.moveDown), true
	case 'k', keyUp:
		return cm.repeatMotion(count, cm.moveUp), true
	case 'h', keyLeft, 0x08, 0x7f:
		return cm.repeatAction(count, func() Action {
			if cm.cx == 0 {
				return ActionNone
			}
			cm.cx--
			cm.updateSelection()
			return ActionRedraw
		}), true
	case 'l', keyRight:
		return cm.repeatAction(count, func() Action {
			if cm.cx >= cm.width-1 {
				return ActionNone
			}
			cm.cx++
			cm.updateSelection()
			return ActionRedraw
		}), true
	case 0x04: // Ctrl-d — half page down
		return cm.scrollBy(-(cm.height/2)*count, false), true
	case 0x15: // Ctrl-u — half page up
		return cm.scrollBy((cm.height/2)*count, false), true
	case 'g': // scroll to top
		cm.oy = cm.maxOY()
		cm.cy = 0
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw, true
	case 'G': // scroll to bottom
		cm.oy = 0
		cm.cy = cm.height - 1
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw, true
	case '0', keyHome:
		cm.cx = 0
		cm.updateSelection()
		return ActionRedraw, true
	case '$', keyEnd:
		cm.cx = cm.lineEndCol()
		cm.updateSelection()
		return ActionRedraw, true
	case '^': // first non-blank character
		cm.cx = cm.firstNonBlankCol()
		cm.updateSelection()
		return ActionRedraw, true
	case 'H':
		cm.cx = 0
		cm.cy = 0
		cm.updateSelection()
		return ActionRedraw, true
	case 'L':
		cm.cx = 0
		cm.cy = cm.height - 1
		cm.updateSelection()
		return ActionRedraw, true
	case 'M':
		cm.cx = 0
		cm.cy = (cm.height - 1) / 2
		cm.updateSelection()
		return ActionRedraw, true
	case 'z':
		return cm.scrollCursorToRow((cm.height - 1) / 2), true
	case 'J', 0x05, keyCtrlDown:
		return cm.WheelScrollDown(count), true
	case 'K', 0x19, keyCtrlUp:
		return cm.WheelScrollUp(count), true
	case 0x06, keyPageDown:
		return cm.scrollBy(-cm.height*count, false), true
	case 0x02, keyPageUp:
		return cm.scrollBy(cm.height*count, false), true
	}
	return ActionNone, false
}

func (cm *CopyMode) handleWordMotionKey(key, count int) (Action, bool) {
	switch key {
	case 'W', 'B', 'E', 'w', 'b', 'e':
		return cm.repeatAction(count, func() Action { return cm.runWordMotion(byte(key)) }), true
	}
	return ActionNone, false
}

func (cm *CopyMode) handleParagraphMotionKey(key, count int) (Action, bool) {
	switch key {
	case '{':
		return cm.repeatAction(count, cm.previousParagraph), true
	case '}':
		return cm.repeatAction(count, cm.nextParagraph), true
	}
	return ActionNone, false
}

func (cm *CopyMode) handleSearchJumpMotionKey(key, count int) (Action, bool) {
	switch key {
	case '/':
		return cm.startPrompt(promptSearchForward), true
	case '?':
		return cm.startPrompt(promptSearchBackward), true
	case ':':
		return cm.startPrompt(promptGotoLine), true
	case 'n':
		cm.searchAgain(false)
		return ActionRedraw, true
	case 'N':
		cm.searchAgain(true)
		return ActionRedraw, true
	case '*':
		return cm.searchWordUnderCursor(true), true
	case '#':
		return cm.searchWordUnderCursor(false), true
	case 'r':
		if cm.searchQuery != "" {
			cm.runSearch(cm.lastSearchForward)
		}
		return ActionRedraw, true
	case 'X':
		cm.markSet = true
		cm.markX = cm.cx
		cm.markY = cm.cursorAbsLine()
		return ActionRedraw, true
	case 'f', 'F', 't', 'T':
		cm.pendingCharSearch = byte(key)
		cm.pendingCharSearchCount = count
		return ActionNone, true
	case ';':
		return cm.repeatAction(count, func() Action { return cm.repeatCharSearch(false) }), true
	case ',':
		return cm.repeatAction(count, func() Action { return cm.repeatCharSearch(true) }), true
	case '%':
		return cm.repeatAction(count, cm.matchingBracket), true
	case keyAltX:
		if !cm.markSet {
			return ActionNone, true
		}
		return cm.jumpToMark(), true
	}
	return ActionNone, false
}

func (cm *CopyMode) startPrompt(mode promptMode) Action {
	cm.prompt = mode
	cm.promptBuf = ""
	cm.promptCursor = 0
	return ActionRedraw
}

func (cm *CopyMode) searchWordUnderCursor(forward bool) Action {
	query := cm.wordUnderCursor()
	if query == "" {
		return ActionNone
	}
	cm.searchQuery = query
	cm.lastSearchForward = forward
	cm.runSearch(forward)
	return ActionRedraw
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
	center := cm.height / 2
	cm.oy = clamp(cm.TotalLines()-cm.height-m.LineIdx+center, 0, cm.maxOY())
	firstVisible := cm.TotalLines() - cm.height - cm.oy
	cm.cy = clamp(m.LineIdx-firstVisible, 0, cm.height-1)
	cm.cx = m.Col
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
