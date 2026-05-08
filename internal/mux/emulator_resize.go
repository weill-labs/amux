package mux

import uv "github.com/charmbracelet/ultraviolet"

type resizeReflowSnapshot struct {
	lines  []resizeReflowLine
	cursor resizeReflowPosition
}

type resizeReflowLine struct {
	cells []uv.Cell
}

type resizeReflowPosition struct {
	logical int
	offset  int
}

func (v *vtEmulator) captureShrinkReflow(oldWidth, oldHeight, newWidth int) (resizeReflowSnapshot, bool) {
	var snapshot resizeReflowSnapshot
	if v == nil || v.emu == nil || oldWidth <= 0 || oldHeight <= 0 || newWidth <= 0 || newWidth >= oldWidth || v.IsAltScreen() {
		return snapshot, false
	}

	cursorCol, cursorRow := v.CursorPosition()
	cursorRow = clampInt(cursorRow, 0, oldHeight-1)
	rowLogical := make([]int, oldHeight)
	rowBase := make([]int, oldHeight)
	hasOverflow := false

	for y := 0; y < oldHeight; y++ {
		preserveCols := []int(nil)
		if y == cursorRow {
			preserveCols = append(preserveCols, cursorCol)
		}
		cells := captureResizeReflowCells(func(x int) *uv.Cell {
			return v.emu.CellAt(x, y)
		}, oldWidth, preserveCols...)
		rowOverflows := resizeLineEnd(func(x int) *uv.Cell { return v.emu.CellAt(x, y) }, oldWidth) > newWidth
		if rowOverflows {
			hasOverflow = true
		}

		if y == 0 || !lineUsesFullWidth(oldWidth, func(x int) *uv.Cell { return v.emu.CellAt(x, y-1) }) || !rowOverflows {
			snapshot.lines = append(snapshot.lines, resizeReflowLine{cells: cells})
			rowLogical[y] = len(snapshot.lines) - 1
			rowBase[y] = 0
			continue
		}

		rowLogical[y] = rowLogical[y-1]
		rowBase[y] = rowBase[y-1] + oldWidth
		snapshot.lines[rowLogical[y]].cells = append(snapshot.lines[rowLogical[y]].cells, cells...)
	}

	snapshot.cursor = resizeReflowPosition{
		logical: rowLogical[cursorRow],
		offset:  rowBase[cursorRow] + max(cursorCol, 0),
	}
	return snapshot, hasOverflow
}

func captureResizeReflowCells(cellAt func(int) *uv.Cell, width int, preserveCols ...int) []uv.Cell {
	if width <= 0 {
		return nil
	}
	limit := resizeLineEnd(cellAt, width, preserveCols...)
	if limit == 0 {
		return nil
	}

	cells := make([]uv.Cell, 0, limit)
	for col := 0; col < limit; {
		cell := cellAt(col)
		if cell == nil {
			cells = append(cells, uv.EmptyCell)
			col++
			continue
		}
		if cell.Width == 0 {
			col++
			continue
		}
		cells = append(cells, *cell)
		col += max(cell.Width, 1)
	}
	return cells
}

func resizeLineEnd(cellAt func(int) *uv.Cell, width int, preserveCols ...int) int {
	end := 0
	for col := 0; col < width; {
		cell := cellAt(col)
		if cell == nil {
			col++
			continue
		}
		if cell.Width == 0 {
			col++
			continue
		}
		cellWidth := max(cell.Width, 1)
		if cellUsesFullWidth(*cell) {
			end = max(end, col+cellWidth)
		}
		col += cellWidth
	}
	for _, preserve := range preserveCols {
		if preserve < 0 {
			continue
		}
		end = max(end, min(preserve+1, width))
	}
	return end
}

func (v *vtEmulator) repaintReflowedScreen(snapshot resizeReflowSnapshot, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	rows, counts := wrapResizeReflowLines(snapshot.lines, width)
	wrappedCursor := resizeWrappedPosition(counts, snapshot.cursor, width)
	startRow := 0
	if resizeReflowLineHasContent(snapshot.lines, snapshot.cursor.logical) {
		_, currentCursorRow := v.CursorPosition()
		startRow = resizeReflowStartRow(len(rows), wrappedCursor.Y, currentCursorRow, height)
	}
	if startRow > 0 {
		rows = rows[startRow:]
	}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			cell := uv.EmptyCell
			if y < len(rows) && x < len(rows[y]) {
				cell = rows[y][x]
			}
			v.emu.SetCell(x, y, &cell)
		}
	}
}

func resizeReflowLineHasContent(lines []resizeReflowLine, index int) bool {
	if index < 0 || index >= len(lines) {
		return false
	}
	for _, cell := range lines[index].cells {
		if cellUsesFullWidth(cell) {
			return true
		}
	}
	return false
}

func resizeReflowStartRow(rowCount, cursorRow, targetRow, height int) int {
	if rowCount <= height || cursorRow < height {
		return 0
	}
	start := cursorRow - clampInt(targetRow, 0, height-1)
	return clampInt(start, 0, rowCount-height)
}

func wrapResizeReflowLines(lines []resizeReflowLine, width int) ([]uv.Line, []int) {
	if width <= 0 {
		width = 1
	}
	rows := make([]uv.Line, 0, len(lines))
	counts := make([]int, len(lines))
	for i, line := range lines {
		wrapped := wrapResizeReflowLine(line.cells, width)
		counts[i] = len(wrapped)
		rows = append(rows, wrapped...)
	}
	return rows, counts
}

func wrapResizeReflowLine(cells []uv.Cell, width int) []uv.Line {
	row := uv.NewLine(width)
	rows := make([]uv.Line, 0, 1)
	col := 0

	appendRow := func() {
		rows = append(rows, row)
		row = uv.NewLine(width)
		col = 0
	}

	if len(cells) == 0 {
		appendRow()
		return rows
	}

	for _, cell := range cells {
		cellWidth := max(cell.Width, 1)
		if col > 0 && col+cellWidth > width {
			appendRow()
		}
		row.Set(col, &cell)
		col += cellWidth
		if col >= width {
			appendRow()
		}
	}
	if col > 0 {
		appendRow()
	}
	return rows
}

func resizeWrappedPosition(counts []int, pos resizeReflowPosition, width int) uv.Position {
	if len(counts) == 0 {
		return uv.Pos(0, 0)
	}
	pos.logical = clampInt(pos.logical, 0, len(counts)-1)
	if pos.offset < 0 {
		pos.offset = 0
	}
	row := 0
	for i := 0; i < pos.logical; i++ {
		row += counts[i]
	}
	return uv.Pos(pos.offset%width, row+pos.offset/width)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
