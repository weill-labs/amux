package mux

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// DefaultHost is the host value for locally-running panes.
const DefaultHost = "local"

// PaneNameFormat is the format string for auto-assigned pane names.
const PaneNameFormat = "pane-%d"

// PaneMeta holds amux metadata for a pane.
type PaneMeta struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	Task         string `json:"task,omitempty"`
	Remote       string `json:"remote,omitempty"`
	Color        string `json:"color"`
	Minimized    bool   `json:"minimized,omitempty"`
	RestoreH     int    `json:"restore_h,omitempty"`     // saved height before minimize
	MinimizedSeq uint64 `json:"minimized_seq,omitempty"` // monotonic counter for LIFO restore ordering
	Dormant      bool   `json:"dormant,omitempty"`       // pane is in Session.Panes but not in any window layout (e.g., SSH takeover host)
	Dir          string `json:"dir,omitempty"`           // working directory override for new shell
}

// Pane manages a PTY, its terminal emulator, and metadata.
type Pane struct {
	ID          uint32
	ActivePoint uint64 // monotonic counter — higher means more recently focused
	Meta        PaneMeta

	ptmx     *os.File
	cmd      *exec.Cmd
	process  *os.Process // set for restored panes (where cmd is nil)
	emulator TerminalEmulator

	outputSeq       atomic.Uint64
	snapshotSeq     atomic.Uint64
	baseHistory     atomic.Pointer[paneBaseHistory]
	scrollbackLines int

	// writeOverride, when non-nil, receives Write() calls instead of the PTY.
	// Used by proxy panes to route input over SSH to a remote amux server.
	writeOverride func([]byte) (int, error)

	closed         atomic.Bool
	drainStarted   bool
	onOutput       func(paneID uint32, data []byte, seq uint64)
	onExit         func(paneID uint32)
	onClipboard    func(paneID uint32, data []byte)
	onTakeover     func(paneID uint32, req TakeoverRequest)
	osc52Scanner   OSC52Scanner
	controlScanner AmuxControlScanner

	// Idle tracking (LAB-159)
	createdAt        time.Time
	lastBusySeenUnix atomic.Int64 // UnixNano; last time process tree showed busy
	idleSinceUnix    atomic.Int64 // UnixNano; when the current idle period began
}

type paneBaseHistory struct {
	lines []string
}

// CaptureSnapshot is a consistent plain-text snapshot of a pane's retained
// history, visible screen, and cursor state.
type CaptureSnapshot struct {
	History      []string
	Content      []string
	CursorCol    int
	CursorRow    int
	CursorHidden bool
}

// NewPaneWithScrollback creates a new pane running the user's shell but does
// NOT start the read/drain/wait goroutines. Call Start() after releasing any
// locks that the onOutput/onExit callbacks might need.
func NewPaneWithScrollback(id uint32, meta PaneMeta, cols, rows int, sessionName string, scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32)) (*Pane, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(),
		"TERM=amux",
		"AMUX_PANE=1",
		"AMUX_SESSION="+sessionName,
	)
	if meta.Dir != "" {
		cmd.Dir = meta.Dir
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, err
	}

	emu := NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)

	p := &Pane{
		ID:              id,
		Meta:            meta,
		ptmx:            ptmx,
		cmd:             cmd,
		emulator:        emu,
		onOutput:        onOutput,
		onExit:          onExit,
		createdAt:       time.Now(),
		scrollbackLines: effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	return p, nil
}

// RestorePaneWithScrollback creates a pane from inherited file descriptors
// after server reload using an explicit retained scrollback limit. It wraps an
// existing PTY master FD and finds the running shell process by PID. No new
// shell is spawned — the existing shell survives the exec. The drain goroutine
// starts immediately to prevent deadlock during screen replay.
func RestorePaneWithScrollback(id uint32, meta PaneMeta, ptmxFd, pid, cols, rows int, scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32)) (*Pane, error) {
	ptmx := os.NewFile(uintptr(ptmxFd), fmt.Sprintf("ptmx-%d", id))
	if ptmx == nil {
		return nil, fmt.Errorf("invalid FD %d for pane %d", ptmxFd, id)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("finding process %d for pane %d: %w", pid, id, err)
	}

	emu := NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)

	p := &Pane{
		ID:              id,
		Meta:            meta,
		ptmx:            ptmx,
		process:         proc,
		emulator:        emu,
		drainStarted:    true,
		onOutput:        onOutput,
		onExit:          onExit,
		createdAt:       time.Now(),
		scrollbackLines: effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})

	// Start drain immediately so screen replay doesn't deadlock
	// on the emulator's unbuffered response pipe.
	go p.drainResponses()

	return p, nil
}

