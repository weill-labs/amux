package test

import (
	"crypto/rand"
	"fmt"
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

// ServerHarness starts the inner amux server daemon and attaches a headless
// client. The client maintains local emulators and responds to capture
// requests — making it the rendering source of truth, same as a real client.
// CLI commands are synchronous: after runCmd("split") returns, capture()
// immediately reflects the split. Zero polling, zero time.Sleep.
type ServerHarness struct {
	tb       testing.TB
	session  string
	cmd      *exec.Cmd
	coverDir string          // per-test GOCOVERDIR subdirectory (avoids coverage metadata races)
	client   *headlessClient // attached headless client for capture
}

// newServerHarnessWithSize starts a server harness with a custom terminal size.
func newServerHarnessWithSize(tb testing.TB, cols, rows int) *ServerHarness {
	return newServerHarnessWithConfig(tb, cols, rows, "")
}

// newServerHarness starts a server daemon with a unique session name,
// waits for the ready signal, and seeds the first pane. Safe for parallel tests.
func newServerHarness(tb testing.TB) *ServerHarness {
	return newServerHarnessImpl(tb, 80, 24)
}

func newServerHarnessImpl(tb testing.TB, cols, rows int) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithConfig(tb, cols, rows, "")
}

// newServerHarnessWithConfig starts a server with a custom config file.
// The config is written to a temp file and passed via AMUX_CONFIG.
// Pass an empty configContent to start with the default (no) config.
func newServerHarnessWithConfig(tb testing.TB, cols, rows int, configContent string) *ServerHarness {
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

	// Write config to a temp file and pass via AMUX_CONFIG if provided.
	if configContent != "" {
		configDir := tb.TempDir()
		configPath := filepath.Join(configDir, "config.toml")
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			tb.Fatalf("writing config: %v", err)
		}
		env = append(env, "AMUX_CONFIG="+configPath)
	}

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

	// Attach a headless client — seeds the first pane and stays connected
	// so capture requests route through client-side emulators.
	sockPath := server.SocketPath(session)
	client, err := newHeadlessClient(sockPath, session, cols, rows)
	if err != nil {
		cmd.Process.Kill()
		tb.Fatalf("attaching headless client: %v", err)
	}
	h.client = client

	return h
}

// cleanup detaches the headless client, sends SIGTERM for graceful shutdown
// (coverage flush), then cleans up the socket and log files.
func (h *ServerHarness) cleanup() {
	if h.client != nil {
		h.client.close()
	}
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
	return captureJSONFor(h.tb, h.runCmd)
}

// jsonPane finds a pane by name in a CaptureJSON, or fails the test.
// Also fails if Position is nil (full-screen captures always set it).
func (h *ServerHarness) jsonPane(capture proto.CaptureJSON, name string) proto.CapturePane {
	h.tb.Helper()
	return jsonPaneFor(h.tb, capture, name)
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
	return activePaneNameFor(h.tb, h.captureJSON())
}

// globalBar returns the global bar line from the capture.
func (h *ServerHarness) globalBar() string {
	h.tb.Helper()
	return globalBarFromLines(h.captureLines())
}

// ---------------------------------------------------------------------------
// Synchronization primitives — zero polling replacements
// ---------------------------------------------------------------------------

// waitFor blocks until substr appears in the named pane's screen content.
// Uses the server's wait-for command (blocking, zero polling).
func (h *ServerHarness) waitFor(pane, substr string) {
	h.tb.Helper()
	h.waitForTimeout(pane, substr, "10s")
}

// waitForTimeout is like waitFor but with a custom timeout.
func (h *ServerHarness) waitForTimeout(pane, substr, timeout string) {
	h.tb.Helper()
	out := h.runCmd("wait-for", pane, substr, "--timeout", timeout)
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

// startLongSleep starts a background sleep in the named pane and waits for a
// sentinel marker to confirm the child process exists. This is more reliable
// than waitBusy under parallel test load because it uses output-based detection
// (waitFor) instead of pgrep.
func (h *ServerHarness) startLongSleep(pane string) {
	h.tb.Helper()
	h.sendKeys(pane, `sleep 300 & printf '\x42\x55\x53\x59_OK\n'; wait`, "Enter")
	h.waitFor(pane, "BUSY_OK")
}

// waitIdle blocks until the named pane becomes idle (no activity for DefaultIdleTimeout).
// Uses the server's wait-idle command (event-based, zero polling).
func (h *ServerHarness) waitIdle(pane string) {
	h.tb.Helper()
	out := h.runCmd("wait-idle", pane, "--timeout", "10s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-idle %s: %s\ncapture:\n%s", pane, strings.TrimSpace(out), h.capture())
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
	h.waitLayoutTimeout(afterGen, "5s")
}

// waitLayoutTimeout is like waitLayout but with a custom timeout.
func (h *ServerHarness) waitLayoutTimeout(afterGen uint64, timeout string) {
	h.tb.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	if strings.Contains(out, "timeout") {
		h.tb.Fatalf("wait-layout timed out after generation %d\ncapture:\n%s", afterGen, h.capture())
	}
}

// waitLayoutOrTimeout is like waitLayoutTimeout but returns false on timeout
// instead of failing the test. Used in polling loops where timeout is a valid
// exit condition rather than a test failure.
func (h *ServerHarness) waitLayoutOrTimeout(afterGen uint64, timeout string) bool {
	h.tb.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	return !strings.Contains(out, "timeout")
}

// ---------------------------------------------------------------------------
// Split helpers — synchronous via CLI, no keybinding simulation
// ---------------------------------------------------------------------------

// doSplit runs a split CLI command, waits for the layout generation to bump
// (ensuring the headless client has received the broadcast), and fails the
// test if the command errors.
func (h *ServerHarness) doSplit(args ...string) {
	h.tb.Helper()
	gen := h.generation()
	cmdArgs := append([]string{"split"}, args...)
	out := h.runCmd(cmdArgs...)
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.tb.Fatalf("split %v failed: %s", args, out)
	}
	h.waitLayout(gen)
}

// doFocus runs a focus command and waits for the layout generation to bump
// (ensuring the headless client has received the broadcast).
func (h *ServerHarness) doFocus(args ...string) string {
	h.tb.Helper()
	gen := h.generation()
	cmdArgs := append([]string{"focus"}, args...)
	out := h.runCmd(cmdArgs...)
	h.waitLayout(gen)
	return out
}

func (h *ServerHarness) splitV()     { h.tb.Helper(); h.doSplit("v") }
func (h *ServerHarness) splitH()     { h.tb.Helper(); h.doSplit() }
func (h *ServerHarness) splitRootV() { h.tb.Helper(); h.doSplit("root", "v") }
func (h *ServerHarness) splitRootH() { h.tb.Helper(); h.doSplit("root") }

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
