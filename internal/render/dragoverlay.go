package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func buildDropIndicatorCells(g *ScreenGrid, overlay *DropIndicatorOverlay) {
	if g == nil || overlay == nil || overlay.Length <= 0 {
		return
	}

	style := uv.Style{
		Fg:    hexToColor(config.BlueHex),
		Bg:    hexToColor(config.Surface0Hex),
		Attrs: uv.AttrBold,
	}

	char := "━"
	if overlay.Dir == mux.SplitVertical {
		char = "┃"
	}

	for i := 0; i < overlay.Length; i++ {
		x, y := overlay.X, overlay.Y
		if overlay.Dir == mux.SplitVertical {
			y += i
		} else {
			x += i
		}
		g.Set(x, y, ScreenCell{
			Char:  char,
			Width: 1,
			Style: style,
		})
	}
}

func renderDropIndicator(buf *strings.Builder, overlay *DropIndicatorOverlay) {
	if overlay == nil || overlay.Length <= 0 {
		return
	}

	buf.WriteString(Surface0Bg)
	buf.WriteString(Bold)
	buf.WriteString(hexToANSI(config.BlueHex))
	if overlay.Dir == mux.SplitHorizontal {
		writeCursorTo(buf, overlay.Y+1, overlay.X+1)
		buf.WriteString(strings.Repeat("━", overlay.Length))
		buf.WriteString(Reset)
		return
	}

	for i := 0; i < overlay.Length; i++ {
		writeCursorTo(buf, overlay.Y+i+1, overlay.X+1)
		buf.WriteString("┃")
	}
	buf.WriteString(Reset)
}