// CreatedAt returns when this pane was created.
func (p *Pane) CreatedAt() time.Time {
	return p.createdAt
}

// SetCreatedAt overrides the creation time (used to restore from checkpoint).
func (p *Pane) SetCreatedAt(t time.Time) {
	p.createdAt = t
}

func (p *Pane) beginSnapshotMutation() {
	p.snapshotSeq.Add(1)
}

func (p *Pane) endSnapshotMutation() {
	p.snapshotSeq.Add(1)
}

func (p *Pane) waitForStableSnapshot() uint64 {
	for {
		seq := p.snapshotSeq.Load()
		if seq%2 == 0 {
			return seq
		}
		runtime.Gosched()
	}
}

func (p *Pane) loadBaseHistory() []string {
	base := p.baseHistory.Load()
	if base == nil {
		return nil
	}
	return base.lines
}

// PtmxFd returns the file descriptor number for the PTY master.
// Returns -1 for proxy panes (no PTY).
func (p *Pane) PtmxFd() int {
	if p.ptmx == nil {
		return -1
	}
	return int(p.ptmx.Fd())
}

// ShellName returns the shell's command name (e.g., "bash", "zsh") without
// forking a subprocess. Falls back to processName() if the cmd path is unavailable.
func (p *Pane) ShellName() string {
	if p.cmd != nil {
		name := p.cmd.Path
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		return strings.TrimPrefix(name, "-")
	}
	if pid := p.ProcessPid(); pid != 0 {
		return processName(pid)
	}
	return ""
}

// ProcessPid returns the PID of the shell process.
func (p *Pane) ProcessPid() int {
	if p.cmd != nil {
		return p.cmd.Process.Pid
	}
	if p.process != nil {
		return p.process.Pid
	}
	return 0
}

// ReplayScreen feeds screen data into the emulator to restore visual state.
func (p *Pane) ReplayScreen(data string) {
	p.beginSnapshotMutation()
	p.emulator.Write([]byte(data))
	p.endSnapshotMutation()
}

// Start launches the goroutines that read PTY output and wait for exit.
// Call this after releasing any locks that onOutput/onExit callbacks need.
func (p *Pane) Start() {
	go p.readLoop()
	if !p.drainStarted {
		go p.drainResponses()
	}
	go p.waitLoop()
}

// SetOnClipboard sets the callback invoked when OSC 52 clipboard sequences
// are detected in pane output. Must be called before Start().
func (p *Pane) SetOnClipboard(fn func(paneID uint32, data []byte)) {
	p.onClipboard = fn
}

// SetOnTakeover sets the callback invoked when a nested amux emits a
// takeover sequence through the PTY. Must be called before Start().
func (p *Pane) SetOnTakeover(fn func(paneID uint32, req TakeoverRequest)) {
	p.onTakeover = fn
}

// readLoop reads PTY output, feeds the emulator, and notifies the callback.
func (p *Pane) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			if p.onClipboard != nil {
				for _, seq := range p.osc52Scanner.Scan(data) {
					p.onClipboard(p.ID, seq)
				}
			}

			if p.onTakeover != nil {
				for _, req := range p.controlScanner.Scan(data) {
					p.onTakeover(p.ID, req)
				}
			}

			// Feed emulator for screen state tracking (enables reattach).
			seq := p.applyOutput(data)

			if p.onOutput != nil {
				p.onOutput(p.ID, data, seq)
			}
		}
		if err != nil {
			return
		}
	}
}

