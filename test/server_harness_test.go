package test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// ServerHarness starts only the inner amux server daemon. All interaction
// is via CLI commands over the Unix socket — no client process, no tmux.
// CLI commands are synchronous: after runCmd("split") returns, capture()
// immediately reflects the split. Zero polling, zero time.Sleep.
type ServerHarness struct {
	tb       testing.TB
	session  string
	cmd      *exec.Cmd
	coverDir string // per-test GOCOVERDIR subdirectory (avoids coverage metadata races)
}

// newServerHarness starts a server daemon with a unique session name,
// waits for the ready signal, and seeds the first pane. Safe for parallel tests.
func newServerHarness(tb testing.TB) *ServerHarness {
	tb.Helper()
	var b [4]byte
	rand.Read(b[:])
	session := fmt.Sprintf("t-%x", b)

	// Create pipe for the server's ready signal.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		tb.Fatalf("creating ready pipe: %v", err)
	}

	cmd := exec.Command(amuxBin, "_server", session)
	cmd.ExtraFiles = []*os.File{writePipe} // fd 3 in child
	env := append(os.Environ(), "AMUX_READY_FD=3", "AMUX_NO_WATCH=1")

	// Give each test its own GOCOVERDIR subdirectory. Without this, all
	// parallel amux processes (servers + short-lived CLI commands) race on
	// covmeta.* file renames in the shared directory, causing intermittent
	// "rename: no such file or directory" errors that corrupt CLI output.
	var coverDir string
	if gocoverDir != "" {
		coverDir = filepath.Join(gocoverDir, session)
		os.MkdirAll(coverDir, 0755)
		for i, e := range env {
			if strings.HasPrefix(e, "GOCOVERDIR=") {
				env[i] = "GOCOVERDIR=" + coverDir
				break
			}
		}
	}
	cmd.Env = env

	logDir := server.SocketDir()
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, session+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		tb.Fatalf("opening log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		readPipe.Close()
		writePipe.Close()
		tb.Fatalf("starting server: %v", err)
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
		tb.Fatalf("server ready signal not received: err=%v, buf=%q", err, string(buf[:n]))
	}

	h := &ServerHarness{tb: tb, session: session, cmd: cmd, coverDir: coverDir}
	tb.Cleanup(h.cleanup)

	// Seed the first pane by sending a temporary Attach message.
	h.seedPane()

	return h
}

// seedPane creates the first pane+window by connecting as a client, reading
// the layout response (which confirms pane creation), then disconnecting.
func (h *ServerHarness) seedPane() {
	h.tb.Helper()
	sockPath := server.SocketPath(h.session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		h.tb.Fatalf("seeding pane: %v", err)
	}
	defer conn.Close()

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: h.session,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		h.tb.Fatalf("seeding pane: writing attach: %v", err)
	}

	// Read the layout message — confirms the server created the pane.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := server.ReadMsg(conn)
	if err != nil {
		h.tb.Fatalf("seeding pane: reading layout: %v", err)
	}
	if msg.Type != server.MsgTypeLayout {
		h.tb.Fatalf("seeding pane: expected layout (type %d), got type %d", server.MsgTypeLayout, msg.Type)
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
	h.tb.Helper()
	cmdArgs := append([]string{"-s", h.session}, args...)
	cmd := exec.Command(amuxBin, cmdArgs...)
	if h.coverDir != "" {
		cmd.Env = append(os.Environ(), "GOCOVERDIR="+h.coverDir)
	}
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// capture returns the server-side composited screen (plain text 2D grid).
func (h *ServerHarness) capture() string {
	h.tb.Helper()
	return h.runCmd("capture")
}

// captureLines returns the capture output split into rows.
func (h *ServerHarness) captureLines() []string {
	h.tb.Helper()
	return strings.Split(h.capture(), "\n")
}

// captureContentLines returns capture lines excluding the global bar.
func (h *ServerHarness) captureContentLines() []string {
	h.tb.Helper()
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
	h.tb.Helper()
	return findVerticalBorderCol(h.captureContentLines())
}

// assertScreen fails the test if fn returns false for the current screen.
func (h *ServerHarness) assertScreen(msg string, fn func(string) bool) {
	h.tb.Helper()
	screen := h.capture()
	if !fn(screen) {
		h.tb.Errorf("%s\nScreen:\n%s", msg, screen)
	}
}

// captureJSON returns the full-screen JSON capture as a parsed struct.
func (h *ServerHarness) captureJSON() proto.CaptureJSON {
	h.tb.Helper()
	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		h.tb.Fatalf("captureJSON: %v\nraw: %s", err, out)
	}
	return capture
}

