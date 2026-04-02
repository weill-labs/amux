package vt

import (
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const maxKittyKeyboardStack = 16

type kittyKeyboardMode struct {
	stack []int
}

func (e *Emulator) activeKittyKeyboard() *kittyKeyboardMode {
	if e.scr == &e.scrs[1] {
		return &e.kittyKeyboard[1]
	}
	return &e.kittyKeyboard[0]
}

func (e *Emulator) kittyKeyboardFlags() int {
	state := e.activeKittyKeyboard()
	if len(state.stack) == 0 {
		return 0
	}
	return state.stack[len(state.stack)-1]
}

func (e *Emulator) pushKittyKeyboard(flags int) {
	state := e.activeKittyKeyboard()
	flags &= ansi.KittyAllFlags
	if len(state.stack) >= maxKittyKeyboardStack {
		copy(state.stack, state.stack[1:])
		state.stack = state.stack[:maxKittyKeyboardStack-1]
	}
	state.stack = append(state.stack, flags)
}

func (e *Emulator) popKittyKeyboard(count int) {
	if count < 1 {
		count = 1
	}

	state := e.activeKittyKeyboard()
	if count >= len(state.stack) {
		state.stack = state.stack[:0]
		return
	}

	state.stack = state.stack[:len(state.stack)-count]
}

func (e *Emulator) reportKittyKeyboard() {
	_, _ = io.WriteString(e.pw, "\x1b[?"+strconv.Itoa(e.kittyKeyboardFlags())+"u")
}

func (e *Emulator) resetKittyKeyboard() {
	for i := range e.kittyKeyboard {
		e.kittyKeyboard[i].stack = nil
	}
}

func (e *Emulator) kittyKeySequence(ev uv.KeyEvent) (string, bool) {
	flags := e.kittyKeyboardFlags()
	if flags == 0 {
		return "", false
	}

	var (
		key       uv.Key
		eventType = 1
	)

	switch k := ev.(type) {
	case uv.KeyPressEvent:
		key = k.Key()
		if k.IsRepeat {
			if flags&ansi.KittyReportEventTypes == 0 {
				eventType = 1
			} else {
				eventType = 2
			}
		}
	case uv.KeyReleaseEvent:
		if flags&ansi.KittyReportEventTypes == 0 {
			return "", false
		}
		key = k.Key()
		eventType = 3
	default:
		return "", false
	}

	if !shouldEncodeKittyKey(key, flags, eventType) {
		return "", false
	}

	codeField, ok := kittyKeyCodeField(key, flags)
	if !ok {
		return "", false
	}

	var seq strings.Builder
	seq.WriteString("\x1b[")
	seq.WriteString(codeField)

	modifiers := kittyModifierBits(key.Mod)
	if modifiers != 0 || eventType != 1 {
		seq.WriteByte(';')
		seq.WriteString(strconv.Itoa(modifiers + 1))
		if eventType != 1 {
			seq.WriteByte(':')
			seq.WriteString(strconv.Itoa(eventType))
		}
	}

	if flags&ansi.KittyReportAllKeysAsEscapeCodes != 0 && flags&ansi.KittyReportAssociatedKeys != 0 {
		if textField, ok := kittyAssociatedTextField(key.Text); ok {
			seq.WriteByte(';')
			seq.WriteString(textField)
		}
	}

	seq.WriteByte('u')
	return seq.String(), true
}

func shouldEncodeKittyKey(key uv.Key, flags int, eventType int) bool {
	if flags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
		return true
	}

	if eventType != 1 && keyProducesText(key) {
		return false
	}

	mod := key.Mod &^ (uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock)
	if kittyAlwaysLegacyKey(key.Code) {
		return mod != 0 && !(key.Code == uv.KeyTab && mod == uv.ModShift)
	}

	if _, ok := kittySpecialKeyCode(key.Code); ok {
		return true
	}

	if keyProducesText(key) {
		return mod != 0 && mod != uv.ModShift
	}

	return false
}

func keyProducesText(key uv.Key) bool {
	return key.Text != "" || (unicode.IsPrint(key.Code) && key.Code != uv.KeySpace)
}

func kittyAlwaysLegacyKey(code rune) bool {
	switch code {
	case uv.KeyEnter, uv.KeyTab, uv.KeyBackspace:
		return true
	default:
		return false
	}
}

