package mux

import (
	"io"
	"os"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

func newPaneEmulator(cols, rows, scrollbackLines int) TerminalEmulator {
	return NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)
}

// wireScrollbackCallbacks connects the emulator's scrollback push/clear
// callbacks to the pane's atomic width tracking.
func wireScrollbackCallbacks(p *Pane) {
	if vte, ok := p.emulator.(*vtEmulator); ok {
		vte.scrollbackPushFn = p.recordScrollbackPush
		vte.scrollbackClearFn = p.clearScrollbackWidths
	}
}

// ReplayScreen feeds screen data into the emulator to restore visual state.
func (p *Pane) ReplayScreen(data string) {
	p.withActor(func() {
		_, _ = p.emulator.Write([]byte(data))
	})
}

// drainResponses reads terminal responses from the emulator (DA replies,
// cursor position reports, etc.) and writes them back to the PTY so the
// shell receives them. Without this, the emulator's unbuffered io.Pipe
// blocks on the first response, deadlocking the server.
func (p *Pane) drainResponses(emulator TerminalEmulator, ptmx *os.File, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	defer closeTerminalResponsePipe(emulator)
	buf := make([]byte, 1024)
	for {
		n, err := emulator.Read(buf)
		if n > 0 {
			if _, writeErr := ptmx.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// applyOutput feeds PTY bytes into the retained emulator state and returns the
// monotonically increasing output sequence included in that state.
func (p *Pane) applyOutput(data []byte) uint64 {
	return paneActorValue(p, func() uint64 {
		_, _ = p.emulator.Write(data)
		return p.outputSeq.Add(1)
	})
}

// EmulatorSize returns the current emulator dimensions.
func (p *Pane) EmulatorSize() (cols, rows int) {
	p.withActor(func() {
		if p.emulator != nil {
			cols, rows = p.emulator.Size()
			return
		}
	})
	return cols, rows
}

// Resize changes the PTY and emulator dimensions.
func (p *Pane) Resize(cols, rows int) error {
	return paneActorValue(p, func() error {
		sizeChanged := true
		if p.emulator != nil {
			currentCols, currentRows := p.emulator.Size()
			sizeChanged = currentCols != cols || currentRows != rows
			p.emulator.Resize(cols, rows)
		}
		if sizeChanged {
			if err := p.resizePTY(cols, rows); err != nil {
				return err
			}
		}
		return nil
	})
}

// Render returns the current screen cell content as an ANSI string.
// Used by the compositor via PaneData.RenderScreen(). For the reattach
// snapshot (which needs cursor positioning embedded), use RenderScreen().
func (p *Pane) Render() string {
	return paneActorValue(p, func() string {
		return p.emulator.Render()
	})
}

// RenderScreen returns the screen state with a trailing cursor-position escape.
// Used when sending a reattach snapshot to a reconnecting client so the
// client-side emulator seeds the correct cursor position.
func (p *Pane) RenderScreen() string {
	return paneActorValue(p, func() string {
		return RenderWithCursor(p.emulator)
	})
}

// ScreenSnapshot returns the current visible screen plus the latest
// live-output sequence included in that state.
func (p *Pane) ScreenSnapshot() (screen string, seq uint64) {
	p.withActor(func() {
		screen = RenderWithCursor(p.emulator)
		seq = p.outputSeq.Load()
	})
	return screen, seq
}

// HistoryScreenSnapshot returns a consistent snapshot of retained scrollback,
// current screen, and the latest live-output sequence included in that state.
func (p *Pane) HistoryScreenSnapshot() (history []string, screen string, seq uint64) {
	p.withActor(func() {
		history = p.combinedScrollback(p.loadBaseHistory())
		screen = RenderWithCursor(p.emulator)
		seq = p.outputSeq.Load()
	})
	return history, screen, seq
}

func (p *Pane) styledHistorySnapshot() []proto.StyledLine {
	baseHistory := p.loadBaseHistory()
	liveHistory := EmulatorScrollbackStyledLines(p.emulator)
	baseStart, liveStart := trimScrollbackStarts(len(baseHistory), len(liveHistory), effectiveScrollbackLines(p.scrollbackLines))

	history := make([]proto.StyledLine, 0, len(baseHistory)-baseStart+len(liveHistory)-liveStart)
	for _, line := range baseHistory[baseStart:] {
		history = append(history, proto.StyledLine{Text: line})
	}
	return append(history, proto.CloneStyledLines(liveHistory[liveStart:])...)
}

// StyledHistorySnapshot returns retained history with frozen cell styles.
func (p *Pane) StyledHistorySnapshot() (history []proto.StyledLine) {
	p.withActor(func() {
		history = p.styledHistorySnapshot()
	})
	return history
}

// StyledHistoryScreenSnapshot returns retained history with frozen cell styles,
// current screen, and the latest live-output sequence included in that state.
func (p *Pane) StyledHistoryScreenSnapshot() (history []proto.StyledLine, screen string, seq uint64) {
	p.withActor(func() {
		history = p.styledHistorySnapshot()
		screen = RenderWithCursor(p.emulator)
		seq = p.outputSeq.Load()
	})
	return history, screen, seq
}

func (p *Pane) terminalSnapshot() PaneTerminalSnapshot {
	col, row := p.emulator.CursorPosition()
	return PaneTerminalSnapshot{
		Terminal:     p.emulator.TerminalState(),
		CursorCol:    col,
		CursorRow:    row,
		CursorHidden: p.emulator.CursorHidden(),
	}
}

// TerminalSnapshot returns a lightweight snapshot of the pane's cursor and
// non-text terminal metadata without allocating content/history slices.
func (p *Pane) TerminalSnapshot() PaneTerminalSnapshot {
	return paneActorValue(p, func() PaneTerminalSnapshot {
		return p.terminalSnapshot()
	})
}

// CaptureSnapshot returns a consistent plain-text snapshot of retained
// scrollback, visible screen content, and cursor state.
func (p *Pane) CaptureSnapshot() CaptureSnapshot {
	return paneActorValue(p, func() CaptureSnapshot {
		baseHistory := p.loadBaseHistory()
		liveHistory := EmulatorScrollbackHistoryLines(p.emulator, p.loadScrollbackWidths())
		baseHistory, liveHistory, history := p.captureScrollback(baseHistory, liveHistory)
		contentRows := EmulatorContentHistoryLines(p.emulator)
		terminal := p.terminalSnapshot()
		width, _ := p.emulator.Size()
		snap := CaptureSnapshot{
			BaseHistory:    append([]string(nil), baseHistory...),
			LiveHistory:    append([]CaptureHistoryLine(nil), liveHistory...),
			History:        history,
			ContentRows:    append([]CaptureHistoryLine(nil), contentRows...),
			Content:        captureHistoryLineText(contentRows),
			Terminal:       terminal.Terminal,
			Width:          width,
			CursorCol:      terminal.CursorCol,
			CursorRow:      terminal.CursorRow,
			CursorHidden:   terminal.CursorHidden,
			HasCursorBlock: false,
		}
		if blockCol, blockRow, ok := p.emulator.CursorBlockPosition(); ok {
			snap.CursorBlockCol = blockCol
			snap.CursorBlockRow = blockRow
			snap.HasCursorBlock = true
		}
		return snap
	})
}

func (p *Pane) captureScrollback(baseHistory []string, liveHistory []CaptureHistoryLine) ([]string, []CaptureHistoryLine, []string) {
	limit := effectiveScrollbackLines(p.scrollbackLines)
	baseStart, liveStart := trimScrollbackStarts(len(baseHistory), len(liveHistory), limit)
	if baseStart > 0 || liveStart > 0 {
		baseHistory = baseHistory[baseStart:]
		liveHistory = liveHistory[liveStart:]
	}

	history := make([]string, 0, len(baseHistory)+len(liveHistory))
	history = append(history, baseHistory...)
	for _, line := range liveHistory {
		history = append(history, line.Text)
	}
	return baseHistory, liveHistory, history
}

func trimScrollbackStarts(baseLen, liveLen, limit int) (baseStart, liveStart int) {
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

func captureHistoryLineText(lines []CaptureHistoryLine) []string {
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.Text
	}
	return text
}

// RenderWithoutCursorBlock returns the screen with the cursor cell's
// reverse-video attribute cleared, so inactive panes don't show a block cursor.
func (p *Pane) RenderWithoutCursorBlock() string {
	return paneActorValue(p, func() string {
		return p.emulator.RenderWithoutCursorBlock()
	})
}

// HasCursorBlock returns true if the pane contains an app-rendered block cursor.
func (p *Pane) HasCursorBlock() bool {
	return paneActorValue(p, func() bool {
		return p.emulator.HasCursorBlock()
	})
}

// CursorBlockPos returns the app-drawn block cursor position, if present.
func (p *Pane) CursorBlockPos() (col, row int, ok bool) {
	p.withActor(func() {
		col, row, ok = p.emulator.CursorBlockPosition()
	})
	return col, row, ok
}

// CursorPos returns the cursor position within this pane (0-indexed).
func (p *Pane) CursorPos() (col, row int) {
	p.withActor(func() {
		col, row = p.emulator.CursorPosition()
	})
	return col, row
}

// CursorHidden returns true if the application running in this pane has
// hidden the hardware cursor (e.g. via \033[?25l).
func (p *Pane) CursorHidden() bool {
	return paneActorValue(p, func() bool {
		return p.emulator.CursorHidden()
	})
}

// Output returns the last N lines of visible pane content from the emulator.
func (p *Pane) Output(lines int) string {
	return paneActorValue(p, func() string {
		_, rows := p.emulator.Size()
		result := make([]string, 0, lines)
		for y := rows - 1; y >= 0 && len(result) < lines; y-- {
			plain := p.emulator.ScreenLineText(y)
			if plain != "" {
				result = append([]string{plain}, result...)
			}
		}
		return strings.Join(result, "\n")
	})
}

// ContentLines returns all visible screen lines as a slice of plain text strings.
// Every row from 0 to height-1 is represented (len(result) == pane height).
// Lines are right-trimmed of trailing whitespace.
func (p *Pane) ContentLines() []string {
	return paneActorValue(p, func() []string {
		return EmulatorContentLines(p.emulator)
	})
}

// ScrollbackLines returns retained plain-text scrollback lines from oldest to
// newest.
func (p *Pane) ScrollbackLines() []string {
	return paneActorValue(p, func() []string {
		return p.combinedScrollback(p.loadBaseHistory())
	})
}

// OutputSeq reports the latest live-output sequence applied to the emulator.
func (p *Pane) OutputSeq() uint64 {
	return p.outputSeq.Load()
}

// SetRetainedHistory replaces the retained pre-attach/pre-reload history base
// for this pane. New live scrollback from the emulator is combined on top.
func (p *Pane) SetRetainedHistory(lines []string) {
	p.withActor(func() {
		limit := effectiveScrollbackLines(p.scrollbackLines)
		if len(lines) > limit {
			lines = lines[len(lines)-limit:]
		}
		p.baseHistory.Store(&paneBaseHistory{lines: append([]string(nil), lines...)})
	})
}

// ResetState clears retained pane history and resets the terminal emulator to a
// blank default screen without touching the underlying PTY or process.
func (p *Pane) ResetState() {
	p.withActor(func() {
		p.baseHistory.Store(&paneBaseHistory{})
		p.clearScrollbackWidths()
		if p.emulator != nil {
			p.emulator.Reset()
		}
	})
}

// ScreenContains returns true if substr appears in the pane's visible screen
// content, matching across soft-wrapped lines.
func (p *Pane) ScreenContains(substr string) bool {
	return paneActorValue(p, func() bool {
		return p.emulator.ScreenContains(substr)
	})
}

// drainResponsesDiscard reads and discards terminal responses from the
// emulator. Proxy panes have no PTY to forward responses to, but the
// emulator's pipe must be drained to prevent blocking.
func (p *Pane) drainResponsesDiscard(emulator TerminalEmulator, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	defer closeTerminalResponsePipe(emulator)
	buf := make([]byte, 1024)
	for {
		_, err := emulator.Read(buf)
		if err != nil {
			return
		}
	}
}

type terminalResponsePipeCloser interface {
	closeResponsePipe(error) error
}

func closeTerminalResponsePipe(emulator TerminalEmulator) {
	closer, ok := emulator.(terminalResponsePipeCloser)
	if !ok {
		return
	}
	_ = closer.closeResponsePipe(io.ErrClosedPipe)
}

// FeedOutput feeds remote PTY output into this proxy pane's local emulator
// and broadcasts it to connected clients. Called by the remote host connection
// when it receives pane output from the remote amux server.
func (p *Pane) FeedOutput(data []byte) {
	seq := p.applyOutput(data)
	if p.onOutput != nil {
		p.onOutput(p.ID, data, seq)
	}
}

func (p *Pane) combinedScrollback(baseHistory []string) []string {
	live := EmulatorScrollbackLines(p.emulator)
	limit := effectiveScrollbackLines(p.scrollbackLines)
	baseStart, liveStart := trimScrollbackStarts(len(baseHistory), len(live), limit)
	if baseStart == 0 && liveStart == 0 {
		total := len(baseHistory) + len(live)
		out := make([]string, 0, total)
		out = append(out, baseHistory...)
		out = append(out, live...)
		return out
	}

	out := make([]string, 0, limit)
	out = append(out, baseHistory[baseStart:]...)
	out = append(out, live[liveStart:]...)
	return out
}
