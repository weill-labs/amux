package render

import (
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

func TestExtractColorMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream string
		width  int
		height int
		want   string
	}{
		{
			name:   "maps truecolor border and text separator",
			stream: "\x1b[38;2;245;224;220m│\x1b[38;2;205;214;244m│",
			width:  2,
			height: 1,
			want:   "R|",
		},
		{
			name:   "clamps cursor movement and skips osc sequence",
			stream: "\x1b]0;ignored\a\x1b[99;99H\x1b[38;2;242;205;205m│",
			width:  3,
			height: 1,
			want:   "  F",
		},
		{
			name:   "dim unknown and reset colors",
			stream: "\x1b[38;2;108;112;134m│\x1b[38;2;1;2;3m│\x1b[0m│",
			width:  3,
			height: 1,
			want:   ".?.",
		},
		{
			name:   "non border runes remain blank",
			stream: "\x1b[38;2;245;224;220mA B",
			width:  3,
			height: 1,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractColorMap(tt.stream, tt.width, tt.height); got != tt.want {
				t.Fatalf("ExtractColorMap() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractFgHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  string
		current string
		want    string
	}{
		{"reset_empty", "", "abcd00", ""},
		{"reset_zero", "0", "abcd00", ""},
		{"truecolor", "38;2;245;224;220", "", "f5e0dc"},
		{"unsupported_preserves_current", "31", "f5e0dc", "f5e0dc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := extractFgHex(tt.params, tt.current); got != tt.want {
				t.Fatalf("extractFgHex(%q, %q) = %q, want %q", tt.params, tt.current, got, tt.want)
			}
		})
	}
}

func TestColorToLetter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hex  string
		want byte
	}{
		{"empty_is_dim", "", '.'},
		{"dim", config.DimColorHex, '.'},
		{"catppuccin", "f5e0dc", 'R'},
		{"text_color_separator", config.TextColorHex, '|'},
		{"unknown", "010203", '?'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := colorToLetter(tt.hex); got != tt.want {
				t.Fatalf("colorToLetter(%q) = %q, want %q", tt.hex, got, tt.want)
			}
		})
	}
}

func TestIsBorderRune(t *testing.T) {
	t.Parallel()

	for _, r := range []rune{'│', '─', '┼', '┌', '┘'} {
		if !isBorderRune(r) {
			t.Fatalf("expected %q to be treated as a border rune", r)
		}
	}
	for _, r := range []rune{'A', ' ', '╳'} {
		if isBorderRune(r) {
			t.Fatalf("expected %q to not be treated as a border rune", r)
		}
	}
}
