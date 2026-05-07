package client

import (
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

func TestConfigureThemeSetsRendererIconSet(t *testing.T) {
	t.Parallel()

	ascii := config.ThemeIconsASCII
	nerd := config.ThemeIconsNerd

	tests := []struct {
		name string
		cfg  *config.Config
		want render.IconSet
	}{
		{
			name: "nil config defaults to unicode",
			cfg:  nil,
			want: render.UnicodeIconSet(),
		},
		{
			name: "unset theme defaults to unicode",
			cfg:  &config.Config{},
			want: render.UnicodeIconSet(),
		},
		{
			name: "ascii",
			cfg:  &config.Config{Theme: config.ThemeConfig{Icons: &ascii}},
			want: render.ASCIIIconSet(),
		},
		{
			name: "nerd",
			cfg:  &config.Config{Theme: config.ThemeConfig{Icons: &nerd}},
			want: render.NerdFontIconSet(),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := NewClientRendererWithScrollback(20, 4, mux.DefaultScrollbackLines)
			defer cr.renderer.Close()

			cr.ConfigureTheme(tt.cfg)
			if got := cr.IconSet(); got != tt.want {
				t.Fatalf("IconSet() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
