package mux

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/weill-labs/amux/internal/mouse"
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

	// ScreenLineText returns the plain text of screen line y (0=top row).
	// Continuation cells (Width==0) are skipped, trailing spaces trimmed.
	ScreenLineText(y int) string

	// ScreenContains returns true if any screen line contains substr.
	ScreenContains(substr string) bool

	// CellAt returns the raw cell at (col, row). Returns nil for out-of-bounds.
	CellAt(col, row int) *uv.Cell

	// IsAltScreen reports whether the pane is currently in alternate-screen mode.
	IsAltScreen() bool

	// MouseProtocol reports the pane's current application mouse-tracking mode.
	MouseProtocol() MouseProtocol

	// EncodeMouse returns the pane-local mouse escape sequence for the event.
	// Returns nil when the pane is not accepting this mouse event.
	EncodeMouse(ev mouse.Event, x, y int) []byte
}

// MouseTrackingMode is the pane's current application mouse-tracking mode.
type MouseTrackingMode int

const (
	MouseTrackingNone MouseTrackingMode = iota
	MouseTrackingStandard
	MouseTrackingButton
	MouseTrackingAny
)

// MouseProtocol describes how a pane wants mouse events encoded.
type MouseProtocol struct {
	Tracking MouseTrackingMode
	SGR      bool
}

// Enabled reports whether the pane currently accepts mouse events.
func (p MouseProtocol) Enabled() bool {
	return p.Tracking != MouseTrackingNone
}

// vtEmulator wraps charmbracelet/x/vt.SafeEmulator.
type vtEmulator struct {
	emu          *vt.SafeEmulator
	w            int
	h            int
	mu           sync.Mutex
	cursorHidden atomic.Bool
	altScreen    bool
	mouseModes   uint8
	mouseSGR     bool
}

const (
	mouseModeStandard uint8 = 1 << iota
	mouseModeButton
	mouseModeAny
)

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
		AltScreen: func(on bool) {
			v.mu.Lock()
			v.altScreen = on
			v.mu.Unlock()
		},
		CursorVisibility: func(visible bool) {
			v.cursorHidden.Store(!visible)
		},
		EnableMode: func(mode ansi.Mode) {
			v.setMouseMode(mode, true)
		},
		DisableMode: func(mode ansi.Mode) {
			v.setMouseMode(mode, false)
		},
	})
	return v
}

func (v *vtEmulator) setMouseMode(mode ansi.Mode, enabled bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	switch mode {
	case ansi.ModeMouseNormal:
		if enabled {
			v.mouseModes |= mouseModeStandard
		} else {
			v.mouseModes &^= mouseModeStandard
		}
	case ansi.ModeMouseButtonEvent:
		if enabled {
			v.mouseModes |= mouseModeButton
		} else {
			v.mouseModes &^= mouseModeButton
		}
	case ansi.ModeMouseAnyEvent:
		if enabled {
			v.mouseModes |= mouseModeAny
		} else {
			v.mouseModes &^= mouseModeAny
		}
	case ansi.ModeMouseExtSgr:
		v.mouseSGR = enabled
	}
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

// screenLineTextInner extracts plain text from screen row y across w columns.
// Skips continuation cells (Width==0) and trims trailing spaces.
func (v *vtEmulator) screenLineTextInner(w, y int) string {
	var buf strings.Builder
	buf.Grow(w)
	for x := 0; x < w; x++ {
		cell := v.emu.CellAt(x, y)
		if cell == nil {
			buf.WriteByte(' ')
			continue
		}
		if cell.Width == 0 {
			continue
		}
		if cell.Content == "" {
			buf.WriteByte(' ')
		} else {
			buf.WriteString(cell.Content)
		}
	}
	return strings.TrimRight(buf.String(), " ")
}

func (v *vtEmulator) ScreenLineText(y int) string {
	v.mu.Lock()
	w := v.w
	v.mu.Unlock()
	return v.screenLineTextInner(w, y)
}

func (v *vtEmulator) CellAt(col, row int) *uv.Cell {
	return v.emu.CellAt(col, row)
}

func (v *vtEmulator) IsAltScreen() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.altScreen
}

