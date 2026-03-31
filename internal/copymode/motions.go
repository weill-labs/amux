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

type wordClassifier func(rune) wordClass

type wordMotionKind uint8

const (
	wordMotionNextStart wordMotionKind = iota
	wordMotionPrevStart
	wordMotionNextEnd
)

type wordMotionSpec struct {
	classify  wordClassifier
	kind      wordMotionKind
	stickyTop bool
	stickyEnd bool
}

var wordMotionSpecs = map[byte]wordMotionSpec{
	'W': {classify: classifyWordRune, kind: wordMotionNextStart},
	'B': {classify: classifyWordRune, kind: wordMotionPrevStart, stickyTop: true},
	'E': {classify: classifyWordRune, kind: wordMotionNextEnd, stickyEnd: true},
	'w': {classify: classifyViWordRune, kind: wordMotionNextStart},
	'b': {classify: classifyViWordRune, kind: wordMotionPrevStart},
	'e': {classify: classifyViWordRune, kind: wordMotionNextEnd},
}

type cursorState struct {
	cx int
	cy int
	oy int
}

type motionCursor struct {
	cm   *CopyMode
	line []rune
	pos  int
}

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

func classifyWordRune(r rune) wordClass {
	if unicode.IsSpace(r) {
		return wordClassWhitespace
	}
	return wordClassWord
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

func (cm *CopyMode) runWordMotion(key byte) Action {
	spec, ok := wordMotionSpecs[key]
	if !ok {
		return ActionNone
	}
	switch spec.kind {
	case wordMotionNextStart:
		return cm.nextWordStart(spec.classify)
	case wordMotionPrevStart:
		return cm.previousWordStart(spec.classify, spec.stickyTop)
	case wordMotionNextEnd:
		return cm.nextWordEnd(spec.classify, spec.stickyEnd)
	default:
		return ActionNone
	}
}

func (cm *CopyMode) saveCursor() cursorState {
	return cursorState{cx: cm.cx, cy: cm.cy, oy: cm.oy}
}

func (cm *CopyMode) restoreCursor(state cursorState) {
	cm.cx, cm.cy, cm.oy = state.cx, state.cy, state.oy
}

func (cm *CopyMode) finishWordMotion(col int) Action {
	cm.cx = col
	cm.updateSelection()
	return ActionRedraw
}

func (cm *CopyMode) restoreWordMotion(saved cursorState, col int) Action {
	cm.restoreCursor(saved)
	return cm.finishWordMotion(col)
}

func (cm *CopyMode) failWordMotion(saved cursorState) Action {
	cm.restoreCursor(saved)
	return ActionNone
}

func newMotionCursor(cm *CopyMode) motionCursor {
	line := cm.cursorLineRunes()
	return motionCursor{
		cm:   cm,
		line: line,
		pos:  min(cm.cx, len(line)),
	}
}

func (mc *motionCursor) resetLine(pos int) {
	mc.line = mc.cm.cursorLineRunes()
	mc.pos = pos
}

func (mc *motionCursor) moveDown(pos int) bool {
	if !mc.cm.moveDown() {
		return false
	}
	mc.resetLine(pos)
	return true
}

func (mc *motionCursor) moveUpToEnd() bool {
	if !mc.cm.moveUp() {
		return false
	}
	mc.line = mc.cm.cursorLineRunes()
	mc.pos = len(mc.line) - 1
	return true
}

func (mc *motionCursor) stepForward() bool {
	if mc.pos < len(mc.line)-1 {
		mc.pos++
		return true
	}
	return mc.moveDown(0)
}

func (mc *motionCursor) skipForwardClass(classify wordClassifier, class wordClass) {
	for mc.pos < len(mc.line) && classify(mc.line[mc.pos]) == class {
		mc.pos++
	}
}

func (mc *motionCursor) seekForwardNonWhitespace(classify wordClassifier) bool {
	for {
		for mc.pos < len(mc.line) && classify(mc.line[mc.pos]) == wordClassWhitespace {
			mc.pos++
		}
		if mc.pos < len(mc.line) {
			return true
		}
		if !mc.moveDown(0) {
			return false
		}
	}
}

func (mc *motionCursor) seekBackwardNonWhitespace(classify wordClassifier) bool {
	for {
		for mc.pos >= 0 && (mc.pos >= len(mc.line) || classify(mc.line[mc.pos]) == wordClassWhitespace) {
			mc.pos--
		}
		if mc.pos >= 0 {
			return true
		}
		if !mc.moveUpToEnd() {
			return false
		}
	}
}

func (cm *CopyMode) nextWordStart(classify wordClassifier) Action {
	saved := cm.saveCursor()
	cursor := newMotionCursor(cm)

	if cursor.pos < len(cursor.line) {
		class := classify(cursor.line[cursor.pos])
		if class != wordClassWhitespace {
			cursor.skipForwardClass(classify, class)
		}
	}
	if !cursor.seekForwardNonWhitespace(classify) {
		return cm.failWordMotion(saved)
	}
	return cm.finishWordMotion(cursor.pos)
}

func (cm *CopyMode) previousWordStart(classify wordClassifier, stickyTop bool) Action {
	saved := cm.saveCursor()
	cursor := newMotionCursor(cm)
	cursor.pos--
	if !cursor.seekBackwardNonWhitespace(classify) {
		if stickyTop {
			return cm.restoreWordMotion(saved, 0)
		}
		return cm.failWordMotion(saved)
	}
	class := classify(cursor.line[cursor.pos])
	for cursor.pos > 0 && classify(cursor.line[cursor.pos-1]) == class {
		cursor.pos--
	}
	return cm.finishWordMotion(max(0, cursor.pos))
}

func (cm *CopyMode) nextWordEnd(classify wordClassifier, stickyEnd bool) Action {
	saved := cm.saveCursor()
	cursor := newMotionCursor(cm)
	if cursor.pos < len(cursor.line) && classify(cursor.line[cursor.pos]) != wordClassWhitespace {
		if !cursor.stepForward() {
			return cm.failWordMotion(saved)
		}
	}
	if !cursor.seekForwardNonWhitespace(classify) {
		if stickyEnd {
			return cm.restoreWordMotion(saved, max(0, len(cursor.line)-1))
		}
		return cm.failWordMotion(saved)
	}
	class := classify(cursor.line[cursor.pos])
	for cursor.pos < len(cursor.line)-1 && classify(cursor.line[cursor.pos+1]) == class {
		cursor.pos++
	}
	return cm.finishWordMotion(clamp(cursor.pos, 0, cm.width-1))
}
