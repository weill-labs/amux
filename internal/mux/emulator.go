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

	// Close stops the emulator and releases any background readers.
	Close() error

	// Render returns the full screen as an ANSI-formatted string.
	Render() string

	// Resize changes the emulator's terminal dimensions.
	Resize(width, height int)

	// Size returns the current width and height.
	Size() (width, height int)

	// Reset clears retained emulator state and restores terminal defaults.
	Reset()

	// CursorPosition returns cursor column and row (0-indexed).
	CursorPosition() (col, row int)

	// CursorHidden returns true if the cursor is hidden.
	CursorHidden() bool

	// ScrollbackLen returns the number of lines in the scrollback buffer.
	ScrollbackLen() int

	// ScrollbackLineText returns the plain text of scrollback line y (0=oldest).
	ScrollbackLineText(y int) string

	// ScrollbackCellAt returns the raw cell at (col, row) in retained
	// scrollback (0=oldest row). Returns nil for out-of-bounds.
	ScrollbackCellAt(col, row int) *uv.Cell

	// ScrollbackSourceWidth returns the pane width at which the scrollback row
	// was originally wrapped. Returns zero when unknown.
	ScrollbackSourceWidth(row int) int

	// RenderWithoutCursorBlock renders the screen with the cursor cell's
	// reverse-video attribute cleared. Used for inactive pane rendering so
	// app-drawn block cursors don't appear in unfocused panes.
	RenderWithoutCursorBlock() string

	// HasCursorBlock returns true if the screen contains an isolated
	// reverse-video space cell (an app-rendered block cursor).
	HasCursorBlock() bool

	// CursorBlockPosition returns the app-drawn block cursor cell position
	// when one is present. The boolean is false when no isolated block cursor
	// can be identified.
	CursorBlockPosition() (col, row int, ok bool)

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

// DefaultScrollbackLines is the retained history limit used by amux for pane
// scrollback on both server and client emulators.
const DefaultScrollbackLines = 10000

func effectiveScrollbackLines(scrollbackLines int) int {
	if scrollbackLines <= 0 {
		return DefaultScrollbackLines
	}
	return scrollbackLines
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
	emu              *vt.SafeEmulator
	w                atomic.Int32
	h                atomic.Int32
	cursorHidden     atomic.Bool
	altScreen        atomic.Bool
	mouseFlags       atomic.Uint32
	scrollbackMu     sync.Mutex
	scrollbackWidths []int
	scrollbackLimit  int
}

const (
	mouseModeStandard uint32 = 1 << iota
	mouseModeButton
	mouseModeAny
	mouseModeSGR
)

