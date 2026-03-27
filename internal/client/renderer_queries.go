package client

import (
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

type paneInteractionSnapshot struct {
	Width         int
	Height        int
	CursorCol     int
	CursorRow     int
	CursorHidden  bool
	AltScreen     bool
	MouseProtocol mux.MouseProtocol
}

type paneBufferLine struct {
	text  string
	cells []render.ScreenCell
}

type paneBufferSnapshot struct {
	width        int
	height       int
	cursorCol    int
	cursorRow    int
	cursorHidden bool
	scrollback   []paneBufferLine
	screen       []paneBufferLine
}

func (p paneBufferSnapshot) Size() (width, height int) {
	return p.width, p.height
}

func (p paneBufferSnapshot) ScrollbackLen() int {
	return len(p.scrollback)
}

func (p paneBufferSnapshot) ScrollbackLineText(y int) string {
	if y < 0 || y >= len(p.scrollback) {
		return ""
	}
	return p.scrollback[y].text
}

func (p paneBufferSnapshot) ScreenLineText(y int) string {
	if y < 0 || y >= len(p.screen) {
		return ""
	}
	return p.screen[y].text
}

func (p paneBufferSnapshot) ScrollbackCellAt(col, row int) render.ScreenCell {
	return paneBufferLineCell(p.scrollback, row, col)
}

func (p paneBufferSnapshot) ScreenCellAt(col, row int) render.ScreenCell {
	return paneBufferLineCell(p.screen, row, col)
}

func paneBufferLineCell(lines []paneBufferLine, row, col int) render.ScreenCell {
	if row < 0 || row >= len(lines) {
		return render.ScreenCell{Char: " ", Width: 1}
	}
	line := lines[row]
	if len(line.cells) != 0 {
		if col >= 0 && col < len(line.cells) {
			return line.cells[col]
		}
		return render.ScreenCell{Char: " ", Width: 1}
	}
	return plainTextHistoryCell(line.text, col)
}

func plainTextHistoryCell(line string, col int) render.ScreenCell {
	runes := []rune(line)
	if col < 0 || col >= len(runes) {
		return render.ScreenCell{Char: " ", Width: 1}
	}
	return render.ScreenCell{Char: string(runes[col]), Width: 1}
}

func capturePaneBufferSnapshot(emu mux.TerminalEmulator, baseHistory []string, scrollbackLines int) paneBufferSnapshot {
	width, height := emu.Size()
	cursorCol, cursorRow := emu.CursorPosition()
	scrollbackLen := emu.ScrollbackLen()
	baseStart, liveStart := trimPaneBufferStarts(len(baseHistory), scrollbackLen, scrollbackLines)

	scrollback := make([]paneBufferLine, 0, len(baseHistory)-baseStart+scrollbackLen-liveStart)
	for _, line := range baseHistory[baseStart:] {
		scrollback = append(scrollback, paneBufferLine{text: line})
	}
	for row := liveStart; row < scrollbackLen; row++ {
		scrollback = append(scrollback, paneBufferLine{
			text:  emu.ScrollbackLineText(row),
			cells: captureScrollbackCells(emu, row, width),
		})
	}

	screen := make([]paneBufferLine, 0, height)
	for row := 0; row < height; row++ {
		screen = append(screen, paneBufferLine{
			text:  emu.ScreenLineText(row),
			cells: captureScreenCells(emu, row, width),
		})
	}

	return paneBufferSnapshot{
		width:        width,
		height:       height,
		cursorCol:    cursorCol,
		cursorRow:    cursorRow,
		cursorHidden: emu.CursorHidden(),
		scrollback:   scrollback,
		screen:       screen,
	}
}

func trimPaneBufferStarts(baseLen, liveLen, limit int) (baseStart, liveStart int) {
	if limit <= 0 {
		limit = mux.DefaultScrollbackLines
	}
	total := baseLen + liveLen
	if total <= limit {
		return 0, 0
	}

	drop := total - limit
	if drop >= baseLen {
		return baseLen, drop - baseLen
	}
	return drop, 0
}

func captureScrollbackCells(emu mux.TerminalEmulator, row, width int) []render.ScreenCell {
	cells := make([]render.ScreenCell, width)
	for col := 0; col < width; col++ {
		cells[col] = render.CellFromUV(emu.ScrollbackCellAt(col, row))
	}
	return cells
}

func captureScreenCells(emu mux.TerminalEmulator, row, width int) []render.ScreenCell {
	cells := make([]render.ScreenCell, width)
	for col := 0; col < width; col++ {
		cells[col] = render.CellFromUV(emu.CellAt(col, row))
	}
	return cells
}

func (r *Renderer) PaneSize(paneID uint32) (width, height int, ok bool) {
	r.withActor(func(st *rendererActorState) {
		emu, exists := st.emulators[paneID]
		if !exists {
			return
		}
		width, height = emu.Size()
		ok = true
	})
	return width, height, ok
}

func (r *Renderer) PaneInteractionSnapshot(paneID uint32) (paneInteractionSnapshot, bool) {
	snap := paneInteractionSnapshot{}
	ok := false
	r.withActor(func(st *rendererActorState) {
		emu, exists := st.emulators[paneID]
		if !exists {
			return
		}
		width, height := emu.Size()
		cursorCol, cursorRow := emu.CursorPosition()
		snap = paneInteractionSnapshot{
			Width:         width,
			Height:        height,
			CursorCol:     cursorCol,
			CursorRow:     cursorRow,
			CursorHidden:  emu.CursorHidden(),
			AltScreen:     emu.IsAltScreen(),
			MouseProtocol: emu.MouseProtocol(),
		}
		ok = true
	})
	return snap, ok
}

func (r *Renderer) EncodeMouse(paneID uint32, ev mouse.Event, x, y int) []byte {
	return withRendererActorValue(r, func(st *rendererActorState) []byte {
		emu, ok := st.emulators[paneID]
		if !ok {
			return nil
		}
		data := emu.EncodeMouse(ev, x, y)
		if len(data) == 0 {
			return nil
		}
		return append([]byte(nil), data...)
	})
}

func (r *Renderer) PaneBufferSnapshot(paneID uint32, baseHistory []string) (paneBufferSnapshot, bool) {
	snap := paneBufferSnapshot{}
	ok := false
	r.withActor(func(st *rendererActorState) {
		emu, exists := st.emulators[paneID]
		if !exists {
			return
		}
		snap = capturePaneBufferSnapshot(emu, append([]string(nil), baseHistory...), st.snapshot.scrollbackLines)
		ok = true
	})
	return snap, ok
}
