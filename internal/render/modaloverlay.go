package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
)

const (
	chooserModalMaxWidth  = 80
	chooserModalMinMargin = 2
)

func buildChooserOverlayCells(g *ScreenGrid, overlay *ChooserOverlay) {
	if overlay == nil {
		return
	}
	lines, styles, x, y := chooserOverlayLayout(g.Width, g.Height, overlay)
	if len(lines) == 0 {
		return
	}
	borderStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: hexToColor(config.Surface0Hex), Attrs: uv.AttrBold}
	textStyle := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: hexToColor(config.Surface0Hex)}
	dimStyle := uv.Style{Fg: hexToColor(config.DimColorHex), Bg: hexToColor(config.Surface0Hex)}
	selectedStyle := uv.Style{Fg: hexToColor(config.Surface0Hex), Bg: hexToColor(config.TextColorHex), Attrs: uv.AttrBold}

	for row, line := range lines {
		for col, r := range line {
			style := textStyle
			switch styles[row] {
			case "border":
				style = borderStyle
			case "selected":
				style = selectedStyle
			case "dim":
				style = dimStyle
			default:
				if col == 0 || col == len(line)-1 {
					style = borderStyle
				}
			}
			if styles[row] == "row" && (col == 0 || col == len(line)-1) {
				style = borderStyle
			}
			if styles[row] == "selected" && (col == 0 || col == len(line)-1) {
				style = borderStyle
			}
			if styles[row] == "dim" && (col == 0 || col == len(line)-1) {
				style = borderStyle
			}
			g.Set(x+col, y+row, ScreenCell{Char: string(r), Width: 1, Style: style})
		}
	}
}

func renderChooserOverlay(buf *strings.Builder, width, height int, overlay *ChooserOverlay) {
	if overlay == nil {
		return
	}
	lines, _, x, y := chooserOverlayLayout(width, height, overlay)
	if len(lines) == 0 {
		return
	}
	for row, line := range lines {
		writeCursorTo(buf, y+row+1, x+1)
		if row == 0 || row == len(lines)-1 {
			buf.WriteString(Surface0Bg + Bold + TextFg)
		} else {
			buf.WriteString(Surface0Bg + TextFg)
		}
		buf.WriteString(line)
		buf.WriteString(Reset)
	}
}

func chooserOverlayLayout(screenW, screenH int, overlay *ChooserOverlay) ([]string, []string, int, int) {
	if overlay == nil || screenW <= 0 || screenH <= 0 {
		return nil, nil, 0, 0
	}
	title := " " + overlay.Title + " "
	query := "> " + overlay.Query
	if overlay.Query == "" {
		query = "> "
	}
	width := len(title)
	if len(query) > width {
		width = len(query)
	}
	for _, row := range overlay.Rows {
		if len(row.Text) > width {
			width = len(row.Text)
		}
	}
	width += 2
	maxWidth := screenW - chooserModalMinMargin*2
	if maxWidth < 10 {
		return nil, nil, 0, 0
	}
	if width > chooserModalMaxWidth {
		width = chooserModalMaxWidth
	}
	if width > maxWidth {
		width = maxWidth
	}

	rowLimit := screenH - chooserModalMinMargin*2 - 3
	if rowLimit < 1 {
		return nil, nil, 0, 0
	}
	start := 0
	end := len(overlay.Rows)
	if end-start > rowLimit {
		start = overlay.Selected - rowLimit/2
		if start < 0 {
			start = 0
		}
		end = start + rowLimit
		if end > len(overlay.Rows) {
			end = len(overlay.Rows)
			start = end - rowLimit
		}
	}

	lines := make([]string, 0, end-start+3)
	styles := make([]string, 0, end-start+3)
	lines = append(lines, "+"+padOrTrim(title, width-2)+"+")
	styles = append(styles, "border")
	lines = append(lines, "|"+padOrTrim(query, width-2)+"|")
	styles = append(styles, "row")
	for i := start; i < end; i++ {
		row := overlay.Rows[i]
		lines = append(lines, "|"+padOrTrim(row.Text, width-2)+"|")
		style := "row"
		if !row.Selectable {
			style = "dim"
		}
		if i == overlay.Selected && row.Selectable {
			style = "selected"
		}
		styles = append(styles, style)
	}
	lines = append(lines, "+"+strings.Repeat("-", width-2)+"+")
	styles = append(styles, "border")

	height := len(lines)
	if height > screenH-chooserModalMinMargin*2 {
		return nil, nil, 0, 0
	}
	x := (screenW - width) / 2
	y := (screenH - height) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return lines, styles, x, y
}

func padOrTrim(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) > width {
		return s[:width]
	}
	if len(s) < width {
		return s + strings.Repeat(" ", width-len(s))
	}
	return s
}
