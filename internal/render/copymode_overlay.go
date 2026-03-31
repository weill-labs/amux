package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/proto"
)

var (
	copySelectionBg = ansi.BasicColor(4)  // blue
	copyMatchBg     = ansi.BasicColor(3)  // yellow
	copyCurrentBg   = ansi.BasicColor(11) // bright yellow
)

func ScreenCellFromCopyMode(cell proto.Cell) ScreenCell {
	return normalizeScreenCell(ScreenCell{
		Char:  cell.Char,
		Style: cell.Style,
		Width: cell.Width,
	})
}

func normalizeScreenCell(cell ScreenCell) ScreenCell {
	sc := cell
	if sc.Char == "" {
		sc.Char = " "
	}
	if sc.Width < 0 {
		sc.Width = 1
	}
	return sc
}

func applyCopyModeOverlay(base ScreenCell, overlay *proto.ViewportOverlay, col, row int) ScreenCell {
	base = normalizeScreenCell(base)
	if overlay == nil {
		return base
	}

	kind := copyModeHighlightAt(overlay, row, col)
	switch kind {
	case proto.HighlightSelection:
		base.Style.Bg = copySelectionBg
	case proto.HighlightSearchMatch:
		base.Style.Bg = copyMatchBg
	case proto.HighlightCurrentMatch:
		base.Style.Bg = copyCurrentBg
		base.Style.Attrs |= uv.AttrBold
	}

	if overlay.Cursor == (proto.CursorPosition{Col: col, Row: row}) {
		base.Style.Attrs |= uv.AttrReverse
	}

	return base
}

func copyModeHighlightAt(overlay *proto.ViewportOverlay, row, col int) proto.HighlightKind {
	var best proto.HighlightKind
	for _, line := range overlay.HighlightedLines {
		if line.Row != row {
			continue
		}
		for _, span := range line.Spans {
			if col < span.StartCol || col >= span.EndCol {
				continue
			}
			if highlightPriority(span.Kind) > highlightPriority(best) {
				best = span.Kind
			}
		}
	}
	return best
}

func highlightPriority(kind proto.HighlightKind) int {
	switch kind {
	case proto.HighlightCurrentMatch:
		return 3
	case proto.HighlightSearchMatch:
		return 2
	case proto.HighlightSelection:
		return 1
	default:
		return 0
	}
}

func RenderPaneViewportANSI(width, height int, active bool, pd PaneData) string {
	var buf strings.Builder
	buf.Grow(width * height * 2)

	var prevStyle *uv.Style
	copyOverlay := pd.CopyModeOverlay()
	for row := 0; row < height; row++ {
		if row > 0 {
			buf.WriteByte('\n')
		}
		rowCells := paneContentRowCells(width, row, active, pd, copyOverlay)
		for col := 0; col < width; {
			cell := rowCells[col]
			if cell.Width == 0 {
				col++
				continue
			}
			if diff := uv.StyleDiff(prevStyle, &cell.Style); diff != "" {
				buf.WriteString(diff)
			}
			styleCopy := cell.Style
			prevStyle = &styleCopy

			char := cell.Char
			if char == "" {
				char = " "
			}
			buf.WriteString(char)

			cellWidth := cell.Width
			if cellWidth <= 0 {
				cellWidth = 1
			}
			col += cellWidth
		}
	}
	if prevStyle != nil {
		if diff := uv.StyleDiff(prevStyle, &uv.Style{}); diff != "" {
			buf.WriteString(diff)
		}
	}
	return buf.String()
}
