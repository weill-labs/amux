package keys

import (
	"bytes"
	"testing"
)

func TestParseKeyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want []byte
	}{
		{name: "return alias", key: "Return", want: []byte{'\r'}},
		{name: "escape alias", key: "Esc", want: []byte{0x1b}},
		{name: "backspace alias", key: "Backspace", want: []byte{0x7f}},
		{name: "page up alias", key: "PgUp", want: []byte{0x1b, '[', '5', '~'}},
		{name: "page down alias", key: "PgDn", want: []byte{0x1b, '[', '6', '~'}},
		{name: "keypad period alias", key: "KPPeriod", want: []byte{0x1b, 'O', 'n'}},

		{name: "ctrl enter", key: "C-Enter", want: []byte{'\r'}},
		{name: "ctrl tab", key: "C-Tab", want: []byte{'\t'}},
		{name: "ctrl escape alias", key: "C-Esc", want: []byte{0x1b}},
		{name: "ctrl backspace alias", key: "C-Backspace", want: []byte{0x08}},
		{name: "ctrl digit alias", key: "C-4", want: []byte{0x1c}},
		{name: "ctrl punctuation alias", key: "C-?", want: []byte{0x7f}},
		{name: "ctrl range punctuation", key: "C-[", want: []byte{0x1b}},
		{name: "ctrl range underscore", key: "C-_", want: []byte{0x1f}},
		{name: "ctrl shifted punctuation", key: "C-+", want: []byte{'='}},

		{name: "meta shifted printable", key: "M-S-a", want: []byte{0x1b, 'A'}},
		{name: "meta shifted named key", key: "M-S-Tab", want: []byte{0x1b, '\t'}},
		{name: "ctrl meta printable", key: "C-M-a", want: []byte{0x1b, 0x01}},

		{name: "invalid modifier falls back to literal", key: "Q-a", want: []byte("Q-a")},
		{name: "empty base falls back to literal", key: "C-", want: []byte("C-")},
		{name: "ctrl multi rune falls back to literal", key: "C-ab", want: []byte("C-ab")},
		{name: "meta multi rune falls back to literal", key: "M-ab", want: []byte("M-ab")},
		{name: "ctrl non ascii falls back to literal", key: "C-é", want: []byte("C-é")},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ParseKey(tt.key); !bytes.Equal(got, tt.want) {
				t.Fatalf("ParseKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestPacedKeyTokenMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "enter alias", key: "Return", want: true},
		{name: "ctrl meta printable", key: "C-M-a", want: true},
		{name: "meta enter", key: "M-Enter", want: false},
		{name: "invalid modifier", key: "Q-a", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := PacedKeyToken(tt.key); got != tt.want {
				t.Fatalf("PacedKeyToken(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestParseKeyToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       string
		wantBase  string
		wantMods  keyModifier
		wantShift bool
		wantOK    bool
	}{
		{name: "ctrl meta shift", key: "C-M-S-F1", wantBase: "F1", wantMods: keyModCtrl | keyModMeta, wantShift: true, wantOK: true},
		{name: "lowercase modifiers", key: "c-m-s-a", wantBase: "a", wantMods: keyModCtrl | keyModMeta, wantShift: true, wantOK: true},
		{name: "invalid modifier", key: "Q-a", wantOK: false},
		{name: "missing hyphen", key: "Enter", wantOK: false},
		{name: "empty base", key: "C-", wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotBase, gotMods, gotShift, gotOK := parseKeyToken(tt.key)
			if gotBase != tt.wantBase || gotMods != tt.wantMods || gotShift != tt.wantShift || gotOK != tt.wantOK {
				t.Fatalf("parseKeyToken(%q) = (%q, %v, %v, %v), want (%q, %v, %v, %v)", tt.key, gotBase, gotMods, gotShift, gotOK, tt.wantBase, tt.wantMods, tt.wantShift, tt.wantOK)
			}
		})
	}
}

func TestNamedKeyBytesCopiesData(t *testing.T) {
	t.Parallel()

	got, ok := namedKeyBytes("Esc")
	if !ok {
		t.Fatal("namedKeyBytes(Esc) = not found, want found")
	}
	got[0] = 'x'

	again, ok := namedKeyBytes("Escape")
	if !ok {
		t.Fatal("namedKeyBytes(Escape) = not found, want found")
	}
	if !bytes.Equal(again, []byte{0x1b}) {
		t.Fatalf("namedKeyBytes copy was mutated: got %v, want %v", again, []byte{0x1b})
	}
}
