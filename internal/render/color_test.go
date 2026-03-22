package render

import (
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

func TestBlendHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fg, bg string
		ratio  float64
		want   string
	}{
		{"zero ratio returns bg", "ffffff", "000000", 0.0, "000000"},
		{"full ratio returns fg", "ffffff", "000000", 1.0, "ffffff"},
		{"half white on black", "ffffff", "000000", 0.5, "808080"},
		{"half black on white", "000000", "ffffff", 0.5, "808080"},
		{"quarter red on black", "ff0000", "000000", 0.25, "400000"},
		{"negative ratio clamps to bg", "ffffff", "000000", -0.5, "000000"},
		{"ratio above 1 clamps to fg", "ffffff", "000000", 1.5, "ffffff"},
		{"empty fg returns bg", "", "313244", 0.25, "313244"},
		{"short fg returns bg", "abc", "313244", 0.25, "313244"},
		{"invalid fg hex returns bg", "zzzzzz", "313244", 0.25, "313244"},
		{"same color returns itself", "abcdef", "abcdef", 0.5, "abcdef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := blendHex(tt.fg, tt.bg, tt.ratio)
			if got != tt.want {
				t.Errorf("blendHex(%q, %q, %v) = %q, want %q", tt.fg, tt.bg, tt.ratio, got, tt.want)
			}
		})
	}
}

func TestBlendHex_AllPaletteColors(t *testing.T) {
	t.Parallel()
	for _, hex := range config.CatppuccinMocha {
		t.Run(hex, func(t *testing.T) {
			t.Parallel()
			result := blendHex(hex, config.Surface0Hex, 0.25)
			if len(result) != 6 {
				t.Errorf("blendHex(%q, Surface0, 0.25) = %q, want 6-char hex", hex, result)
			}
			// Blended result should differ from both inputs (unless they're identical)
			if result == hex && hex != config.Surface0Hex {
				t.Errorf("blendHex(%q, Surface0, 0.25) = %q, expected different from fg", hex, result)
			}
			if result == config.Surface0Hex {
				t.Errorf("blendHex(%q, Surface0, 0.25) = Surface0, expected tinted result", hex)
			}
		})
	}
}

func TestStatusBarBgHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		color  string
		active bool
	}{
		{"active rosewater", "f5e0dc", true},
		{"inactive rosewater", "f5e0dc", false},
		{"active blue", "89b4fa", true},
		{"inactive blue", "89b4fa", false},
		{"empty color falls back", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := statusBarBgHex(tt.color, tt.active)
			if len(got) != 6 {
				t.Errorf("statusBarBgHex(%q, %v) = %q, want 6-char hex", tt.color, tt.active, got)
			}
			// Active should produce a more saturated blend than inactive
			if tt.color != "" && tt.active {
				inactive := statusBarBgHex(tt.color, false)
				if got == inactive {
					t.Errorf("active and inactive produced same result %q", got)
				}
			}
		})
	}
}