// NewVTEmulatorWithScrollback creates a terminal emulator with an explicit
// retained scrollback limit.
func NewVTEmulatorWithScrollback(width, height, scrollbackLines int) TerminalEmulator {
	limit := effectiveScrollbackLines(scrollbackLines)
	v := &vtEmulator{
		emu:             vt.NewSafeEmulator(width, height),
		scrollbackLimit: limit,
	}
	v.w.Store(int32(width))
	v.h.Store(int32(height))
	v.emu.SetScrollbackSize(limit)
	// Track cursor visibility changes so CursorHidden() reflects the
	// application's actual cursor state (e.g. \033[?25l / \033[?25h).
	v.emu.SetCallbacks(vt.Callbacks{
		AltScreen: func(on bool) {
			v.altScreen.Store(on)
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
		ScrollbackPush: func(count, width int) {
			v.recordScrollbackPush(count, width)
		},
		ScrollbackClear: func() {
			v.clearScrollbackWidths()
		},
	})
	return v
}

func (v *vtEmulator) recordScrollbackPush(count, width int) {
	if count <= 0 || width <= 0 {
		return
	}
	v.scrollbackMu.Lock()
	defer v.scrollbackMu.Unlock()
	for range count {
		v.scrollbackWidths = append(v.scrollbackWidths, width)
	}
	if overflow := len(v.scrollbackWidths) - v.scrollbackLimit; overflow > 0 {
		copy(v.scrollbackWidths, v.scrollbackWidths[overflow:])
		v.scrollbackWidths = v.scrollbackWidths[:len(v.scrollbackWidths)-overflow]
	}
}

func (v *vtEmulator) clearScrollbackWidths() {
	v.scrollbackMu.Lock()
	defer v.scrollbackMu.Unlock()
	v.scrollbackWidths = v.scrollbackWidths[:0]
}

func (v *vtEmulator) setMouseMode(mode ansi.Mode, enabled bool) {
	var bit uint32
	switch mode {
	case ansi.ModeMouseNormal:
		bit = mouseModeStandard
	case ansi.ModeMouseButtonEvent:
		bit = mouseModeButton
	case ansi.ModeMouseAnyEvent:
		bit = mouseModeAny
	case ansi.ModeMouseExtSgr:
		bit = mouseModeSGR
	default:
		return
	}
	for {
		current := v.mouseFlags.Load()
		next := current
		if enabled {
			next |= bit
		} else {
			next &^= bit
		}
		if v.mouseFlags.CompareAndSwap(current, next) {
			return
		}
	}
}

func (v *vtEmulator) Write(data []byte) (int, error) {
	return v.emu.Write(data)
}

func (v *vtEmulator) Read(p []byte) (int, error) {
	return v.emu.Read(p)
}

func (v *vtEmulator) Close() error {
	return v.emu.Close()
}

func (v *vtEmulator) Render() string {
	return v.emu.Render()
}

func (v *vtEmulator) Resize(width, height int) {
	v.w.Store(int32(width))
	v.h.Store(int32(height))
	v.emu.Resize(width, height)
}

func (v *vtEmulator) Size() (int, int) {
	return int(v.w.Load()), int(v.h.Load())
}

func (v *vtEmulator) Reset() {
	v.emu.ClearScrollback()
	_, _ = v.emu.Write([]byte("\x1bc"))
	v.cursorHidden.Store(false)
	v.altScreen.Store(false)
	v.mouseFlags.Store(0)
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

func (v *vtEmulator) ScrollbackCellAt(col, row int) *uv.Cell {
	sb := v.emu.Scrollback()
	if sb == nil || row < 0 || row >= sb.Len() || col < 0 {
		return nil
	}
	line := sb.Line(row)
	if line == nil || col >= len(line) {
		return nil
	}
	cell := line[col]
	return &cell
}

func (v *vtEmulator) ScrollbackSourceWidth(row int) int {
	v.scrollbackMu.Lock()
	defer v.scrollbackMu.Unlock()
	if row < 0 || row >= len(v.scrollbackWidths) {
		return 0
	}
	return v.scrollbackWidths[row]
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
	w := int(v.w.Load())
	return v.screenLineTextInner(w, y)
}

func (v *vtEmulator) CellAt(col, row int) *uv.Cell {
	return v.emu.CellAt(col, row)
}

func (v *vtEmulator) IsAltScreen() bool {
	return v.altScreen.Load()
}

func (v *vtEmulator) MouseProtocol() MouseProtocol {
	flags := v.mouseFlags.Load()
	proto := MouseProtocol{SGR: flags&mouseModeSGR != 0}
	switch {
	case flags&mouseModeAny != 0:
		proto.Tracking = MouseTrackingAny
	case flags&mouseModeButton != 0:
		proto.Tracking = MouseTrackingButton
	case flags&mouseModeStandard != 0:
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
	w, h := int(v.w.Load()), int(v.h.Load())
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

// NewVTEmulatorWithDrain creates a terminal emulator that automatically
// drains its own response pipe. Suitable for client-side emulators that
// don't have a PTY to forward responses to.
func NewVTEmulatorWithDrain(width, height int) TerminalEmulator {
	return NewVTEmulatorWithDrainAndScrollback(width, height, DefaultScrollbackLines)
}

// NewVTEmulatorWithDrainAndScrollback creates a self-draining emulator with
// an explicit retained scrollback limit.
func NewVTEmulatorWithDrainAndScrollback(width, height, scrollbackLines int) TerminalEmulator {
	emu := NewVTEmulatorWithScrollback(width, height, scrollbackLines)
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

// EmulatorScrollbackLines returns retained plain-text scrollback lines from
// oldest to newest.
func EmulatorScrollbackLines(emu TerminalEmulator) []string {
	lines := make([]string, emu.ScrollbackLen())
	for y := range len(lines) {
		lines[y] = emu.ScrollbackLineText(y)
	}
	return lines
}

// EmulatorScrollbackHistoryLines returns retained scrollback rows with their
// tracked source widths.
func EmulatorScrollbackHistoryLines(emu TerminalEmulator) []CaptureHistoryLine {
	lines := make([]CaptureHistoryLine, emu.ScrollbackLen())
	for y := range len(lines) {
		sourceWidth := emu.ScrollbackSourceWidth(y)
		lines[y] = CaptureHistoryLine{
			Text:        emu.ScrollbackLineText(y),
			SourceWidth: sourceWidth,
			Filled:      lineUsesFullWidth(sourceWidth, func(x int) *uv.Cell { return emu.ScrollbackCellAt(x, y) }),
		}
	}
	return lines
}

// EmulatorContentHistoryLines returns visible screen rows with the width they
// were wrapped at and whether they filled the available width.
func EmulatorContentHistoryLines(emu TerminalEmulator) []CaptureHistoryLine {
	width, rows := emu.Size()
	lines := make([]CaptureHistoryLine, rows)
	for y := 0; y < rows; y++ {
		lines[y] = CaptureHistoryLine{
			Text:        emu.ScreenLineText(y),
			SourceWidth: width,
			Filled:      lineUsesFullWidth(width, func(x int) *uv.Cell { return emu.CellAt(x, y) }),
		}
	}
	return lines
}

func lineUsesFullWidth(width int, cellAt func(int) *uv.Cell) bool {
	if width <= 0 {
		return false
	}
	cell := cellAt(width - 1)
	if cell == nil {
		return false
	}
	if cell.Width == 0 {
		return true
	}
	return cell.Content != "" || (!cell.IsZero() && !cell.Equal(&uv.EmptyCell))
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
