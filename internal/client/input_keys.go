package client

import (
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/proto"
)

type decodedInputEvent struct {
	raw   []byte
	event uv.Event
}

func splitTrailingIncompleteUTF8(raw []byte) (complete []byte, pending []byte) {
	if len(raw) == 0 {
		return nil, nil
	}

	start := len(raw) - 1
	for start >= 0 && len(raw)-start < utf8.UTFMax && !utf8.RuneStart(raw[start]) {
		start--
	}
	if start < 0 || !utf8.RuneStart(raw[start]) {
		return raw, nil
	}
	if utf8.FullRune(raw[start:]) {
		return raw, nil
	}

	return raw[:start], raw[start:]
}

func decodeInputEvents(raw []byte) []decodedInputEvent {
	decoder := uv.EventDecoder{}
	events := make([]decodedInputEvent, 0, len(raw))
	for len(raw) > 0 {
		n, event := decoder.Decode(raw)
		if n <= 0 {
			n = 1
		}
		events = append(events, decodedInputEvent{
			raw:   append([]byte(nil), raw[:n]...),
			event: event,
		})
		raw = raw[n:]
	}
	return events
}

func normalizeLocalInput(raw []byte) []byte {
	var out []byte
	for _, decoded := range decodeInputEvents(raw) {
		if key, ok := decoded.event.(uv.KeyPressEvent); ok {
			if legacy := legacyBytesForKeyPress(key); len(legacy) > 0 {
				out = append(out, legacy...)
				continue
			}
		}
		out = append(out, decoded.raw...)
	}
	return out
}

func forwardedBytesForDecodedInput(decoded decodedInputEvent) []byte {
	if key, ok := decoded.event.(uv.KeyPressEvent); ok {
		if legacy := legacyPaneBytesForKeyPress(key); len(legacy) > 0 {
			return legacy
		}
	}
	return decoded.raw
}

func legacyPaneBytesForKeyPress(key uv.KeyPressEvent) []byte {
	return legacyBytesForKeyPress(key)
}

func keyPressMatchesByte(key uv.KeyPressEvent, want byte) bool {
	legacy := legacyBytesForKeyPress(key)
	return len(legacy) == 1 && legacy[0] == want
}

func clientUIEventForDecodedInput(decoded decodedInputEvent) (string, bool) {
	switch decoded.event.(type) {
	case uv.FocusEvent:
		return proto.UIEventClientFocusGained, true
	case uv.BlurEvent:
		return "", true
	default:
		return "", false
	}
}

func hasActivityInput(raw []byte) bool {
	for _, decoded := range decodeInputEvents(raw) {
		if _, handled := clientUIEventForDecodedInput(decoded); handled {
			continue
		}
		return true
	}
	return false
}

func legacyBytesForKeyPress(key uv.KeyPressEvent) []byte {
	k := key.Key()
	mod := normalizedLegacyKeyMod(k.Mod)
	code := legacyCtrlRune(k)

	// Shifted printable keys should still behave like their textual byte form
	// for local bindings such as prefix + M, including alt-modified text that
	// should preserve the shifted glyph.
	if ascii := asciiTextBytes(k.Text); len(ascii) > 0 {
		switch mod &^ uv.ModShift {
		case 0:
			return ascii
		case uv.ModAlt:
			return append([]byte{0x1b}, ascii...)
		}
	}

	switch mod {
	case 0:
		if seq := legacySpecialKeySequence(k.Code); len(seq) > 0 {
			return seq
		}
	case uv.ModAlt:
		if ascii := legacyPrintableBytes(k); len(ascii) > 0 {
			return append([]byte{0x1b}, ascii...)
		}
		if seq := legacySpecialKeySequence(k.Code); len(seq) > 0 {
			return append([]byte{0x1b}, seq...)
		}
	case uv.ModCtrl:
		if b, ok := legacyCtrlByte(code); ok {
			return []byte{b}
		}
	case uv.ModCtrl | uv.ModAlt:
		if b, ok := legacyCtrlByte(code); ok {
			return []byte{0x1b, b}
		}
	}

	return nil
}

func normalizedLegacyKeyMod(mod uv.KeyMod) uv.KeyMod {
	mod &^= uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock
	if mod&uv.ModCtrl != 0 {
		mod &^= uv.ModShift
	}
	return mod
}

func legacyPrintableBytes(k uv.Key) []byte {
	if k.Text != "" {
		if ascii := asciiTextBytes(k.Text); len(ascii) > 0 {
			return ascii
		}
	}
	if r := legacyCtrlRune(k); r != 0 {
		if ascii, ok := asciiRuneByte(r); ok {
			return []byte{ascii}
		}
	}
	return nil
}

func legacyCtrlRune(k uv.Key) rune {
	if k.ShiftedCode != 0 && k.Mod.Contains(uv.ModShift) {
		return k.ShiftedCode
	}
	if k.Code != 0 {
		return k.Code
	}
	if k.Text == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(k.Text)
	if r == utf8.RuneError || size != len(k.Text) {
		return 0
	}
	return r
}

func asciiTextBytes(text string) []byte {
	if text == "" {
		return nil
	}
	buf := make([]byte, 0, len(text))
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError || r > unicode.MaxASCII {
			return nil
		}
		buf = append(buf, byte(r))
		text = text[size:]
	}
	return buf
}

func asciiRuneByte(r rune) (byte, bool) {
	if r < 0 || r > unicode.MaxASCII {
		return 0, false
	}
	return byte(r), true
}

func legacyCtrlByte(code rune) (byte, bool) {
	// Match tmux input_key_vt10x's Ctrl+printable fallback map for keys that
	// cannot be represented as distinct C0 controls in legacy mode.
	if ascii, ok := asciiRuneByte(code); ok {
		switch ascii {
		case '1', '!':
			return '1', true
		case '9', '(':
			return '9', true
		case '0', ')':
			return '0', true
		case '=', '+':
			return '=', true
		case ';', ':':
			return ';', true
		case '\'', '"':
			return '\'', true
		case ',', '<':
			return ',', true
		case '.', '>':
			return '.', true
		case '/', '-':
			return 0x1f, true
		case '8', '?':
			return 0x7f, true
		case ' ', '2':
			return 0x00, true
		case '3', '4', '5', '6', '7':
			return ascii - 0x18, true
		}
	}

	switch {
	case code >= 'a' && code <= 'z':
		return byte(code-'a') + 1, true
	case code >= 'A' && code <= 'Z':
		return byte(code-'A') + 1, true
	case code >= '@' && code <= '~':
		return byte(code) & 0x1f, true
	}

	switch code {
	case '@', uv.KeySpace:
		return 0x00, true
	case '[', uv.KeyEscape:
		return 0x1b, true
	case '\\':
		return 0x1c, true
	case ']':
		return 0x1d, true
	case '^':
		return 0x1e, true
	case '_':
		return 0x1f, true
	case '?':
		return 0x7f, true
	case uv.KeyTab:
		return '\t', true
	case uv.KeyEnter:
		return '\r', true
	case uv.KeyBackspace:
		return 0x08, true
	default:
		return 0, false
	}
}

func legacySpecialKeySequence(code rune) []byte {
	switch code {
	case uv.KeyEnter:
		return []byte{'\r'}
	case uv.KeyTab:
		return []byte{'\t'}
	case uv.KeyEscape:
		return []byte{0x1b}
	case uv.KeyBackspace:
		return []byte{0x7f}
	case uv.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case uv.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case uv.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case uv.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case uv.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case uv.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case uv.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case uv.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case uv.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case uv.KeyInsert:
		return []byte{0x1b, '[', '2', '~'}
	default:
		return nil
	}
}
