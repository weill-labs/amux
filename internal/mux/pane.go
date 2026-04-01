package mux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/debugowner"
	"github.com/weill-labs/amux/internal/termprofile"
)

// DefaultHost is the host value for locally-running panes.
const DefaultHost = "local"

// PaneNameFormat is the format string for auto-assigned pane names.
const PaneNameFormat = "pane-%d"

func paneShellEnv(id uint32, sessionName string) []string {
	return paneCommandEnvWithProfile(os.Environ(), id, sessionName, "")
}

func paneShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "/bin/bash"
	}
	return shell
}

func shellPath(shell string, pid int) string {
	if shell != "" {
		return shell
	}
	if pid != 0 {
		if name := processName(pid); name != "" {
			if path, err := exec.LookPath(name); err == nil {
				return path
			}
			return name
		}
	}
	return paneShellPath()
}

func paneExecCommand(shell string, id uint32, sessionName, dir, colorProfile string) *exec.Cmd {
	cmd := exec.Command(shell, "-l")
	cmd.Env = paneCommandEnvWithProfile(os.Environ(), id, sessionName, colorProfile)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd
}

func startPaneShell(shell string, id uint32, sessionName, dir string, cols, rows int, colorProfile string) (*exec.Cmd, *os.File, error) {
	cmd := paneExecCommand(shell, id, sessionName, dir, colorProfile)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, nil, err
	}
	return cmd, ptmx, nil
}

// Pane manages a PTY, its terminal emulator, and metadata.
type Pane struct {
	ID          uint32
	ActivePoint uint64 // monotonic counter — higher means more recently focused
	Meta        PaneMeta

	ptmx          *os.File
	cmd           *exec.Cmd
	process       *os.Process // set for restored panes (where cmd is nil)
	emulator      TerminalEmulator
	actorCommands chan paneCommand
	actorDone     chan struct{}
	readLoopDone  chan struct{}
	drainLoopDone chan struct{}
	waitLoopDone  chan struct{}
	actorClosing  atomic.Bool
	suppressExit  atomic.Bool

	outputSeq        atomic.Uint64
	baseHistory      atomic.Pointer[paneBaseHistory]
	scrollbackWidths atomic.Pointer[[]int]
	scrollbackLines  int
	scrollbackLimit  int

	// writeOverride, when non-nil, receives Write() calls instead of the PTY.
	// Used by proxy panes to route input over SSH to a remote amux server.
	writeOverride func([]byte) (int, error)

	closed              atomic.Bool
	exitDone            chan struct{} // closed by waitLoop when the shell process exits
	closeDoneOnce       sync.Once
	closeDone           chan struct{}
	closeErr            error
	closeForbiddenOwner *debugowner.Checker
	drainStarted        bool
	onOutput            func(paneID uint32, data []byte, seq uint64)
	onExit              func(paneID uint32, reason string)
	onClipboard         func(paneID uint32, data []byte)
	onTakeover          func(paneID uint32, req TakeoverRequest)
	onMetaUpdate        func(paneID uint32, update MetaUpdate)
	osc52Scanner        OSC52Scanner
	controlScanner      AmuxControlScanner
	metaScanner         AmuxMetaScanner
	suppressCallbacks   atomic.Bool

	// Idle tracking (LAB-159)
	createdAt        time.Time
	lastBusySeenUnix atomic.Int64 // UnixNano; last time process tree showed busy
	idleSinceUnix    atomic.Int64 // UnixNano; when the current idle period began

	// CWD/branch detection
	liveCwd          string // last-detected CWD, not checkpointed
	metaManualBranch bool   // true when GitBranch was set via escape sequence or CLI
}

type paneBaseHistory struct {
	lines []string
}

// PaneTerminalSnapshot is a lightweight snapshot of the pane's cursor and
// non-text terminal metadata. It avoids the history/content allocations used
// by CaptureSnapshot when callers only need terminal metadata.
type PaneTerminalSnapshot struct {
	Terminal     TerminalState
	CursorCol    int
	CursorRow    int
	CursorHidden bool
}

