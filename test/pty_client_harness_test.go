package test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/render"
)

// ptyClientHarness runs a real interactive amux client in a PTY so tests can
// observe terminal mode sequences and decide how a terminal would encode
// logical keypresses in response.
type ptyClientHarness struct {
	tb   testing.TB
	cmd  *exec.Cmd
	ptmx *os.File

	mu       sync.Mutex
	output   []byte
	waitErr  error
	updateCh chan struct{}
	exited   chan struct{}
}

func newPTYClientHarness(tb testing.TB, server *ServerHarness, envVars ...string) *ptyClientHarness {
	tb.Helper()
	return newPTYClientHarnessWithReadyOutput(tb, server, "[pane-1]", envVars...)
}

func newPTYClientHarnessWithReadyOutput(tb testing.TB, server *ServerHarness, readySubstr string, envVars ...string) *ptyClientHarness {
	tb.Helper()
	return newPTYClientHarnessForSessionWithReadyOutput(tb, server.session, server.home, server.coverDir, readySubstr, envVars...)
}

func newPTYClientHarnessForSession(tb testing.TB, session, home, coverDir string, envVars ...string) *ptyClientHarness {
	tb.Helper()

	cmd := exec.Command(amuxBin, "-s", session)
	env := upsertEnv(os.Environ(), "HOME", home)
	env = upsertEnv(env, "TERM", "xterm-256color")
	env = upsertEnv(env, "AMUX_NO_WATCH", "1")
	if coverDir != "" {
		env = upsertEnv(env, "GOCOVERDIR", coverDir)
	}
	env = append(env, envVars...)
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		tb.Fatalf("starting PTY client: %v", err)
	}

	h := &ptyClientHarness{
		tb:       tb,
		cmd:      cmd,
		ptmx:     ptmx,
		updateCh: make(chan struct{}, 1),
		exited:   make(chan struct{}),
	}

	go h.readLoop()
	go func() {
		err := cmd.Wait()
		h.mu.Lock()
		h.waitErr = err
		h.mu.Unlock()
		close(h.exited)
	}()

	tb.Cleanup(h.cleanup)

	return h
}

func newPTYClientHarnessForSessionWithReadyOutput(tb testing.TB, session, home, coverDir, readySubstr string, envVars ...string) *ptyClientHarness {
	tb.Helper()

	h := newPTYClientHarnessForSession(tb, session, home, coverDir, envVars...)

	if !h.waitForOutput(readySubstr, 10*time.Second) {
		tb.Fatalf("PTY client did not render ready output %q\nOutput:\n%s", readySubstr, h.outputString())
	}

	return h
}

func (h *ptyClientHarness) cleanup() {
	select {
	case <-h.exited:
	default:
		h.detach()
		if !h.waitExited(3*time.Second) && h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
			<-h.exited
		}
	}
	_ = h.ptmx.Close()
}

func (h *ptyClientHarness) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := h.ptmx.Read(buf)
		if n > 0 {
			h.mu.Lock()
			h.output = append(h.output, buf[:n]...)
			h.mu.Unlock()
			select {
			case h.updateCh <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *ptyClientHarness) outputBytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.output...)
}

func (h *ptyClientHarness) outputString() string {
	return string(h.outputBytes())
}

func (h *ptyClientHarness) screen(width, height int) string {
	h.tb.Helper()
	term := vt.NewSafeEmulator(width, height)
	if _, err := term.Write(h.outputBytes()); err != nil {
		h.tb.Fatalf("replay PTY output: %v", err)
	}
	return render.MaterializeGrid(term.Render(), width, height)
}

func (h *ptyClientHarness) waitError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waitErr
}

func (h *ptyClientHarness) waitForOutput(substr string, timeout time.Duration) bool {
	h.tb.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if bytes.Contains(h.outputBytes(), []byte(substr)) {
			return true
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return false
		}
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		select {
		case <-h.updateCh:
		case <-h.exited:
			return bytes.Contains(h.outputBytes(), []byte(substr))
		case <-time.After(wait):
		}
	}
}

func (h *ptyClientHarness) kittyKeyboardEnabled() bool {
	out := h.outputBytes()
	enableAt := bytes.LastIndex(out, []byte(render.KittyKeyboardEnable))
	disableAt := bytes.LastIndex(out, []byte(render.KittyKeyboardDisable))
	return enableAt >= 0 && enableAt > disableAt
}

func (h *ptyClientHarness) write(data []byte) {
	h.tb.Helper()
	if _, err := h.ptmx.Write(data); err != nil {
		h.tb.Fatalf("writing to PTY client: %v", err)
	}
}

func (h *ptyClientHarness) sendText(text string) {
	h.write([]byte(text))
}

func (h *ptyClientHarness) sendCtrl(letter byte) {
	h.tb.Helper()
	if letter < 'a' || letter > 'z' {
		h.tb.Fatalf("sendCtrl only supports lowercase ASCII letters, got %q", letter)
	}
	if h.kittyKeyboardEnabled() {
		h.write([]byte(fmt.Sprintf("\x1b[%d;5u", letter)))
		return
	}
	h.write([]byte{letter - 'a' + 1})
}

func (h *ptyClientHarness) detach() {
	h.tb.Helper()
	h.sendCtrl('a')
	h.sendText("d")
}

func (h *ptyClientHarness) waitExited(timeout time.Duration) bool {
	h.tb.Helper()
	select {
	case <-h.exited:
		return true
	case <-time.After(timeout):
		return false
	}
}