func (v *vtEmulator) MouseProtocol() MouseProtocol {
	v.mu.Lock()
	defer v.mu.Unlock()

	proto := MouseProtocol{SGR: v.mouseSGR}
	switch {
	case v.mouseModes&mouseModeAny != 0:
		proto.Tracking = MouseTrackingAny
	case v.mouseModes&mouseModeButton != 0:
		proto.Tracking = MouseTrackingButton
	case v.mouseModes&mouseModeStandard != 0:
		proto.Tracking = MouseTrackingStandard
	default:
		proto.Tracking = MouseTrackingNone
	}
	return proto
}

func (v *vtEmulator) EncodeMouse(ev mouse.Event, x, y int) []byte {
	proto := v.MouseProtocol()
	if !proto.Enabled() {
		return nil
	}
	if x < 0 || y < 0 {
		return nil
	}

	switch ev.Action {
	case mouse.Motion:
		if proto.Tracking != MouseTrackingButton && proto.Tracking != MouseTrackingAny {
			return nil
		}
	case mouse.Release:
		if proto.Tracking == MouseTrackingNone {
			return nil
		}
	}

	btn, ok := encodeMouseButton(ev.Button)
	if !ok {
		return nil
	}
	code := ansi.EncodeMouseButton(btn, ev.Action == mouse.Motion, ev.Shift, ev.Alt, ev.Ctrl)
	if code == 0xff {
		return nil
	}

	if proto.SGR {
		return []byte(ansi.MouseSgr(code, x, y, ev.Action == mouse.Release))
	}
	return []byte(ansi.MouseX10(code, x, y))
}

func encodeMouseButton(btn mouse.Button) (ansi.MouseButton, bool) {
	switch btn {
	case mouse.ButtonLeft:
		return ansi.MouseLeft, true
	case mouse.ButtonMiddle:
		return ansi.MouseMiddle, true
	case mouse.ButtonRight:
		return ansi.MouseRight, true
	case mouse.ButtonNone:
		return ansi.MouseNone, true
	case mouse.ScrollUp:
		return ansi.MouseWheelUp, true
	case mouse.ScrollDown:
		return ansi.MouseWheelDown, true
	case mouse.ScrollLeft:
		return ansi.MouseWheelLeft, true
	case mouse.ScrollRight:
		return ansi.MouseWheelRight, true
	default:
		return 0, false
	}
}

func (v *vtEmulator) ScreenContains(substr string) bool {
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()
	// Join all lines without separators — terminal output is a continuous
	// stream and column-boundary wraps are visual, not logical.
	var buf strings.Builder
	buf.Grow(w * h)
	for y := 0; y < h; y++ {
		buf.WriteString(v.screenLineTextInner(w, y))
	}
	return strings.Contains(buf.String(), substr)
}

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
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()

	x, y = v.CursorPosition()
	if x < 0 || y < 0 || x >= w || y >= h {
		return 0, 0, false
	}
	if !v.isCursorBlock(x, y, w) {
		return 0, 0, false
	}
	return x, y, true
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

// EmulatorContentLines returns plain-text screen lines from an emulator
// by reading the cell grid directly (no ANSI round-trip).
func EmulatorContentLines(emu TerminalEmulator) []string {
	_, rows := emu.Size()
	result := make([]string, rows)
	for y := 0; y < rows; y++ {
		result[y] = emu.ScreenLineText(y)
	}
	return result
}

// RenderWithCursor returns the emulator's screen using explicit cursor
// positioning per row, followed by a final cursor position escape.
// Using CUP sequences per row avoids width-dependent line wrapping that
// causes garbling when wide Unicode characters (block elements, etc.)
// have different widths across emulator instances.
func RenderWithCursor(emu TerminalEmulator) string {
	rendered := emu.Render()
	lines := strings.Split(rendered, "\n")

	var buf strings.Builder
	for i, line := range lines {
		// Position cursor at start of each row (CUP is 1-indexed)
		buf.WriteString(fmt.Sprintf("\033[%d;1H", i+1))
		buf.WriteString(line)
	}

	col, row := emu.CursorPosition()
	buf.WriteString(fmt.Sprintf("\033[%d;%dH", row+1, col+1))
	return buf.String()
}