// NewPaneWithScrollback creates a new pane running the user's shell but does
// NOT start the read/drain/wait goroutines. Call Start() after releasing any
// locks that the onOutput/onExit callbacks might need.
func NewPaneWithScrollback(id uint32, meta PaneMeta, cols, rows int, sessionName string, scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	return NewPaneWithScrollbackColorProfile(id, meta, cols, rows, sessionName, scrollbackLines, "", onOutput, onExit)
}

func NewPaneWithScrollbackColorProfile(id uint32, meta PaneMeta, cols, rows int, sessionName string, scrollbackLines int, colorProfile string, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	return newPaneWithShellPath(id, meta, cols, rows, sessionName, scrollbackLines, shellPath("", 0), colorProfile, onOutput, onExit)
}

func newPaneWithShellPath(id uint32, meta PaneMeta, cols, rows int, sessionName string, scrollbackLines int, shell, colorProfile string, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	manualBranch, err := NormalizePaneMeta(&meta)
	if err != nil {
		return nil, err
	}

	cmd, ptmx, err := startPaneShell(shell, id, sessionName, meta.Dir, cols, rows, colorProfile)
	if err != nil {
		return nil, err
	}

	emu := NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)

	p := &Pane{
		ID:               id,
		Meta:             meta,
		ptmx:             ptmx,
		cmd:              cmd,
		emulator:         emu,
		exitDone:         make(chan struct{}),
		onOutput:         onOutput,
		onExit:           onExit,
		createdAt:        time.Now(),
		metaManualBranch: manualBranch,
		scrollbackLines:  effectiveScrollbackLines(scrollbackLines),
		scrollbackLimit:  effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	wireScrollbackCallbacks(p)
	p.startActor()
	return p, nil
}

// wireScrollbackCallbacks connects the emulator's scrollback push/clear
// callbacks to the pane's atomic width tracking.
func wireScrollbackCallbacks(p *Pane) {
	if vte, ok := p.emulator.(*vtEmulator); ok {
		vte.scrollbackPushFn = p.recordScrollbackPush
		vte.scrollbackClearFn = p.clearScrollbackWidths
	}
}

func paneCommandEnv(base []string, paneID uint32, sessionName string) []string {
	return paneCommandEnvWithProfile(base, paneID, sessionName, "")
}

func paneCommandEnvWithProfile(base []string, paneID uint32, sessionName, colorProfile string) []string {
	env := make([]string, 0, len(base)+4)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		switch key {
		case "TERM", "AMUX_PANE", "AMUX_SESSION", termprofile.EnvKey:
			// amux owns these values for pane shells.
			continue
		case "NO_COLOR", "CODEX_CI":
			// These are launcher-context flags. Passing them through to an
			// interactive pane makes nested tools like Codex suppress ANSI.
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"TERM=amux",
		fmt.Sprintf("AMUX_PANE=%d", paneID),
		"AMUX_SESSION="+sessionName,
	)
	if colorProfile == "" {
		env = append(env, termprofile.EnvEntry(paneEnv(base)))
	} else {
		env = append(env, fmt.Sprintf("%s=%s", termprofile.EnvKey, colorProfile))
	}
	return env
}

type paneEnv []string

