package mux

import (
	"errors"
	"fmt"
	"io"
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

	runtimeInfoMu   sync.RWMutex
	runtimePID      int
	runtimeShellCmd string

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
	scrollbackWidths []int
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
	metaManualBranch bool   // true when metadata owns branch state, including explicit empty clears
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

// NewPendingPaneWithScrollback creates a local pane placeholder with emulator
// state but no PTY or process. Callers can place it in layout immediately
// while the real PTY is created on a background goroutine.
func NewPendingPaneWithScrollback(id uint32, meta PaneMeta, cols, rows int, scrollbackLines int, onOutput func(uint32, []byte, uint64), onExit func(uint32, string)) (*Pane, error) {
	manualBranch, err := NormalizePaneMeta(&meta)
	if err != nil {
		return nil, err
	}

	emu := newPaneEmulator(cols, rows, scrollbackLines)
	p := &Pane{
		ID:               id,
		Meta:             meta,
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
	p.setRuntimeProcessInfo(nil, nil)
	wireScrollbackCallbacks(p)
	p.startActor()
	return p, nil
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

	emu := newPaneEmulator(cols, rows, scrollbackLines)

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
	p.setRuntimeProcessInfo(cmd, nil)
	wireScrollbackCallbacks(p)
	p.startActor()
	return p, nil
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
		case "TERM_PROGRAM", "TERM_PROGRAM_VERSION":
			// These identify the outer terminal (e.g. Ghostty, iTerm).
			// Inside amux the terminal emulator is the vt library, not the
			// outer app. Propagating these causes TUI apps to enable
			// features (like DEC 2026 synchronized output) that amux's
			// emulator does not implement, leading to rendering corruption.
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
		if ok && (key == "NO_COLOR" || key == "CODEX_CI" || key == termprofile.EnvKey) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (e paneEnv) Getenv(key string) string {
	if key == "NO_COLOR" || key == "CODEX_CI" || key == termprofile.EnvKey {
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

	emu := newPaneEmulator(cols, rows, scrollbackLines)

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
	p.setRuntimeProcessInfo(nil, proc)
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

// Scrollback width callbacks fire synchronously during actor-owned emulator
// writes, so the width slice can be reused in place without atomic snapshots.
func (p *Pane) loadScrollbackWidths() []int {
	return p.scrollbackWidths
}

func (p *Pane) recordScrollbackPush(count, width int) {
	if count <= 0 || width <= 0 {
		return
	}
	for range count {
		p.scrollbackWidths = append(p.scrollbackWidths, width)
	}
	if overflow := len(p.scrollbackWidths) - p.scrollbackLimit; overflow > 0 {
		copy(p.scrollbackWidths, p.scrollbackWidths[overflow:])
		p.scrollbackWidths = p.scrollbackWidths[:len(p.scrollbackWidths)-overflow]
	}
}

func (p *Pane) clearScrollbackWidths() {
	p.scrollbackWidths = p.scrollbackWidths[:0]
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
	_, shellCmd := p.runtimeProcessInfo()
	if shellCmd != "" {
		name := shellCmd
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
	pid, _ := p.runtimeProcessInfo()
	return pid
}

func (p *Pane) runtimeProcessInfo() (pid int, shellCmd string) {
	p.runtimeInfoMu.RLock()
	defer p.runtimeInfoMu.RUnlock()
	return p.runtimePID, p.runtimeShellCmd
}

func (p *Pane) setRuntimeProcessInfo(cmd *exec.Cmd, proc *os.Process) {
	pid := 0
	shellCmd := ""
	if cmd != nil {
		shellCmd = cmd.Path
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
	} else if proc != nil {
		pid = proc.Pid
	}
	p.runtimeInfoMu.Lock()
	p.runtimePID = pid
	p.runtimeShellCmd = shellCmd
	p.runtimeInfoMu.Unlock()
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
		p.setRuntimeProcessInfo(cmd, proc)
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
		return writeAll(data, p.writeOverride)
	}
	if p.ptmx == nil {
		return 0, errors.New("pane not ready")
	}
	return writeAll(data, p.ptmx.Write)
}

// AcceptsInput reports whether writes can be routed to the pane immediately.
// Pending local panes return false until the real PTY-backed pane replaces the
// placeholder in session state.
func (p *Pane) AcceptsInput() bool {
	if p == nil {
		return false
	}
	return p.writeOverride != nil || p.ptmx != nil
}

func writeAll(data []byte, write func([]byte) (int, error)) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := write(data)
		if n < 0 || n > len(data) {
			return total, fmt.Errorf("invalid write count %d", n)
		}
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// SuppressCallbacks prevents further output and exit callbacks from this pane.
// Used when replacing a live PTY-backed pane in-place so late bytes from the
// old process cannot tear down or dirty the replacement slot.
func (p *Pane) SuppressCallbacks() {
	p.suppressCallbacks.Store(true)
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
	state := p.detachRuntimeState()
	proc := state.process
	if proc == nil && state.cmd != nil {
		proc = state.cmd.Process
	}
	if proc != nil {
		_ = proc.Signal(syscall.SIGHUP)
	}
	var ptmxErr error
	if state.ptmx != nil {
		ptmxErr = state.ptmx.Close()
	}
	if state.readLoopDone != nil {
		<-state.readLoopDone
	}
	p.waitForDetachedProcessExit(state, proc, 2*time.Second)
	p.stopActor()
	emuErr := func() error {
		if state.emulator == nil {
			return nil
		}
		return state.emulator.Close()
	}()
	return errors.Join(ptmxErr, emuErr)
}

func (p *Pane) waitForProcessExit(proc *os.Process, timeout time.Duration) {
	if proc == nil {
		return
	}
	if p.waitLoopDone != nil {
		select {
		case <-p.exitDone:
		case <-time.After(timeout):
			_ = proc.Signal(syscall.SIGKILL)
			<-p.exitDone
		}
		<-p.waitLoopDone
		return
	}
	if p.cmd == nil {
		select {
		case <-p.exitDone:
		case <-time.After(timeout):
			_ = proc.Signal(syscall.SIGKILL)
			<-p.exitDone
		}
		return
	}

	waitDone := make(chan struct{})
	cmd := p.cmd
	exitDone := p.exitDone
	go func() {
		_ = cmd.Wait()
		close(exitDone)
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(timeout):
		_ = proc.Signal(syscall.SIGKILL)
		<-waitDone
	}
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

	emu := newPaneEmulator(cols, rows, scrollbackLines)
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
	p.setRuntimeProcessInfo(nil, nil)
	wireScrollbackCallbacks(p)
	p.startActor()
	// Start drain goroutine for emulator responses (DA replies etc.)
	// that would otherwise block the emulator's pipe.
	go p.drainResponsesDiscard(emu, p.drainLoopDone)
	return p
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
	if _, shellCmd := p.runtimeProcessInfo(); shellCmd != "" {
		shell = shellPath(shellCmd, p.ProcessPid())
	}
	cmd, ptmx, err := startPaneShell(shell, p.ID, sessionName, dir, cols, rows, "")
	if err != nil {
		return err
	}
	emu := newPaneEmulator(cols, rows, p.scrollbackLimit)

	p.suppressExit.Store(true)
	old := p.detachForRespawn()
	p.stopDetachedForRespawn(old)
	p.withActor(func() {
		p.ptmx = ptmx
		p.cmd = cmd
		p.process = nil
		p.setRuntimeProcessInfo(cmd, nil)
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
	return nil
}

type detachedPaneState struct {
	ptmx          *os.File
	cmd           *exec.Cmd
	process       *os.Process
	emulator      TerminalEmulator
	readLoopDone  chan struct{}
	drainLoopDone chan struct{}
	waitLoopDone  chan struct{}
	exitDone      chan struct{}
}

func (p *Pane) detachRuntimeState() detachedPaneState {
	return paneActorValue(p, func() detachedPaneState {
		state := detachedPaneState{
			ptmx:          p.ptmx,
			cmd:           p.cmd,
			process:       p.process,
			emulator:      p.emulator,
			readLoopDone:  p.readLoopDone,
			drainLoopDone: p.drainLoopDone,
			waitLoopDone:  p.waitLoopDone,
			exitDone:      p.exitDone,
		}
		p.ptmx = nil
		p.cmd = nil
		p.process = nil
		p.readLoopDone = nil
		p.drainLoopDone = nil
		p.waitLoopDone = nil
		return state
	})
}

func (p *Pane) detachForRespawn() detachedPaneState {
	return p.detachRuntimeState()
}

func (p *Pane) stopDetachedForRespawn(state detachedPaneState) {
	proc := state.process
	if proc == nil && state.cmd != nil {
		proc = state.cmd.Process
	}
	if proc != nil {
		_ = proc.Signal(syscall.SIGHUP)
	}
	if state.ptmx != nil {
		_ = state.ptmx.Close()
	}
	if state.readLoopDone != nil {
		<-state.readLoopDone
	}
	p.waitForDetachedProcessExit(state, proc, 2*time.Second)
	if state.emulator != nil {
		_ = state.emulator.Close()
	}
	if state.drainLoopDone != nil {
		<-state.drainLoopDone
	}
}

func (p *Pane) waitForDetachedProcessExit(state detachedPaneState, proc *os.Process, timeout time.Duration) {
	if proc == nil {
		return
	}
	if state.waitLoopDone != nil {
		select {
		case <-state.exitDone:
		case <-time.After(timeout):
			_ = proc.Signal(syscall.SIGKILL)
			<-state.exitDone
		}
		<-state.waitLoopDone
		return
	}
	if state.cmd == nil {
		select {
		case <-state.exitDone:
		case <-time.After(timeout):
			_ = proc.Signal(syscall.SIGKILL)
			<-state.exitDone
		}
		return
	}

	waitDone := make(chan struct{})
	cmd := state.cmd
	exitDone := state.exitDone
	go func() {
		_ = cmd.Wait()
		close(exitDone)
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(timeout):
		_ = proc.Signal(syscall.SIGKILL)
		<-waitDone
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
	if _, shellCmd := p.runtimeProcessInfo(); shellCmd != "" {
		shell = shellPath(shellCmd, p.ProcessPid())
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
