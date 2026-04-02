package keys

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Chunk struct {
	Data       []byte
	PaceBefore bool
}

type keyModifier uint8

const (
	keyModCtrl keyModifier = 1 << iota
	keyModMeta
)

func ParseKeyArgs(args []string) (hexMode bool, keys []string) {
	for _, arg := range args {
		if arg == "--hex" {
			hexMode = true
		} else {
			keys = append(keys, arg)
		}
	}
	return hexMode, keys
}

func EncodeChunks(hexMode bool, keys []string) ([]Chunk, error) {
	var chunks []Chunk
	if hexMode {
		for _, hexStr := range keys {
			b, err := hex.DecodeString(hexStr)
			if err != nil {
				return nil, fmt.Errorf("invalid hex: %s", hexStr)
			}
			chunks = append(chunks, Chunk{Data: b})
		}
		return chunks, nil
	}

	for _, key := range keys {
		chunks = append(chunks, Chunk{
			Data:       ParseKey(key),
			PaceBefore: PacedKeyToken(key),
		})
	}
	return chunks, nil
}

func PacedKeyToken(key string) bool {
	if canonicalKeyName(key) == "Enter" {
		return true
	}
	if _, mods, _, ok := parseKeyToken(key); ok && mods&keyModCtrl != 0 {
		return true
	}
	return false
}

func ParseKey(key string) []byte {
	if b, ok := namedKeyBytes(key); ok {
		return b
	}

	if base, mods, shift, ok := parseKeyToken(key); ok {
		if b, ok := modifiedKeyBytes(base, mods, shift); ok {
			return b
		}
	}

	return []byte(key)
}

func parseKeyToken(key string) (base string, mods keyModifier, shift bool, ok bool) {
	parts := strings.Split(key, "-")
	if len(parts) < 2 {
		return "", 0, false, false
	}
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "C", "c":
			mods |= keyModCtrl
		case "M", "m":
			mods |= keyModMeta
		case "S", "s":
			shift = true
		default:
			return "", 0, false, false
		}
	}
	base = parts[len(parts)-1]
	if base == "" {
		return "", 0, false, false
	}
	return base, mods, shift, true
}

func modifiedKeyBytes(base string, mods keyModifier, shift bool) ([]byte, bool) {
	if mods&keyModCtrl != 0 {
		b, ok := ctrlModifiedKeyByte(base)
		if !ok {
			return nil, false
		}
		if mods&keyModMeta != 0 {
			return []byte{0x1b, b}, true
		}
		return []byte{b}, true
	}

	b, ok := baseKeyBytes(base, shift)
	if !ok {
		return nil, false
	}
	if mods&keyModMeta != 0 {
		return append([]byte{0x1b}, b...), true
	}
	return b, true
}

func baseKeyBytes(base string, shift bool) ([]byte, bool) {
	if b, ok := namedKeyBytes(base); ok {
		return b, true
	}
	if utf8.RuneCountInString(base) != 1 {
		return nil, false
	}
	if shift {
		base = strings.ToUpper(base)
	}
	return []byte(base), true
}

func ctrlModifiedKeyByte(base string) (byte, bool) {
	switch canonicalKeyName(base) {
	case "Space":
		return 0x00, true
	case "Tab":
		return '\t', true
	case "Enter":
		return '\r', true
	case "Escape":
		return 0x1b, true
	case "BSpace":
		return 0x08, true
	}

	if utf8.RuneCountInString(base) != 1 {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(base)

	if ascii, ok := asciiRuneByte(r); ok {
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
	case r >= 'a' && r <= 'z':
		return byte(r-'a') + 1, true
	case r >= 'A' && r <= 'Z':
		return byte(r-'A') + 1, true
	case r >= '@' && r <= '~':
		return byte(r) & 0x1f, true
	default:
		return 0, false
	}
}

func asciiRuneByte(r rune) (byte, bool) {
	if r < 0 || r > unicode.MaxASCII {
		return 0, false
	}
	return byte(r), true
}

func namedKeyBytes(key string) ([]byte, bool) {
	b, ok := specialKeys[canonicalKeyName(key)]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

func canonicalKeyName(key string) string {
	if alias, ok := specialKeyAliases[key]; ok {
		return alias
	}
	return key
}

var specialKeyAliases = map[string]string{
	"Return":    "Enter",
	"Esc":       "Escape",
	"Backspace": "BSpace",
	"PgUp":      "PageUp",
	"PgDn":      "PageDown",
	"KPPeriod":  "KPDecimal",
}

var specialKeys = map[string][]byte{
	"Enter":      {'\r'},
	"Tab":        {'\t'},
	"BTab":       {0x1b, '[', 'Z'},
	"Escape":     {0x1b},
	"Space":      {' '},
	"BSpace":     {0x7f},
	"Up":         {0x1b, '[', 'A'},
	"Down":       {0x1b, '[', 'B'},
	"Right":      {0x1b, '[', 'C'},
	"Left":       {0x1b, '[', 'D'},
	"Home":       {0x1b, '[', 'H'},
	"End":        {0x1b, '[', 'F'},
	"PageUp":     {0x1b, '[', '5', '~'},
	"PageDown":   {0x1b, '[', '6', '~'},
	"Delete":     {0x1b, '[', '3', '~'},
	"Insert":     {0x1b, '[', '2', '~'},
	"F1":         {0x1b, 'O', 'P'},
	"F2":         {0x1b, 'O', 'Q'},
	"F3":         {0x1b, 'O', 'R'},
	"F4":         {0x1b, 'O', 'S'},
	"F5":         {0x1b, '[', '1', '5', '~'},
	"F6":         {0x1b, '[', '1', '7', '~'},
	"F7":         {0x1b, '[', '1', '8', '~'},
	"F8":         {0x1b, '[', '1', '9', '~'},
	"F9":         {0x1b, '[', '2', '0', '~'},
	"F10":        {0x1b, '[', '2', '1', '~'},
	"F11":        {0x1b, '[', '2', '3', '~'},
	"F12":        {0x1b, '[', '2', '4', '~'},
	"KP0":        {0x1b, 'O', 'p'},
	"KP1":        {0x1b, 'O', 'q'},
	"KP2":        {0x1b, 'O', 'r'},
	"KP3":        {0x1b, 'O', 's'},
	"KP4":        {0x1b, 'O', 't'},
	"KP5":        {0x1b, 'O', 'u'},
	"KP6":        {0x1b, 'O', 'v'},
	"KP7":        {0x1b, 'O', 'w'},
	"KP8":        {0x1b, 'O', 'x'},
	"KP9":        {0x1b, 'O', 'y'},
	"KPEnter":    {0x1b, 'O', 'M'},
	"KPEqual":    {0x1b, 'O', 'X'},
	"KPMultiply": {0x1b, 'O', 'j'},
	"KPPlus":     {0x1b, 'O', 'k'},
	"KPComma":    {0x1b, 'O', 'l'},
	"KPMinus":    {0x1b, 'O', 'm'},
	"KPDecimal":  {0x1b, 'O', 'n'},
	"KPDivide":   {0x1b, 'O', 'o'},
}
