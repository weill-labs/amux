package client

import (
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

type decodedInputEvent struct {
	raw   []byte
	event uv.Event
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

func keyPressMatchesByte(key uv.KeyPressEvent, want byte) bool {
	legacy := legacyBytesForKeyPress(key)
	return len(legacy) == 1 && legacy[0] == want
}

func legacyBytesForKeyPress(key uv.KeyPressEvent) []byte {
	k := key.Key()

	// Shifted printable keys should still behave like their textual byte form
	// for local bindings such as prefix + M.
	if k.Text != "" && k.Mod&^(uv.ModShift|uv.ModCapsLock) == 0 {
		if ascii := asciiTextBytes(k.Text); len(ascii) > 0 {
			return ascii
		}
	}

	switch k.Mod {
	case 0:
		if seq := legacySpecialKeySequence(k.Code); len(seq) > 0 {
			return seq
		}
	case uv.ModAlt:
		if seq := legacySpecialKeySequence(k.Code); len(seq) > 0 {
			return append([]byte{0x1b}, seq...)
		}
		if ascii, ok := asciiRuneByte(k.Code); ok {
			return []byte{0x1b, ascii}
		}
	case uv.ModCtrl:
		if b, ok := legacyCtrlByte(k.Code); ok {
			return []byte{b}
		}
	case uv.ModCtrl | uv.ModAlt:
		if b, ok := legacyCtrlByte(k.Code); ok {
			return []byte{0x1b, b}
		}
	}

	return nil
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
	switch {
	case code >= 'a' && code <= 'z':
		return byte(code-'a') + 1, true
	case code >= 'A' && code <= 'Z':
		return byte(code-'A') + 1, true
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
