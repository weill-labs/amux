package client

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

// stripCursorBlock clears reverse-video on isolated cursor block cells
// so inactive panes don't show stray cursors.
func stripCursorBlock(sc *render.ScreenCell, emu mux.TerminalEmulator, x, y int) {
	if sc.Style.Attrs&uv.AttrReverse == 0 {
		return
	}
	if sc.Char != " " {
		return
	}
	cursorX, cursorY := emu.CursorPosition()
	if x != cursorX || y != cursorY {
		return
	}
	w, _ := emu.Size()
	if x > 0 {
		if left := emu.CellAt(x-1, y); left != nil && left.Style.Attrs&uv.AttrReverse != 0 {
			return
		}
	}
	if x < w-1 {
		if right := emu.CellAt(x+1, y); right != nil && right.Style.Attrs&uv.AttrReverse != 0 {
			return
		}
	}
	sc.Style.Attrs &^= uv.AttrReverse
}
