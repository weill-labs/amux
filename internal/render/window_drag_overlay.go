package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
)

const windowDropIndicatorChar = "│"

func buildWindowDropIndicatorCell(g *ScreenGrid, overlay *WindowDropIndicatorOverlay, y int) {
	if g == nil || overlay == nil || overlay.Column < 0 || y < 0 {
		return
	}
	g.Set(overlay.Column, y, ScreenCell{
		Char:  windowDropIndicatorChar,
		Width: 1,
		Style: uv.Style{
			Fg: hexToColor(config.BlueHex),
			Bg: hexToColor(config.Surface0Hex),
		},
	})
}

func renderWindowDropIndicator(buf *strings.Builder, overlay *WindowDropIndicatorOverlay, y int, profile termenv.Profile) {
	if overlay == nil || overlay.Column < 0 {
		return
	}
	writeCursorTo(buf, y+1, overlay.Column+1)
	buf.WriteString(Surface0Bg)
	buf.WriteString(hexToANSIWithProfile(config.BlueHex, profile))
	buf.WriteString(windowDropIndicatorChar)
	buf.WriteString(Reset)
}
