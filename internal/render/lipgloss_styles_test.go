package render

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/weill-labs/amux/internal/config"
)

func TestCatppuccinMochaLipGlossPaletteUsesNamedHexConstants(t *testing.T) {
	t.Parallel()

	palette := newCatppuccinMochaLipGlossPalette()

	tests := []struct {
		name string
		got  lipgloss.TerminalColor
		want string
	}{
		{name: "surface0", got: palette.surface0, want: "#" + config.Surface0Hex},
		{name: "text", got: palette.text, want: "#" + config.TextColorHex},
		{name: "dim", got: palette.dim, want: "#" + config.DimColorHex},
		{name: "blue", got: palette.blue, want: "#" + config.BlueHex},
		{name: "green", got: palette.green, want: "#" + config.GreenHex},
		{name: "yellow", got: palette.yellow, want: "#" + config.YellowHex},
		{name: "red", got: palette.red, want: "#" + config.RedHex},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprint(tt.got); got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestStatusBarStylesExposeSemanticRoles(t *testing.T) {
	t.Parallel()

	styles := newStatusBarStyles(config.GreenHex)

	tests := []struct {
		name          string
		style         lipgloss.Style
		wantFG        string
		wantBG        string
		wantBold      bool
		wantFaint     bool
		wantStrikeout bool
	}{
		{
			name:   "dim",
			style:  styles.dim,
			wantFG: "#" + config.DimColorHex,
			wantBG: "#" + config.Surface0Hex,
		},
		{
			name:   "active",
			style:  styles.active,
			wantFG: "#" + config.GreenHex,
			wantBG: "#" + config.Surface0Hex,
		},
		{
			name:     "focused",
			style:    styles.focused,
			wantFG:   "#" + config.BlueHex,
			wantBG:   "#" + config.Surface0Hex,
			wantBold: true,
		},
		{
			name:   "idle",
			style:  styles.idle,
			wantFG: "#" + config.DimColorHex,
			wantBG: "#" + config.Surface0Hex,
		},
		{
			name:   "busy",
			style:  styles.busy,
			wantFG: "#" + config.TextColorHex,
			wantBG: "#" + config.Surface0Hex,
		},
		{
			name:          "completed metadata",
			style:         styles.completedMeta,
			wantFG:        "#" + config.DimColorHex,
			wantBG:        "#" + config.Surface0Hex,
			wantStrikeout: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprint(tt.style.GetForeground()); got != tt.wantFG {
				t.Fatalf("%s foreground = %q, want %q", tt.name, got, tt.wantFG)
			}
			if got := fmt.Sprint(tt.style.GetBackground()); got != tt.wantBG {
				t.Fatalf("%s background = %q, want %q", tt.name, got, tt.wantBG)
			}
			if got := tt.style.GetBold(); got != tt.wantBold {
				t.Fatalf("%s bold = %v, want %v", tt.name, got, tt.wantBold)
			}
			if got := tt.style.GetFaint(); got != tt.wantFaint {
				t.Fatalf("%s faint = %v, want %v", tt.name, got, tt.wantFaint)
			}
			if got := tt.style.GetStrikethrough(); got != tt.wantStrikeout {
				t.Fatalf("%s strikethrough = %v, want %v", tt.name, got, tt.wantStrikeout)
			}
		})
	}
}