func (e paneEnv) Environ() []string {
	out := make([]string, 0, len(e))
	for _, entry := range e {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (key == "NO_COLOR" || key == "CODEX_CI") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (e paneEnv) Getenv(key string) string {
	if key == "NO_COLOR" || key == "CODEX_CI" {
		return ""
	}
	prefix := key + "="
	for _, entry := range e {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

// RestorePaneWithScrollback creates a pane from inherited file descriptors
// after server reload using an explicit retained scrollback limit. It wraps an
// existing PTY master FD and finds the running shell process by PID. No new
// shell is spawned — the existing shell survives the exec. The drain goroutine
// starts immediately to prevent deadlock during screen replay.
func RestorePaneWithScrollback(id uint32, meta PaneMeta, ptmxFd, pid, cols, rows int, scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	manualBranch, err := NormalizePaneMeta(&meta)
	if err != nil {
		return nil, err
	}

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
		ID:               id,
		Meta:             meta,
		ptmx:             ptmx,
		process:          proc,
		emulator:         emu,
		exitDone:         make(chan struct{}),
		drainStarted:     true,
		onOutput:         onOutput,
		onExit:           onExit,
		createdAt:        time.Now(),
		metaManualBranch: manualBranch,
		scrollbackLines:  effectiveScrollbackLines(scrollbackLines),
		scrollbackLimit:  effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	wireScrollbackCallbacks(p)
	p.startActor()

	// Start drain immediately so screen replay doesn't deadlock
	// on the emulator's unbuffered response pipe.
	go p.drainResponses(emu, ptmx, nil)

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

func (p *Pane) loadBaseHistory() []string {
	base := p.baseHistory.Load()
	if base == nil {
		return nil
	}
	return base.lines
}

func (p *Pane) loadScrollbackWidths() []int {
	if ptr := p.scrollbackWidths.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

func (p *Pane) recordScrollbackPush(count, width int) {
	if count <= 0 || width <= 0 {
		return
	}
	old := p.loadScrollbackWidths()
	newWidths := make([]int, len(old), len(old)+count)
	copy(newWidths, old)
	for range count {
		newWidths = append(newWidths, width)
	}
	if overflow := len(newWidths) - p.scrollbackLimit; overflow > 0 {
		newWidths = newWidths[overflow:]
	}
	p.scrollbackWidths.Store(&newWidths)
}

func (p *Pane) clearScrollbackWidths() {
	empty := make([]int, 0)
	p.scrollbackWidths.Store(&empty)
}

// ScrollbackSourceWidth returns the pane width at which the scrollback row
// was originally wrapped. Returns zero when unknown.
func (p *Pane) ScrollbackSourceWidth(row int) int {
	return paneActorValue(p, func() int {
		widths := p.loadScrollbackWidths()
		if row < 0 || row >= len(widths) {
			return 0
		}
		return widths[row]
	})
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

// DetectCwdBranch returns the current CWD and git branch without mutating state.
// Safe to call from any goroutine.
func (p *Pane) DetectCwdBranch() (cwd, branch string) {
	pid := p.ProcessPid()
	if pid == 0 {
		return "", ""
	}
	cwd = PaneCwd(pid)
	if cwd == "" {
		return "", ""
	}
	branch = GitBranch(cwd)
	return cwd, branch
}

// ApplyCwdBranch updates cached CWD/branch. Only call from the session event loop.
func (p *Pane) ApplyCwdBranch(cwd, branch string) {
	p.liveCwd = cwd
	if !p.metaManualBranch {
		p.Meta.GitBranch = branch
	}
}

// LiveCwd returns the last-detected working directory.
func (p *Pane) LiveCwd() string {
	return p.liveCwd
}

// SetMetaManualBranch controls whether auto-refresh should update GitBranch.
func (p *Pane) SetMetaManualBranch(manual bool) {
	p.metaManualBranch = manual
}

// MetaManualBranch reports whether GitBranch is pinned by user metadata.
func (p *Pane) MetaManualBranch() bool {
	return p.metaManualBranch
}

// ReplayScreen feeds screen data into the emulator to restore visual state.
func (p *Pane) ReplayScreen(data string) {
	p.withActor(func() {
		_, _ = p.emulator.Write([]byte(data))
	})
}

// Start launches the goroutines that read PTY output and wait for exit.
// Call this after releasing any locks that onOutput/onExit callbacks need.
func (p *Pane) Start() {
	var (
		ptmx          *os.File
		emulator      TerminalEmulator
		cmd           *exec.Cmd
		proc          *os.Process
		exitDone      chan struct{}
		readLoopDone  chan struct{}
		drainLoopDone chan struct{}
		waitLoopDone  chan struct{}
		startRead     bool
		startDrain    bool
	)
	p.withActor(func() {
		ptmx = p.ptmx
		emulator = p.emulator
		cmd = p.cmd
		proc = p.process
		exitDone = p.exitDone
		if p.ptmx != nil && p.readLoopDone == nil {
			p.readLoopDone = make(chan struct{})
		}
		if p.drainLoopDone == nil {
			p.drainLoopDone = make(chan struct{})
		}
		if p.waitLoopDone == nil {
			p.waitLoopDone = make(chan struct{})
		}
		readLoopDone = p.readLoopDone
		drainLoopDone = p.drainLoopDone
		waitLoopDone = p.waitLoopDone
		startRead = ptmx != nil && readLoopDone != nil
		if !p.drainStarted {
			p.drainStarted = true
			startDrain = true
		}
	})
	if startRead {
		go p.readLoop(ptmx, readLoopDone)
	}
	if startDrain {
		go p.drainResponses(emulator, ptmx, drainLoopDone)
	}
	go p.waitLoop(cmd, proc, exitDone, waitLoopDone)
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

// SetOnMetaUpdate sets the callback invoked when an amux-meta escape
// sequence is detected in pane output. Must be called before Start().
func (p *Pane) SetOnMetaUpdate(fn func(paneID uint32, update MetaUpdate)) {
	p.onMetaUpdate = fn
}

// readLoop reads PTY output, feeds the emulator, and notifies the callback.
func (p *Pane) readLoop(ptmx *os.File, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			if p.suppressCallbacks.Load() {
				if err != nil {
					return
				}
				continue
			}
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

			if p.onMetaUpdate != nil {
				for _, update := range p.metaScanner.Scan(data) {
					p.onMetaUpdate(p.ID, update)
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
func (p *Pane) drainResponses(emulator TerminalEmulator, ptmx *os.File, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	buf := make([]byte, 1024)
	for {
		n, err := emulator.Read(buf)
		if n > 0 {
			ptmx.Write(buf[:n])
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

// waitLoop waits for the shell process to exit. Closes exitDone so that
// Close() can detect the process has exited without a redundant cmd.Wait().
func (p *Pane) waitLoop(cmd *exec.Cmd, proc *os.Process, exitDone, waitLoopDone chan struct{}) {
	if waitLoopDone != nil {
		defer close(waitLoopDone)
	}
	var err error
	if cmd != nil {
		err = cmd.Wait()
	} else if proc != nil {
		_, err = proc.Wait()
	}
	close(exitDone)
	if p.suppressExit.Swap(false) {
		return
	}
	if p.onExit != nil && !p.suppressCallbacks.Load() {
		p.onExit(p.ID, formatExitReason(err))
	}
}

// formatExitReason turns a Wait() error into a human-readable exit reason.
func formatExitReason(err error) string {
	if err == nil {
		return "exit 0"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return "signal: " + status.Signal().String()
			}
		}
		return fmt.Sprintf("exit %d", exitErr.ExitCode())
	}
	return err.Error()
}

// Write sends input data to the PTY (from client keyboard input).
// For proxy panes, input is routed through writeOverride to the remote server.
func (p *Pane) Write(data []byte) (int, error) {
	if p.writeOverride != nil {
		return p.writeOverride(data)
	}
	return p.ptmx.Write(data)
}

// SuppressCallbacks prevents further output and exit callbacks from this pane.
// Used when replacing a live PTY-backed pane in-place so late bytes from the
// old process cannot tear down or dirty the replacement slot.
func (p *Pane) SuppressCallbacks() {
	p.suppressCallbacks.Store(true)
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

func (p *Pane) resizePTY(cols, rows int) error {
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

// shellProcess returns the *os.Process for the pane's shell, or nil for proxy panes.
func (p *Pane) shellProcess() *os.Process {
	if p.cmd != nil {
		return p.cmd.Process
	}
	if p.process != nil {
		return p.process
	}
	return nil
}

func (p *Pane) ensureCloseDone() chan struct{} {
	p.closeDoneOnce.Do(func() {
		p.closeDone = make(chan struct{})
	})
	return p.closeDone
}

// SetCloseForbiddenOwner installs a debug-only guard that panics when Close is
// invoked on the recorded owner goroutine.
func (p *Pane) SetCloseForbiddenOwner(owner *debugowner.Checker) {
	p.closeForbiddenOwner = owner
}

// Close terminates the pane's shell and PTY, but only schedules the blocking
// wait/close work on a background goroutine. Call WaitClosed when a caller
// needs a barrier before proceeding.
func (p *Pane) Close() error {
	if p.closeForbiddenOwner != nil {
		p.closeForbiddenOwner.PanicIfCurrent("mux.Pane", "Close")
	}
	if p.closed.Swap(true) {
		return nil
	}
	done := p.ensureCloseDone()
	go func() {
		p.closeErr = p.closeBlocking()
		close(done)
	}()
	return nil
}

// WaitClosed waits for the background Close teardown to finish and returns the
// final close error, if any.
func (p *Pane) WaitClosed() error {
	if !p.closed.Load() {
		return nil
	}
	<-p.ensureCloseDone()
	return p.closeErr
}

func (p *Pane) closeBlocking() error {
	proc := p.shellProcess()
	if proc != nil {
		_ = proc.Signal(syscall.SIGHUP)
	}
	var ptmxErr error
	if p.ptmx != nil {
		ptmxErr = p.ptmx.Close()
	}
	if p.readLoopDone != nil {
		<-p.readLoopDone
	}
	if proc != nil {
		select {
		case <-p.exitDone:
		case <-time.After(2 * time.Second):
			_ = proc.Signal(syscall.SIGKILL)
			<-p.exitDone
		}
	}
	p.stopActor()
	emuErr := func() error {
		if p.emulator == nil {
			return nil
		}
		return p.emulator.Close()
	}()
	return errors.Join(ptmxErr, emuErr)
}

// NewProxyPaneWithScrollback creates a proxy pane with an explicit retained
// scrollback limit.
func NewProxyPaneWithScrollback(id uint32, meta PaneMeta, cols, rows int,
	scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32, string),
	writeOverride func([]byte) (int, error)) *Pane {
	manualBranch, err := NormalizePaneMeta(&meta)
	if err != nil {
		panic(err)
	}

	emu := NewVTEmulatorWithScrollback(cols, rows, scrollbackLines)
	exitDone := make(chan struct{})
	close(exitDone) // proxy panes have no process to wait for
	p := &Pane{
		ID:               id,
		Meta:             meta,
		emulator:         emu,
		writeOverride:    writeOverride,
		exitDone:         exitDone,
		drainLoopDone:    make(chan struct{}),
		onOutput:         onOutput,
		onExit:           onExit,
		createdAt:        time.Now(),
		metaManualBranch: manualBranch,
		drainStarted:     true, // no PTY responses to drain
		scrollbackLines:  effectiveScrollbackLines(scrollbackLines),
		scrollbackLimit:  effectiveScrollbackLines(scrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	wireScrollbackCallbacks(p)
	p.startActor()
	// Start drain goroutine for emulator responses (DA replies etc.)
	// that would otherwise block the emulator's pipe.
	go p.drainResponsesDiscard(emu, p.drainLoopDone)
	return p
}

// drainResponsesDiscard reads and discards terminal responses from the
// emulator. Proxy panes have no PTY to forward responses to, but the
// emulator's pipe must be drained to prevent blocking.
func (p *Pane) drainResponsesDiscard(emulator TerminalEmulator, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	buf := make([]byte, 1024)
	for {
		_, err := emulator.Read(buf)
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

// IsProxy returns true if this is a proxy pane (no local PTY).
func (p *Pane) IsProxy() bool {
	return p.writeOverride != nil
}

// Respawn replaces the pane's local shell process with a fresh login shell in
// the same pane slot without changing pane identity.
func (p *Pane) Respawn(sessionName, dir string) error {
	if p.IsProxy() {
		return fmt.Errorf("cannot respawn proxy pane")
	}
	if p.closed.Load() {
		return fmt.Errorf("pane is closed")
	}

	cols, rows := p.EmulatorSize()
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	if dir == "" {
		dir = p.LiveCwd()
	}
	if dir == "" {
		dir = p.Meta.Dir
	}

	shell := shellPath("", p.ProcessPid())
	if p.cmd != nil && p.cmd.Path != "" {
		shell = shellPath(p.cmd.Path, p.ProcessPid())
	}
	cmd, ptmx, err := startPaneShell(shell, p.ID, sessionName, dir, cols, rows, "")
	if err != nil {
		return err
	}
	emu := NewVTEmulatorWithScrollback(cols, rows, p.scrollbackLimit)
	oldEmu := p.emulator
	oldDrainLoopDone := p.drainLoopDone

	p.suppressExit.Store(true)
	p.stopForRespawn()
	p.withActor(func() {
		p.ptmx = ptmx
		p.cmd = cmd
		p.process = nil
		p.emulator = emu
		p.readLoopDone = nil
		p.drainLoopDone = nil
		p.waitLoopDone = nil
		p.drainStarted = false
		p.exitDone = make(chan struct{})
		p.baseHistory.Store(&paneBaseHistory{})
		p.clearScrollbackWidths()
		p.osc52Scanner = OSC52Scanner{}
		p.controlScanner = AmuxControlScanner{}
		p.metaScanner = AmuxMetaScanner{}
		p.createdAt = time.Now()
		storeUnixTime(&p.lastBusySeenUnix, time.Time{})
		storeUnixTime(&p.idleSinceUnix, time.Time{})
		p.liveCwd = dir
		p.Meta.Dir = dir
		wireScrollbackCallbacks(p)
	})
	if oldEmu != nil {
		_ = oldEmu.Close()
	}
	if oldDrainLoopDone != nil {
		<-oldDrainLoopDone
	}
	return nil
}

func (p *Pane) stopForRespawn() {
	proc := p.shellProcess()
	if proc != nil {
		_ = proc.Signal(syscall.SIGHUP)
	}
	if p.ptmx != nil {
		_ = p.ptmx.Close()
	}
	if p.readLoopDone != nil {
		<-p.readLoopDone
	}
	if proc != nil {
		select {
		case <-p.exitDone:
		case <-time.After(2 * time.Second):
			_ = proc.Signal(syscall.SIGKILL)
			<-p.exitDone
		}
	}
	if p.waitLoopDone != nil {
		<-p.waitLoopDone
	}
}

// Replacement clones a local PTY pane into a fresh shell process with the same
// pane ID and metadata. The caller is responsible for swapping the returned
// pane into session/window state and closing the old pane.
func (p *Pane) Replacement(sessionName, startDir string, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	return p.ReplacementWithColorProfile(sessionName, startDir, "", onOutput, onExit)
}

func (p *Pane) ReplacementWithColorProfile(sessionName, startDir, colorProfile string, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	if p.IsProxy() {
		return nil, fmt.Errorf("cannot replace proxy pane")
	}

	cols, rows := p.EmulatorSize()
	meta := p.Meta
	launchMeta := meta
	if startDir != "" {
		launchMeta.Dir = startDir
	}

	shell := shellPath("", p.ProcessPid())
	if p.cmd != nil && p.cmd.Path != "" {
		shell = shellPath(p.cmd.Path, p.ProcessPid())
	}
	pane, err := newPaneWithShellPath(p.ID, launchMeta, cols, rows, sessionName, p.scrollbackLines, shell, colorProfile, onOutput, onExit)
	if err != nil {
		return nil, err
	}
	pane.Meta = meta
	pane.SetMetaManualBranch(p.MetaManualBranch())
	if startDir != "" {
		pane.ApplyCwdBranch(startDir, p.Meta.GitBranch)
	}
	return pane, nil
}
