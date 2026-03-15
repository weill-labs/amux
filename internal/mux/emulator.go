package mux

import (
	"fmt"
	"sync"
	"sync/atomic"

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
