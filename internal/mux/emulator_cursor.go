package mux

import uv "github.com/charmbracelet/ultraviolet"

// isCursorBlock returns true if the cell at (x, y) is an isolated
// reverse-video space. "Isolated" means neither the left nor right neighbor
// has the reverse-video attribute, which distinguishes single-cell cursors
// from multi-cell highlights.
func (v *vtEmulator) isCursorBlock(x, y, w int) bool {
	cell := v.emu.CellAt(x, y)
	if cell == nil || cell.Style.Attrs&uv.AttrReverse == 0 {
		return false
	}
	if cell.Content != " " && cell.Content != "" {
		return false
	}
	if x > 0 {
		if left := v.emu.CellAt(x-1, y); left != nil && left.Style.Attrs&uv.AttrReverse != 0 {
			return false
		}
	}
	if x < w-1 {
		if right := v.emu.CellAt(x+1, y); right != nil && right.Style.Attrs&uv.AttrReverse != 0 {
			return false
		}
	}
	return true
}

func (v *vtEmulator) currentCursorBlock() (x, y int, ok bool) {
	w, h := int(v.w.Load()), int(v.h.Load())

	x, y = v.CursorPosition()
	if x < 0 || y < 0 || x >= w || y >= h {
		return 0, 0, false
	}
	if !v.isCursorBlock(x, y, w) {
		return v.fallbackCursorBlock(x, y, w, h)
	}
	return x, y, true
}

func (v *vtEmulator) fallbackCursorBlock(cursorX, cursorY, w, h int) (x, y int, ok bool) {
	if cursorX != 0 {
		return 0, 0, false
	}

	foundX, foundY := -1, -1
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			if !v.isCursorBlock(xx, yy, w) {
				continue
			}
			// Claude Code sometimes leaves the reported cursor at column 0 on a
			// later status/footer row while still drawing its real prompt cursor
			// as an isolated reverse-video space above. Only trust this fallback
			// when there is a single such candidate above the reported cursor.
			if yy >= cursorY {
				return 0, 0, false
			}
			if foundX != -1 {
				return 0, 0, false
			}
			foundX, foundY = xx, yy
		}
	}
	if foundX == -1 {
		return 0, 0, false
	}
	return foundX, foundY, true
}

func (v *vtEmulator) RenderWithoutCursorBlock() string {
	x, y, ok := v.currentCursorBlock()
	if !ok {
		return v.emu.Render()
	}

	cell := v.emu.CellAt(x, y)
	saved := *cell
	modified := cell.Clone()
	modified.Style.Attrs &^= uv.AttrReverse
	v.emu.SetCell(x, y, modified)
	rendered := v.emu.Render()
	v.emu.SetCell(x, y, &saved)
	return rendered
}

func (v *vtEmulator) HasCursorBlock() bool {
	_, _, ok := v.currentCursorBlock()
	return ok
}

func (v *vtEmulator) CursorBlockPosition() (col, row int, ok bool) {
	return v.currentCursorBlock()
}
