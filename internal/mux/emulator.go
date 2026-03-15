package mux

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// TerminalEmulator abstracts a virtual terminal emulator for pane rendering.
// Phase 1 uses Render() for reattach screen reconstruction.
// Phase 2+ will use cell-level access for multi-pane compositing.
type TerminalEmulator interface {
	// Write feeds PTY output data into the emulator.
	Write(data []byte) (int, error)

	// Read returns terminal responses (DA queries, cursor reports, etc.).
	// These must be drained and written back to the PTY so the shell
	// receives the expected replies. Go's io.Pipe is unbuffered — if
	// Read is not called, Write blocks on the first response.
	Read(p []byte) (int, error)

	// Render returns the full screen as an ANSI-formatted string.
	Render() string

	// Resize changes the emulator's terminal dimensions.
	Resize(width, height int)

	// Size returns the current width and height.
	Size() (width, height int)

	// CursorPosition returns cursor column and row (0-indexed).
	CursorPosition() (col, row int)

	// CursorHidden returns true if the cursor is hidden.
	CursorHidden() bool

	// ScrollbackLen returns the number of lines in the scrollback buffer.
	ScrollbackLen() int

	// ScrollbackLineText returns the plain text of scrollback line y (0=oldest).
	ScrollbackLineText(y int) string

	// RenderWithoutCursorBlock renders the screen with the cursor cell's
	// reverse-video attribute cleared. Used for inactive pane rendering so
	// app-drawn block cursors don't appear in unfocused panes.
	RenderWithoutCursorBlock() string

	// HasCursorBlock returns true if the screen contains an isolated
	// reverse-video space cell (an app-rendered block cursor).
	HasCursorBlock() bool
}

// vtEmulator wraps charmbracelet/x/vt.SafeEmulator.
type vtEmulator struct {
	emu          *vt.SafeEmulator
	w            int
	h            int
	mu           sync.Mutex
	cursorHidden atomic.Bool
}

// NewVTEmulator creates a new terminal emulator with the given dimensions.
func NewVTEmulator(width, height int) TerminalEmulator {
	v := &vtEmulator{
		emu: vt.NewSafeEmulator(width, height),
		w:   width,
		h:   height,
	}
	// Track cursor visibility changes so CursorHidden() reflects the
	// application's actual cursor state (e.g. \033[?25l / \033[?25h).
	v.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) {
			v.cursorHidden.Store(!visible)
		},
	})
	return v
}

func (v *vtEmulator) Write(data []byte) (int, error) {
	return v.emu.Write(data)
}

func (v *vtEmulator) Read(p []byte) (int, error) {
	return v.emu.Read(p)
}

func (v *vtEmulator) Render() string {
	return v.emu.Render()
}

func (v *vtEmulator) Resize(width, height int) {
	v.mu.Lock()
	v.w = width
	v.h = height
	v.mu.Unlock()
	v.emu.Resize(width, height)
}

func (v *vtEmulator) Size() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.w, v.h
}

func (v *vtEmulator) CursorPosition() (col, row int) {
	pos := v.emu.CursorPosition()
	return pos.X, pos.Y
}

func (v *vtEmulator) CursorHidden() bool {
	return v.cursorHidden.Load()
}

func (v *vtEmulator) ScrollbackLen() int {
	return v.emu.ScrollbackLen()
}

func (v *vtEmulator) ScrollbackLineText(y int) string {
	sb := v.emu.Scrollback()
	if sb == nil || y < 0 || y >= sb.Len() {
		return ""
	}
	line := sb.Line(y)
	if line == nil {
		return ""
	}
	var buf strings.Builder
	for _, cell := range line {
		if cell.Content != "" {
			buf.WriteString(cell.Content)
		}
	}
	return buf.String()
}

func (v *vtEmulator) RenderWithoutCursorBlock() string {
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()

	// Find isolated reverse-video space cells (cursor blocks).
	// A cursor block is a single reverse-video space not adjacent to other
	// reverse-video cells. This distinguishes cursor indicators from
	// legitimate reverse-video content like selections or highlights.
	type savedCell struct {
		x, y int
		cell uv.Cell
	}
	var saved []savedCell

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := v.emu.CellAt(x, y)
			if cell == nil || cell.Style.Attrs&uv.AttrReverse == 0 {
				continue
			}
			if cell.Content != " " && cell.Content != "" {
				continue
			}
			leftReverse := false
			if x > 0 {
				if left := v.emu.CellAt(x-1, y); left != nil {
					leftReverse = left.Style.Attrs&uv.AttrReverse != 0
				}
			}
			rightReverse := false
			if x < w-1 {
				if right := v.emu.CellAt(x+1, y); right != nil {
					rightReverse = right.Style.Attrs&uv.AttrReverse != 0
				}
			}
			if leftReverse || rightReverse {
				continue
			}
			saved = append(saved, savedCell{x, y, *cell})
			modified := cell.Clone()
			modified.Style.Attrs &^= uv.AttrReverse
			v.emu.SetCell(x, y, modified)
		}
	}

	if len(saved) == 0 {
		return v.emu.Render()
	}

	rendered := v.emu.Render()
	for _, s := range saved {
		c := s.cell
		v.emu.SetCell(s.x, s.y, &c)
	}
	return rendered
}

func (v *vtEmulator) HasCursorBlock() bool {
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := v.emu.CellAt(x, y)
			if cell == nil || cell.Style.Attrs&uv.AttrReverse == 0 {
				continue
			}
			if cell.Content != " " && cell.Content != "" {
				continue
			}
			leftReverse := false
			if x > 0 {
				if left := v.emu.CellAt(x-1, y); left != nil {
					leftReverse = left.Style.Attrs&uv.AttrReverse != 0
				}
			}
			rightReverse := false
			if x < w-1 {
				if right := v.emu.CellAt(x+1, y); right != nil {
					rightReverse = right.Style.Attrs&uv.AttrReverse != 0
				}
			}
			if !leftReverse && !rightReverse {
				return true
			}
		}
	}
	return false
}

// NewVTEmulatorWithDrain creates a terminal emulator that automatically
// drains its own response pipe. Suitable for client-side emulators that
// don't have a PTY to forward responses to.
func NewVTEmulatorWithDrain(width, height int) TerminalEmulator {
	emu := NewVTEmulator(width, height)
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := emu.Read(buf)
			if err != nil {
				return
			}
		}
	}()
	return emu
}

// RenderWithCursor returns the emulator's rendered screen followed by
// a cursor positioning escape sequence.
func RenderWithCursor(emu TerminalEmulator) string {
	rendered := emu.Render()
	col, row := emu.CursorPosition()
	// ANSI cursor position is 1-indexed
	return rendered + fmt.Sprintf("\033[%d;%dH", row+1, col+1)
}
