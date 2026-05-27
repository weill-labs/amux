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
	sc := ScreenCell{
		Char:  cell.Char,
		Style: cell.Style,
		Width: cell.Width,
	}
	normalizeScreenCellInPlace(&sc)
	return sc
}

func ScreenCellFromCopyModeInto(dst *ScreenCell, cell proto.Cell) {
	dst.Char = cell.Char
	dst.Link = uv.Link{}
	dst.Style = cell.Style
	dst.Width = cell.Width
	normalizeScreenCellInPlace(dst)
}

func normalizeScreenCell(cell ScreenCell) ScreenCell {
	sc := cell
	normalizeScreenCellInPlace(&sc)
	return sc
}

func normalizeScreenCellInPlace(cell *ScreenCell) {
	if cell.Char == "" {
		cell.Char = " "
	}
	if cell.Width < 0 {
		cell.Width = 1
	}
}

func applyCopyModeOverlay(base ScreenCell, overlay *proto.ViewportOverlay, col, row int) ScreenCell {
	applyCopyModeOverlayInPlace(&base, overlay, col, row)
	return base
}

func applyCopyModeOverlayInPlace(base *ScreenCell, overlay *proto.ViewportOverlay, col, row int) {
	normalizeScreenCellInPlace(base)
	if overlay == nil {
		return
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

	var state emittedCellState
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
			state.transition(&buf, cell, defaultColorProfile)

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
	state.closeHyperlink(&buf)
	if state.hasStyle {
		if diff := styleDiffWithProfile(state.stylePtr(), uv.Style{}, defaultColorProfile); diff != "" {
			buf.WriteString(diff)
		}
	}
	return buf.String()
}
