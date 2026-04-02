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

func helpBarRows(overlay *HelpBarOverlay) []string {
	if overlay == nil {
		return nil
	}
	rows := make([]string, 0, len(overlay.Rows))
	for _, row := range overlay.Rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func helpBarRowCount(overlay *HelpBarOverlay) int {
	return len(helpBarRows(overlay))
}

func buildHelpBarRowChars(width int, row string) []styledChar {
	if width <= 0 {
		return nil
	}

	bg := hexToColor(config.Surface0Hex)
	baseStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: bg}
	text := truncateRunes(strings.TrimSpace(row), max(width-1, 0))
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
	rows := helpBarRows(overlay)
	if len(rows) == 0 {
		return
	}
	if len(rows) > g.Height-1 {
		rows = rows[len(rows)-(g.Height-1):]
	}
	startY := g.Height - 1 - len(rows)
	for rowIdx, row := range rows {
		chars := buildHelpBarRowChars(g.Width, row)
		for i := 0; i < g.Width && i < len(chars); i++ {
			g.Set(i, startY+rowIdx, ScreenCell{Char: chars[i].ch, Width: 1, Style: chars[i].style})
		}
	}
}

func renderHelpBar(buf *strings.Builder, width, height int, overlay *HelpBarOverlay) {
	renderHelpBarWithProfile(buf, width, height, overlay, defaultColorProfile)
}

func renderHelpBarWithProfile(buf *strings.Builder, width, height int, overlay *HelpBarOverlay, profile termenv.Profile) {
	if overlay == nil || width <= 0 || height < 2 {
		return
	}

	rows := helpBarRows(overlay)
	if len(rows) == 0 {
		return
	}
	if len(rows) > height-1 {
		rows = rows[len(rows)-(height-1):]
	}

	styles := newStatusBarStyles(config.TextColorHex)
	startY := height - len(rows)
	for rowIdx, row := range rows {
		writeCursorTo(buf, startY+rowIdx, 1)
		text := truncateRunes(strings.TrimSpace(row), max(width-1, 0))
		writeStyledTextWithProfile(buf, styles.background, " ", profile)
		writeStyledTextWithProfile(buf, styles.busy, text, profile)
		fill := width - 1 - helpBarTextWidth(text)
		if fill > 0 {
			writeStyledTextWithProfile(buf, styles.background, strings.Repeat(" ", fill), profile)
		}
		buf.WriteString(Reset)
	}
}
