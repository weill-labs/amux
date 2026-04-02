package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
)

func helpBarTextWidth(text string) int {
	return runewidth.StringWidth(text)
}

func buildHelpBarChars(width int, overlay *HelpBarOverlay) []styledChar {
	if overlay == nil || width <= 0 {
		return nil
	}

	bg := hexToColor(config.Surface0Hex)
	baseStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: bg}
	text := truncateRunes(strings.TrimSpace(overlay.Text), max(width-1, 0))
	chars := make([]styledChar, 0, width)
	chars = appendStyledStr(chars, " ", baseStyle)
	chars = appendStyledStr(chars, text, baseStyle)

	fill := width - 1 - helpBarTextWidth(text)
	if fill > 0 {
		chars = appendStyledStr(chars, strings.Repeat(" ", fill), baseStyle)
	}
	return chars
}

func buildHelpBarCells(g *ScreenGrid, overlay *HelpBarOverlay) {
	if g == nil || g.Height < 2 {
		return
	}
	chars := buildHelpBarChars(g.Width, overlay)
	for i := 0; i < g.Width && i < len(chars); i++ {
		g.Set(i, g.Height-2, ScreenCell{Char: chars[i].ch, Width: 1, Style: chars[i].style})
	}
}

func renderHelpBar(buf *strings.Builder, width, height int, overlay *HelpBarOverlay) {
	renderHelpBarWithProfile(buf, width, height, overlay, defaultColorProfile)
}

func renderHelpBarWithProfile(buf *strings.Builder, width, height int, overlay *HelpBarOverlay, profile termenv.Profile) {
	if overlay == nil || width <= 0 || height < 2 {
		return
	}

	writeCursorTo(buf, height-1, 1)
	styles := newStatusBarStyles(config.TextColorHex)
	text := truncateRunes(strings.TrimSpace(overlay.Text), max(width-1, 0))
	writeStyledTextWithProfile(buf, styles.background, " ", profile)
	writeStyledTextWithProfile(buf, styles.busy, text, profile)
	fill := width - 1 - helpBarTextWidth(text)
	if fill > 0 {
		writeStyledTextWithProfile(buf, styles.background, strings.Repeat(" ", fill), profile)
	}
	buf.WriteString(Reset)
}