// drainResponses reads terminal responses from the emulator (DA replies,
// cursor position reports, etc.) and writes them back to the PTY so the
// shell receives them. Without this, the emulator's unbuffered io.Pipe
// blocks on the first response, deadlocking the server.
func (p *Pane) drainResponses() {
	buf := make([]byte, 1024)
	for {
		n, err := p.emulator.Read(buf)
		if n > 0 {
			p.ptmx.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// applyOutput feeds PTY bytes into the retained emulator state and returns the
// monotonically increasing output sequence included in that state.
func (p *Pane) applyOutput(data []byte) uint64 {
	p.beginSnapshotMutation()
	p.emulator.Write(data)
	seq := p.outputSeq.Add(1)
	p.endSnapshotMutation()
	return seq
}

// waitLoop waits for the shell process to exit.
func (p *Pane) waitLoop() {
	if p.cmd != nil {
		p.cmd.Wait()
	} else if p.process != nil {
		p.process.Wait()
	}
	if p.onExit != nil {
		p.onExit(p.ID)
	}
}

// Write sends input data to the PTY (from client keyboard input).
// For proxy panes, input is routed through writeOverride to the remote server.
func (p *Pane) Write(data []byte) (int, error) {
	if p.writeOverride != nil {
		return p.writeOverride(data)
	}
	return p.ptmx.Write(data)
}

// EmulatorSize returns the current emulator dimensions.
func (p *Pane) EmulatorSize() (cols, rows int) {
	if p.emulator != nil {
		return p.emulator.Size()
	}
	return 0, 0
}

// Resize changes the PTY and emulator dimensions.
func (p *Pane) Resize(cols, rows int) error {
	p.beginSnapshotMutation()
	defer p.endSnapshotMutation()
	if p.emulator != nil {
		p.emulator.Resize(cols, rows)
	}
	if p.ptmx == nil {
		return nil
	}
	return pty.Setsize(p.ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// Render returns the current screen cell content as an ANSI string.
// Used by the compositor via PaneData.RenderScreen(). For the reattach
// snapshot (which needs cursor positioning embedded), use RenderScreen().
func (p *Pane) Render() string {
	return p.emulator.Render()
}

// RenderScreen returns the screen state with a trailing cursor-position escape.
// Used when sending a reattach snapshot to a reconnecting client so the
// client-side emulator seeds the correct cursor position.
func (p *Pane) RenderScreen() string {
	return RenderWithCursor(p.emulator)
}

// HistoryScreenSnapshot returns a consistent snapshot of retained scrollback,
// current screen, and the latest live-output sequence included in that state.
func (p *Pane) HistoryScreenSnapshot() (history []string, screen string, seq uint64) {
	for {
		before := p.waitForStableSnapshot()
		history = p.combinedScrollback(p.loadBaseHistory())
		screen = RenderWithCursor(p.emulator)
		seq = p.outputSeq.Load()
		after := p.snapshotSeq.Load()
		if before == after && after%2 == 0 {
			return history, screen, seq
		}
	}
}

// CaptureSnapshot returns a consistent plain-text snapshot of retained
// scrollback, visible screen content, and cursor state.
func (p *Pane) CaptureSnapshot() CaptureSnapshot {
	for {
		before := p.waitForStableSnapshot()
		col, row := p.emulator.CursorPosition()
		snap := CaptureSnapshot{
			History:      p.combinedScrollback(p.loadBaseHistory()),
			Content:      EmulatorContentLines(p.emulator),
			CursorCol:    col,
			CursorRow:    row,
			CursorHidden: p.emulator.CursorHidden(),
		}
		after := p.snapshotSeq.Load()
		if before == after && after%2 == 0 {
			return snap
		}
	}
}

// RenderWithoutCursorBlock returns the screen with the cursor cell's
// reverse-video attribute cleared, so inactive panes don't show a block cursor.
func (p *Pane) RenderWithoutCursorBlock() string {
	return p.emulator.RenderWithoutCursorBlock()
}

// HasCursorBlock returns true if the pane contains an app-rendered block cursor.
func (p *Pane) HasCursorBlock() bool {
	return p.emulator.HasCursorBlock()
}

// CursorPos returns the cursor position within this pane (0-indexed).
func (p *Pane) CursorPos() (col, row int) {
	return p.emulator.CursorPosition()
}

// CursorHidden returns true if the application running in this pane has
// hidden the hardware cursor (e.g. via \033[?25l).
func (p *Pane) CursorHidden() bool {
	return p.emulator.CursorHidden()
}

// Output returns the last N lines of visible pane content from the emulator.
func (p *Pane) Output(lines int) string {
	_, rows := p.emulator.Size()
	var result []string
	for y := rows - 1; y >= 0 && len(result) < lines; y-- {
		plain := p.emulator.ScreenLineText(y)
		if plain != "" {
			result = append([]string{plain}, result...)
		}
	}
	return strings.Join(result, "\n")
}

// ContentLines returns all visible screen lines as a slice of plain text strings.
// Every row from 0 to height-1 is represented (len(result) == pane height).
// Lines are right-trimmed of trailing whitespace.
func (p *Pane) ContentLines() []string {
	return EmulatorContentLines(p.emulator)
}

// ScrollbackLines returns retained plain-text scrollback lines from oldest to
// newest.
func (p *Pane) ScrollbackLines() []string {
	for {
		before := p.waitForStableSnapshot()
		lines := p.combinedScrollback(p.loadBaseHistory())
		after := p.snapshotSeq.Load()
		if before == after && after%2 == 0 {
			return lines
		}
	}
}

// OutputSeq reports the latest live-output sequence applied to the emulator.
func (p *Pane) OutputSeq() uint64 {
	return p.outputSeq.Load()
}

// SetRetainedHistory replaces the retained pre-attach/pre-reload history base
// for this pane. New live scrollback from the emulator is combined on top.
func (p *Pane) SetRetainedHistory(lines []string) {
	p.beginSnapshotMutation()
	defer p.endSnapshotMutation()
	limit := effectiveScrollbackLines(p.scrollbackLines)
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	p.baseHistory.Store(&paneBaseHistory{lines: append([]string(nil), lines...)})
}

// ScreenContains returns true if substr appears in the pane's visible screen
// content, matching across soft-wrapped lines.
func (p *Pane) ScreenContains(substr string) bool {
	return p.emulator.ScreenContains(substr)
}

// Close terminates the pane's shell and PTY.
// For proxy panes (no PTY), Close() just marks the pane as closed.
func (p *Pane) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	if p.cmd != nil {
		p.cmd.Process.Signal(syscall.SIGHUP)
	} else if p.process != nil {
		p.process.Signal(syscall.SIGHUP)
	}
	if p.ptmx == nil {
		return nil
	}
	return p.ptmx.Close()
}

// NewProxyPaneWithScrollback creates a proxy pane with an explicit retained
// scrollback limit.
func NewProxyPaneWithScrollback(id uint32, meta PaneMeta, cols, rows int,
	scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32),
	writeOverride func([]byte) (int, error)) *Pane {

	emu := NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)
	p := &Pane{
		ID:              id,
		Meta:            meta,
		emulator:        emu,
		writeOverride:   writeOverride,
		onOutput:        onOutput,
		onExit:          onExit,
		createdAt:       time.Now(),
		drainStarted:    true, // no PTY responses to drain
		scrollbackLines: effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	// Start drain goroutine for emulator responses (DA replies etc.)
	// that would otherwise block the emulator's pipe.
	go p.drainResponsesDiscard()
	return p
}

// drainResponsesDiscard reads and discards terminal responses from the
// emulator. Proxy panes have no PTY to forward responses to, but the
// emulator's pipe must be drained to prevent blocking.
func (p *Pane) drainResponsesDiscard() {
	buf := make([]byte, 1024)
	for {
		_, err := p.emulator.Read(buf)
		if err != nil {
			return
		}
	}
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
	total := len(baseHistory) + len(live)
	if total <= limit {
		out := make([]string, 0, total)
		out = append(out, baseHistory...)
		out = append(out, live...)
		return out
	}

	drop := total - limit
	baseStart := 0
	liveStart := 0
	if drop >= len(baseHistory) {
		baseStart = len(baseHistory)
		liveStart = drop - len(baseHistory)
	} else {
		baseStart = drop
	}

	out := make([]string, 0, limit)
	out = append(out, baseHistory[baseStart:]...)
	out = append(out, live[liveStart:]...)
	return out
}

// IsProxy returns true if this is a proxy pane (no local PTY).
func (p *Pane) IsProxy() bool {
	return p.writeOverride != nil
}
