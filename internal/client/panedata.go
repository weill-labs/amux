package client

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// Ensure PaneData satisfies render.PaneData at compile time.
var _ render.PaneData = (*PaneData)(nil)

// PaneData adapts an emulator + snapshot metadata for the render.PaneData
// interface. This is the basic adapter without copy mode support — the main
// package wraps this with copy mode overlay.
type PaneData struct {
	Emu  mux.TerminalEmulator
	Info proto.PaneSnapshot
}

func (p *PaneData) RenderScreen(active bool) string {
	if !active {
		return p.Emu.RenderWithoutCursorBlock()
	}
	return p.Emu.Render()
}

func (p *PaneData) CellAt(col, row int, active bool) render.ScreenCell {
	cell := p.Emu.CellAt(col, row)
	sc := render.CellFromUV(cell)
	if !active {
		stripCursorBlock(&sc, p.Emu, col, row)
	}
	return sc
}

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

func (p *PaneData) CursorPos() (col, row int) { return p.Emu.CursorPosition() }
func (p *PaneData) CursorHidden() bool        { return p.Emu.CursorHidden() }
func (p *PaneData) HasCursorBlock() bool      { return p.Emu.HasCursorBlock() }
func (p *PaneData) ID() uint32                { return p.Info.ID }
func (p *PaneData) Name() string              { return p.Info.Name }
func (p *PaneData) Host() string              { return p.Info.Host }
func (p *PaneData) Task() string              { return p.Info.Task }
func (p *PaneData) Color() string             { return p.Info.Color }
func (p *PaneData) Minimized() bool           { return p.Info.Minimized }
func (p *PaneData) Idle() bool                { return p.Info.Idle }
func (p *PaneData) ConnStatus() string        { return p.Info.ConnStatus }
func (p *PaneData) InCopyMode() bool          { return false }
func (p *PaneData) CopyModeSearch() string    { return "" }
