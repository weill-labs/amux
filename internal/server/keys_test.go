package server

import (
	"bytes"
	"testing"
)

func TestParseKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  []byte
	}{
		// Special keys
		{"Enter", []byte{'\r'}},
		{"Return", []byte{'\r'}},
		{"Tab", []byte{'\t'}},
		{"BTab", []byte{0x1b, '[', 'Z'}},
		{"Escape", []byte{0x1b}},
		{"Esc", []byte{0x1b}},
		{"Space", []byte{' '}},
		{"BSpace", []byte{0x7f}},
		{"Backspace", []byte{0x7f}},

		// Arrow keys
		{"Up", []byte{0x1b, '[', 'A'}},
		{"Down", []byte{0x1b, '[', 'B'}},
		{"Right", []byte{0x1b, '[', 'C'}},
		{"Left", []byte{0x1b, '[', 'D'}},
		{"PageUp", []byte{0x1b, '[', '5', '~'}},
		{"PgUp", []byte{0x1b, '[', '5', '~'}},
		{"PageDown", []byte{0x1b, '[', '6', '~'}},
		{"PgDn", []byte{0x1b, '[', '6', '~'}},
		{"F1", []byte{0x1b, 'O', 'P'}},
		{"F12", []byte{0x1b, '[', '2', '4', '~'}},
		{"KP0", []byte{0x1b, 'O', 'p'}},
		{"KPEnter", []byte{0x1b, 'O', 'M'}},
		{"KPMultiply", []byte{0x1b, 'O', 'j'}},
		{"KPPeriod", []byte{0x1b, 'O', 'n'}},

		// Ctrl combinations
		{"C-c", []byte{0x03}},
		{"C-a", []byte{0x01}},
		{"C-z", []byte{0x1a}},
		{"C-Space", []byte{0x00}},
		{"C-3", []byte{0x1b}},
		{"C-/", []byte{0x1f}},
		{"C-C", []byte{0x03}}, // uppercase letter
		{"C-A", []byte{0x01}},
		{"C-d", []byte{0x04}},
		{"c-c", []byte{0x03}}, // lowercase prefix

		// Meta combinations
		{"M-a", []byte{0x1b, 'a'}},
		{"M-Up", []byte{0x1b, 0x1b, '[', 'A'}},
		{"M-Enter", []byte{0x1b, '\r'}},

		// Literal text
		{"hello", []byte("hello")},
		{"echo foo", []byte("echo foo")},
		{"a", []byte("a")},

		// Not a control sequence (too long)
		{"C-ab", []byte("C-ab")},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := parseKey(tt.input)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("parseKey(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestPacedKeyToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "enter", key: "Enter", want: true},
		{name: "return alias", key: "Return", want: true},
		{name: "ctrl key", key: "C-c", want: true},
		{name: "ctrl space alias", key: "C-Space", want: true},
		{name: "lowercase ctrl prefix", key: "c-d", want: true},
		{name: "arrow key", key: "Up", want: false},
		{name: "escape", key: "Escape", want: false},
		{name: "meta enter", key: "M-Enter", want: false},
		{name: "meta key", key: "M-a", want: false},
		{name: "literal text", key: "hello", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pacedKeyToken(tt.key); got != tt.want {
				t.Fatalf("pacedKeyToken(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