func kittyKeyCodeField(key uv.Key, flags int) (string, bool) {
	code, ok := kittyKeyCode(key)
	if !ok {
		return "", false
	}

	var field strings.Builder
	field.WriteString(strconv.Itoa(code))

	if flags&ansi.KittyReportAlternateKeys == 0 {
		return field.String(), true
	}

	shifted := key.ShiftedCode
	if shifted == 0 && key.Mod.Contains(uv.ModShift) && unicode.IsPrint(key.Code) {
		shifted = unicode.ToUpper(key.Code)
	}

	base := key.BaseCode
	if shifted == 0 && base == 0 {
		return field.String(), true
	}

	field.WriteByte(':')
	if shifted != 0 && unicode.IsPrint(shifted) {
		field.WriteString(strconv.Itoa(int(shifted)))
	}

	if base == 0 || !unicode.IsPrint(base) {
		return field.String(), true
	}

	field.WriteByte(':')
	field.WriteString(strconv.Itoa(int(base)))
	return field.String(), true
}

func kittyKeyCode(key uv.Key) (int, bool) {
	if key.BaseCode != 0 && unicode.IsPrint(key.BaseCode) {
		return int(unicode.ToLower(key.BaseCode)), true
	}
	if unicode.IsPrint(key.Code) {
		return int(unicode.ToLower(key.Code)), true
	}

	return kittySpecialKeyCode(key.Code)
}

func kittySpecialKeyCode(code rune) (int, bool) {
	switch code {
	case uv.KeyEscape:
		return int(ansi.ESC), true
	case uv.KeyEnter:
		return int(ansi.CR), true
	case uv.KeyTab:
		return int(ansi.HT), true
	case uv.KeyBackspace:
		return int(ansi.DEL), true
	case uv.KeyInsert:
		return 57348, true
	case uv.KeyDelete:
		return 57349, true
	case uv.KeyLeft:
		return 57350, true
	case uv.KeyRight:
		return 57351, true
	case uv.KeyUp:
		return 57352, true
	case uv.KeyDown:
		return 57353, true
	case uv.KeyPgUp:
		return 57354, true
	case uv.KeyPgDown:
		return 57355, true
	case uv.KeyHome:
		return 57356, true
	case uv.KeyEnd:
		return 57357, true
	case uv.KeyF1:
		return 57364, true
	case uv.KeyF2:
		return 57365, true
	case uv.KeyF3:
		return 57366, true
	case uv.KeyF4:
		return 57367, true
	case uv.KeyF5:
		return 57368, true
	case uv.KeyF6:
		return 57369, true
	case uv.KeyF7:
		return 57370, true
	case uv.KeyF8:
		return 57371, true
	case uv.KeyF9:
		return 57372, true
	case uv.KeyF10:
		return 57373, true
	case uv.KeyF11:
		return 57374, true
	case uv.KeyF12:
		return 57375, true
	case uv.KeyKp0:
		return 57399, true
	case uv.KeyKp1:
		return 57400, true
	case uv.KeyKp2:
		return 57401, true
	case uv.KeyKp3:
		return 57402, true
	case uv.KeyKp4:
		return 57403, true
	case uv.KeyKp5:
		return 57404, true
	case uv.KeyKp6:
		return 57405, true
	case uv.KeyKp7:
		return 57406, true
	case uv.KeyKp8:
		return 57407, true
	case uv.KeyKp9:
		return 57408, true
	case uv.KeyKpDecimal:
		return 57409, true
	case uv.KeyKpDivide:
		return 57410, true
	case uv.KeyKpMultiply:
		return 57411, true
	case uv.KeyKpMinus:
		return 57412, true
	case uv.KeyKpPlus:
		return 57413, true
	case uv.KeyKpEnter:
		return 57414, true
	case uv.KeyKpEqual:
		return 57415, true
	case uv.KeyKpComma:
		return 57416, true
	default:
		return 0, false
	}
}

func kittyModifierBits(mod uv.KeyMod) int {
	bits := 0
	if mod&uv.ModShift != 0 {
		bits |= 1
	}
	if mod&uv.ModAlt != 0 {
		bits |= 1 << 1
	}
	if mod&uv.ModCtrl != 0 {
		bits |= 1 << 2
	}
	if mod&uv.ModSuper != 0 {
		bits |= 1 << 3
	}
	if mod&uv.ModHyper != 0 {
		bits |= 1 << 4
	}
	if mod&uv.ModMeta != 0 {
		bits |= 1 << 5
	}
	if mod&uv.ModCapsLock != 0 {
		bits |= 1 << 6
	}
	if mod&uv.ModNumLock != 0 {
		bits |= 1 << 7
	}
	return bits
}

func kittyAssociatedTextField(text string) (string, bool) {
	if text == "" {
		return "", false
	}

	var field strings.Builder
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError || size == 0 || unicode.IsControl(r) {
			return "", false
		}
		if field.Len() > 0 {
			field.WriteByte(':')
		}
		field.WriteString(strconv.Itoa(int(r)))
		text = text[size:]
	}

	return field.String(), field.Len() > 0
}
