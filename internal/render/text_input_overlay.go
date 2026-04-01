package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
)

func buildTextInputOverlayCells(g *ScreenGrid, overlay *TextInputOverlay) {
	if overlay == nil {
		return
	}
	lines, x, y := textInputOverlayLayout(g.Width, g.Height, overlay)
	if len(lines) == 0 {
		return
	}
	borderStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: hexToColor(config.Surface0Hex), Attrs: uv.AttrBold}
	textStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: hexToColor(config.Surface0Hex)}

	for row, line := range lines {
		for col, r := range line {
			style := textStyle
			if col == 0 || col == len(line)-1 || row == 0 || row == len(lines)-1 {
				style = borderStyle
			}
			g.Set(x+col, y+row, ScreenCell{Char: string(r), Width: 1, Style: style})
		}
	}
}

func renderTextInputOverlay(buf *strings.Builder, width, height int, overlay *TextInputOverlay) {
	renderTextInputOverlayWithProfile(buf, width, height, overlay, defaultColorProfile)
}

func renderTextInputOverlayWithProfile(buf *strings.Builder, width, height int, overlay *TextInputOverlay, profile termenv.Profile) {
	if overlay == nil {
		return
	}
	lines, x, y := textInputOverlayLayout(width, height, overlay)
	if len(lines) == 0 {
		return
	}
	surface0Bg := bgHexSequence(config.Surface0Hex, profile)
	textFg := fgHexSequence(config.TextColorHex, profile)
	for row, line := range lines {
		writeCursorTo(buf, y+row+1, x+1)
		if row == 0 || row == len(lines)-1 {
			buf.WriteString(surface0Bg + Bold + textFg)
		} else {
			buf.WriteString(surface0Bg + textFg)
		}
		buf.WriteString(line)
		buf.WriteString(Reset)
	}
}

func textInputOverlayLayout(screenW, screenH int, overlay *TextInputOverlay) ([]string, int, int) {
	if overlay == nil || screenW <= 0 || screenH <= 0 {
		return nil, 0, 0
	}
	title := " " + overlay.Title + " "
	input := "> " + overlay.Input
	if overlay.Input == "" {
		input = "> "
	}
	width := len(title)
	if len(input) > width {
		width = len(input)
	}
	width += 2
	maxWidth := screenW - chooserModalMinMargin*2
	if maxWidth < 10 {
		return nil, 0, 0
	}
	if width > chooserModalMaxWidth {
		width = chooserModalMaxWidth
	}
	if width > maxWidth {
		width = maxWidth
	}

	lines := []string{
		"+" + padOrTrim(title, width-2) + "+",
		"|" + padOrTrim(input, width-2) + "|",
		"+" + strings.Repeat("-", width-2) + "+",
	}
	if len(lines) > screenH-chooserModalMinMargin*2 {
		return nil, 0, 0
	}

	x := (screenW - width) / 2
	y := (screenH - len(lines)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return lines, x, y
}
