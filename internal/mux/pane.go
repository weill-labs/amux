package mux

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/creack/pty"
)

// DefaultHost is the host value for locally-running panes.
const DefaultHost = "local"

// PaneNameFormat is the format string for auto-assigned pane names.
const PaneNameFormat = "pane-%d"

// PaneMeta holds amux metadata for a pane.
type PaneMeta struct {
	Name      string
	Host      string
	Task      string
	Remote    string
	Color     string
	Minimized bool
	RestoreH  int // saved height before minimize
}

// Pane manages a PTY, its terminal emulator, and metadata.
type Pane struct {
	ID   uint32
	Meta PaneMeta

	ptmx     *os.File
	cmd      *exec.Cmd
	process  *os.Process // set for restored panes (where cmd is nil)
	emulator TerminalEmulator

	closed       atomic.Bool
	drainStarted bool
	onOutput     func(paneID uint32, data []byte)
	onExit       func(paneID uint32)
}

// NewPane creates a new pane running the user's shell but does NOT start
// the read/drain/wait goroutines. Call Start() after releasing any locks
// that the onOutput/onExit callbacks might need.
func NewPane(id uint32, meta PaneMeta, cols, rows int, onOutput func(uint32, []byte), onExit func(uint32)) (*Pane, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"AMUX_PANE=1",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, err
	}

	emu := NewVTEmulator(cols, rows)

	return &Pane{
		ID:       id,
		Meta:     meta,
		ptmx:     ptmx,
		cmd:      cmd,
		emulator: emu,
		onOutput: onOutput,
		onExit:   onExit,
	}, nil
}

// RestorePane creates a pane from inherited file descriptors after server reload.
// It wraps an existing PTY master FD and finds the running shell process by PID.
// No new shell is spawned — the existing shell survives the exec.
// The drain goroutine starts immediately to prevent deadlock during screen replay.
func RestorePane(id uint32, meta PaneMeta, ptmxFd, pid, cols, rows int, onOutput func(uint32, []byte), onExit func(uint32)) (*Pane, error) {
	ptmx := os.NewFile(uintptr(ptmxFd), fmt.Sprintf("ptmx-%d", id))
	if ptmx == nil {
		return nil, fmt.Errorf("invalid FD %d for pane %d", ptmxFd, id)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("finding process %d for pane %d: %w", pid, id, err)
	}

	emu := NewVTEmulator(cols, rows)

	p := &Pane{
		ID:           id,
		Meta:         meta,
		ptmx:         ptmx,
		process:      proc,
		emulator:     emu,
		drainStarted: true,
		onOutput:     onOutput,
		onExit:       onExit,
	}

	// Start drain immediately so screen replay doesn't deadlock
	// on the emulator's unbuffered response pipe.
	go p.drainResponses()

	return p, nil
}

// PtmxFd returns the file descriptor number for the PTY master.
func (p *Pane) PtmxFd() int {
	return int(p.ptmx.Fd())
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
	p.emulator.Write([]byte(data))
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

// readLoop reads PTY output, feeds the emulator, and notifies the callback.
func (p *Pane) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// Feed emulator for screen state tracking (enables reattach)
			p.emulator.Write(data)

			if p.onOutput != nil {
				p.onOutput(p.ID, data)
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
func (p *Pane) Write(data []byte) (int, error) {
	return p.ptmx.Write(data)
}

// Resize changes the PTY and emulator dimensions.
func (p *Pane) Resize(cols, rows int) error {
	p.emulator.Resize(cols, rows)
	return pty.Setsize(p.ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// RenderScreen returns the current screen state as ANSI output.
func (p *Pane) RenderScreen() string {
	return RenderWithCursor(p.emulator)
}

// CursorPos returns the cursor position within this pane (0-indexed).
func (p *Pane) CursorPos() (col, row int) {
	return p.emulator.CursorPosition()
}

// Output returns the last N lines of visible pane content from the emulator.
func (p *Pane) Output(lines int) string {
	rendered := p.emulator.Render()
	all := strings.Split(rendered, "\n")
	var result []string
	for i := len(all) - 1; i >= 0 && len(result) < lines; i-- {
		trimmed := strings.TrimRight(all[i], " ")
		plain := StripANSI(trimmed)
		if plain != "" {
			result = append([]string{plain}, result...)
		}
	}
	return strings.Join(result, "\n")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]`)

// StripANSI removes ANSI escape sequences from a string.
func StripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Close terminates the pane's shell and PTY.
func (p *Pane) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	if p.cmd != nil {
		p.cmd.Process.Signal(syscall.SIGHUP)
	} else if p.process != nil {
		p.process.Signal(syscall.SIGHUP)
	}
	return p.ptmx.Close()
}
