package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
)

const dropPlaceholderChar = "░"

func buildDropIndicatorCells(g *ScreenGrid, overlay *DropIndicatorOverlay) {
	if g == nil || overlay == nil || overlay.W <= 0 || overlay.H <= 0 {
		return
	}

	style := uv.Style{
		Fg:    hexToColor(config.DimColorHex),
		Bg:    hexToColor(config.Surface0Hex),
	}

	for dy := 0; dy < overlay.H; dy++ {
		for dx := 0; dx < overlay.W; dx++ {
			g.Set(overlay.X+dx, overlay.Y+dy, ScreenCell{
				Char:  dropPlaceholderChar,
				Width: 1,
				Style: style,
			})
		}
	}
}

func renderDropIndicator(buf *strings.Builder, overlay *DropIndicatorOverlay) {
	if overlay == nil || overlay.W <= 0 || overlay.H <= 0 {
		return
	}

	buf.WriteString(Surface0Bg)
	buf.WriteString(hexToANSI(config.DimColorHex))
	row := strings.Repeat(dropPlaceholderChar, overlay.W)
	for dy := 0; dy < overlay.H; dy++ {
		writeCursorTo(buf, overlay.Y+dy+1, overlay.X+1)
		buf.WriteString(row)
	}
	buf.WriteString(Reset)
}
