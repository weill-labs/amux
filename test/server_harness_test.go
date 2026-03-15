package test

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// ServerHarness starts only the inner amux server daemon. All interaction
// is via CLI commands over the Unix socket — no client process, no tmux.
// CLI commands are synchronous: after runCmd("split") returns, capture()
// immediately reflects the split. Zero polling, zero time.Sleep.
type ServerHarness struct {
	t       *testing.T
	session string
	cmd     *exec.Cmd
}

// newServerHarness starts a server daemon with a unique session name,
// waits for the ready signal, and seeds the first pane. Safe for parallel tests.
func newServerHarness(t *testing.T) *ServerHarness {
	t.Helper()
	var b [4]byte
	rand.Read(b[:])
	session := fmt.Sprintf("t-%x", b)

	// Create pipe for the server's ready signal.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating ready pipe: %v", err)
	}

	cmd := exec.Command(amuxBin, "_server", session)
	cmd.ExtraFiles = []*os.File{writePipe} // fd 3 in child
	cmd.Env = append(os.Environ(), "AMUX_READY_FD=3", "AMUX_NO_WATCH=1")

	logDir := server.SocketDir()
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, session+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("opening log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		readPipe.Close()
		writePipe.Close()
		t.Fatalf("starting server: %v", err)
	}
	writePipe.Close() // close write end in parent
	logFile.Close()

	// Block until server signals readiness (after net.Listen succeeds).
	readPipe.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	n, err := readPipe.Read(buf)
	readPipe.Close()
	if err != nil || !strings.Contains(string(buf[:n]), "ready") {
		cmd.Process.Kill()
		t.Fatalf("server ready signal not received: err=%v, buf=%q", err, string(buf[:n]))
	}

	h := &ServerHarness{t: t, session: session, cmd: cmd}
	t.Cleanup(h.cleanup)

	// Seed the first pane by sending a temporary Attach message.
	h.seedPane()

	return h
}

// seedPane creates the first pane+window by connecting as a client, reading
// the layout response (which confirms pane creation), then disconnecting.
func (h *ServerHarness) seedPane() {
	h.t.Helper()
	sockPath := server.SocketPath(h.session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		h.t.Fatalf("seeding pane: %v", err)
	}
	defer conn.Close()

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: h.session,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		h.t.Fatalf("seeding pane: writing attach: %v", err)
	}

	// Read the layout message — confirms the server created the pane.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := server.ReadMsg(conn)
	if err != nil {
		h.t.Fatalf("seeding pane: reading layout: %v", err)
	}
	if msg.Type != server.MsgTypeLayout {
		h.t.Fatalf("seeding pane: expected layout (type %d), got type %d", server.MsgTypeLayout, msg.Type)
	}
}

// cleanup sends SIGTERM for graceful shutdown (coverage flush), then cleans
// up the socket and log files.
func (h *ServerHarness) cleanup() {
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			h.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			h.cmd.Process.Kill()
		}
	}
	socketDir := server.SocketDir()
	os.Remove(filepath.Join(socketDir, h.session))
	os.Remove(filepath.Join(socketDir, h.session+".log"))
}

// ---------------------------------------------------------------------------
// CLI command helpers — all synchronous, zero polling
// ---------------------------------------------------------------------------

// runCmd executes an amux CLI command targeting this test's session.
func (h *ServerHarness) runCmd(args ...string) string {
	h.t.Helper()
	cmdArgs := append([]string{"-s", h.session}, args...)
	out, _ := exec.Command(amuxBin, cmdArgs...).CombinedOutput()
	return string(out)
}

// capture returns the server-side composited screen (plain text 2D grid).
func (h *ServerHarness) capture() string {
	h.t.Helper()
	return h.runCmd("capture")
}

// captureLines returns the capture output split into rows.
func (h *ServerHarness) captureLines() []string {
	h.t.Helper()
	return strings.Split(h.capture(), "\n")
}

// captureContentLines returns capture lines excluding the global bar.
func (h *ServerHarness) captureContentLines() []string {
	h.t.Helper()
	var out []string
	for _, line := range h.captureLines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// captureVerticalBorderCol finds a consistent vertical border column.
func (h *ServerHarness) captureVerticalBorderCol() int {
	h.t.Helper()
	return findVerticalBorderCol(h.captureContentLines())
}

// assertScreen fails the test if fn returns false for the current screen.
func (h *ServerHarness) assertScreen(msg string, fn func(string) bool) {
	h.t.Helper()
	screen := h.capture()
	if !fn(screen) {
		h.t.Errorf("%s\nScreen:\n%s", msg, screen)
	}
}

// globalBar returns the global bar line from the capture.
func (h *ServerHarness) globalBar() string {
	h.t.Helper()
	for _, line := range h.captureLines() {
		if isGlobalBar(line) {
			return line
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Synchronization primitives — zero polling replacements
// ---------------------------------------------------------------------------

// waitFor blocks until substr appears in the named pane's screen content.
// Uses the server's wait-for command (blocking, zero polling).
func (h *ServerHarness) waitFor(pane, substr string) {
	h.t.Helper()
	out := h.runCmd("wait-for", pane, substr, "--timeout", "5s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.t.Fatalf("wait-for %q in %s: %s\ncapture:\n%s", substr, pane, strings.TrimSpace(out), h.capture())
	}
}

// generation returns the current layout generation counter.
func (h *ServerHarness) generation() uint64 {
	h.t.Helper()
	out := strings.TrimSpace(h.runCmd("generation"))
	n, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		h.t.Fatalf("parsing generation: %v (output: %q)", err, out)
	}
	return n
}

// waitLayout blocks until the layout generation exceeds afterGen.
func (h *ServerHarness) waitLayout(afterGen uint64) {
	h.t.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		h.t.Fatalf("wait-layout timed out after generation %d\ncapture:\n%s", afterGen, h.capture())
	}
}

// ---------------------------------------------------------------------------
// Split helpers — synchronous via CLI, no keybinding simulation
// ---------------------------------------------------------------------------

func (h *ServerHarness) splitV() {
	h.t.Helper()
	out := h.runCmd("split")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.t.Fatalf("splitV failed: %s", out)
	}
}

func (h *ServerHarness) splitH() {
	h.t.Helper()
	out := h.runCmd("split", "v")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.t.Fatalf("splitH failed: %s", out)
	}
}

func (h *ServerHarness) splitRootV() {
	h.t.Helper()
	out := h.runCmd("split", "root")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.t.Fatalf("splitRootV failed: %s", out)
	}
}

func (h *ServerHarness) splitRootH() {
	h.t.Helper()
	out := h.runCmd("split", "root", "v")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.t.Fatalf("splitRootH failed: %s", out)
	}
}

// ---------------------------------------------------------------------------
// Pane interaction — via CLI send-keys, no tmux
// ---------------------------------------------------------------------------

// sendKeys sends keystrokes to a specific pane via the server's send-keys command.
func (h *ServerHarness) sendKeys(pane string, keys ...string) {
	h.t.Helper()
	args := append([]string{"send-keys", pane}, keys...)
	out := h.runCmd(args...)
	if strings.Contains(out, "not found") {
		h.t.Fatalf("sendKeys to %s: %s", pane, strings.TrimSpace(out))
	}
}
