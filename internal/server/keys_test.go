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
		{"Tab", []byte{'\t'}},
		{"Escape", []byte{0x1b}},
		{"Space", []byte{' '}},
		{"BSpace", []byte{0x7f}},

		// Arrow keys
		{"Up", []byte{0x1b, '[', 'A'}},
		{"Down", []byte{0x1b, '[', 'B'}},
		{"Right", []byte{0x1b, '[', 'C'}},
		{"Left", []byte{0x1b, '[', 'D'}},

		// Ctrl combinations
		{"C-c", []byte{0x03}},
		{"C-a", []byte{0x01}},
		{"C-z", []byte{0x1a}},
		{"C-C", []byte{0x03}}, // uppercase letter
		{"C-A", []byte{0x01}},
		{"C-d", []byte{0x04}},
		{"c-c", []byte{0x03}}, // lowercase prefix

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
