package copymode

import "strings"

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