// jsonPane finds a pane by name in a CaptureJSON, or fails the test.
// Also fails if Position is nil (full-screen captures always set it).
func (h *ServerHarness) jsonPane(capture proto.CaptureJSON, name string) proto.CapturePane {
	h.tb.Helper()
	for _, p := range capture.Panes {
		if p.Name == name {
			if p.Position == nil {
				h.tb.Fatalf("pane %q has nil Position in full-screen capture", name)
			}
			return p
		}
	}
	h.tb.Fatalf("pane %q not found in JSON capture", name)
	return proto.CapturePane{}
}

// assertActive asserts that the named pane is the active pane.
func (h *ServerHarness) assertActive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if !p.Active {
		h.tb.Errorf("%s should be active, but is not", name)
	}
}

// assertInactive asserts that the named pane is not the active pane.
func (h *ServerHarness) assertInactive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if p.Active {
		h.tb.Errorf("%s should be inactive, but is active", name)
	}
}

// activePaneName returns the name of the active pane from JSON capture.
func (h *ServerHarness) activePaneName() string {
	h.tb.Helper()
	c := h.captureJSON()
	for _, p := range c.Panes {
		if p.Active {
			return p.Name
		}
	}
	h.tb.Fatal("no active pane found")
	return ""
}

// globalBar returns the global bar line from the capture.
func (h *ServerHarness) globalBar() string {
	h.tb.Helper()
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
	h.tb.Helper()
	out := h.runCmd("wait-for", pane, substr, "--timeout", "5s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-for %q in %s: %s\ncapture:\n%s", substr, pane, strings.TrimSpace(out), h.capture())
	}
}

// waitBusy blocks until the named pane has child processes (a command is running).
// Uses the server's wait-busy command (blocking, zero polling).
func (h *ServerHarness) waitBusy(pane string) {
	h.tb.Helper()
	out := h.runCmd("wait-busy", pane, "--timeout", "5s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-busy %s: %s\ncapture:\n%s", pane, strings.TrimSpace(out), h.capture())
	}
}

// generation returns the current layout generation counter.
func (h *ServerHarness) generation() uint64 {
	h.tb.Helper()
	out := strings.TrimSpace(h.runCmd("generation"))
	n, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		h.tb.Fatalf("parsing generation: %v (output: %q)", err, out)
	}
	return n
}

// waitLayout blocks until the layout generation exceeds afterGen.
func (h *ServerHarness) waitLayout(afterGen uint64) {
	h.tb.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		h.tb.Fatalf("wait-layout timed out after generation %d\ncapture:\n%s", afterGen, h.capture())
	}
}

// ---------------------------------------------------------------------------
// Split helpers — synchronous via CLI, no keybinding simulation
// ---------------------------------------------------------------------------

// doSplit runs a split CLI command and fails the test if it errors.
func (h *ServerHarness) doSplit(args ...string) {
	h.tb.Helper()
	cmdArgs := append([]string{"split"}, args...)
	out := h.runCmd(cmdArgs...)
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.tb.Fatalf("split %v failed: %s", args, out)
	}
}

func (h *ServerHarness) splitV()     { h.tb.Helper(); h.doSplit() }
func (h *ServerHarness) splitH()     { h.tb.Helper(); h.doSplit("v") }
func (h *ServerHarness) splitRootV() { h.tb.Helper(); h.doSplit("root") }
func (h *ServerHarness) splitRootH() { h.tb.Helper(); h.doSplit("root", "v") }

// ---------------------------------------------------------------------------
// Pane interaction — via CLI send-keys, no tmux
// ---------------------------------------------------------------------------

// sendKeys sends keystrokes to a specific pane via the server's send-keys command.
func (h *ServerHarness) sendKeys(pane string, keys ...string) {
	h.tb.Helper()
	args := append([]string{"send-keys", pane}, keys...)
	out := h.runCmd(args...)
	if strings.Contains(out, "not found") {
		h.tb.Fatalf("sendKeys to %s: %s", pane, strings.TrimSpace(out))
	}
}
