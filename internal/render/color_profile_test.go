package render

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestEmitDiffWithProfileDegradesRGBStyles(t *testing.T) {
	t.Parallel()

	changes := []CellChange{
		{
			X: 0,
			Y: 0,
			Cell: ScreenCell{
				Char:  "X",
				Width: 1,
				Style: uv.Style{
					Fg: ansi.RGBColor{R: 0xff, G: 0x88},
					Bg: ansi.RGBColor{B: 0xff},
				},
			},
		},
	}

	got := emitDiffWithProfile(changes, termenv.ANSI256)
	if strings.Contains(got, "38;2;") || strings.Contains(got, "48;2;") {
		t.Fatalf("emitDiffWithProfile() left truecolor escapes in output: %q", got)
	}
	if want := termenv.ANSI256.Color("#ff8800").Sequence(false); !strings.Contains(got, want) {
		t.Fatalf("emitDiffWithProfile() missing ANSI256 foreground %q in %q", want, got)
	}
	if want := termenv.ANSI256.Color("#0000ff").Sequence(true); !strings.Contains(got, want) {
		t.Fatalf("emitDiffWithProfile() missing ANSI256 background %q in %q", want, got)
	}
}

func TestRenderFullWithOverlayDegradesANSIColors(t *testing.T) {
	t.Parallel()

	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, 24, 4)
	c := NewCompositor(24, 4, "main")
	c.SetColorProfile(termenv.ANSI256)

	pane := &statusPaneData{
		id:     1,
		name:   "pane-1",
		color:  "ff8800",
		screen: "hello",
	}

	got := c.RenderFullWithOverlay(root, 1, func(uint32) PaneData { return pane }, OverlayState{}, true)
	if strings.Contains(got, "38;2;") || strings.Contains(got, "48;2;") {
		t.Fatalf("RenderFullWithOverlay() left truecolor escapes in output: %q", got)
	}
	if want := termenv.ANSI256.Color("#ff8800").Sequence(false); !strings.Contains(got, want) {
		t.Fatalf("RenderFullWithOverlay() missing ANSI256 foreground %q in %q", want, got)
	}
	if want := termenv.ANSI256.Color("#" + config.Surface0Hex).Sequence(true); !strings.Contains(got, want) {
		t.Fatalf("RenderFullWithOverlay() missing ANSI256 background %q in %q", want, got)
	}
}
