package test

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// AmuxHarness embeds a ServerHarness as the "outer" amux and launches an
// "inner" amux inside the outer pane. Keybinding simulation flows through
// the outer send-keys → inner client key handler → inner server.
//
// Synchronization:
//   - Layout changes: generation() + sendKeys() + waitLayout() (zero polling)
//   - Shell output: waitFor(substr, timeout) via outer wait-for (blocking)
//   - No time.Sleep for synchronization
type AmuxHarness struct {
	outer   *ServerHarness
	inner   string // inner session name
	tb      testing.TB
	session string // alias for inner, used by extractFrame
}

// newAmuxHarness starts an outer amux server, launches an inner amux inside
// the outer pane, and waits for the inner amux to render its first pane.
func newAmuxHarness(tb testing.TB, envVars ...string) *AmuxHarness {
	tb.Helper()
	outer := newServerHarness(tb)

	var b [4]byte
	rand.Read(b[:])
	inner := fmt.Sprintf("t-%x", b)

	h := &AmuxHarness{outer: outer, inner: inner, tb: tb, session: inner}

	// Export any extra environment variables before launching the inner amux.
	for _, ev := range envVars {
		outer.sendKeys("pane-1", "export "+ev, "Enter")
	}

	// Launch inner amux inside the outer pane.
	outer.sendKeys("pane-1", amuxBin+" -s "+inner, "Enter")

	// Wait for the inner amux client to render (status bar appears in outer
	// pane). Once the client has rendered, the inner server is guaranteed to
	// be accepting connections — no polling loop needed.
	outer.waitForTimeout("pane-1", "[pane-", "30s")

	tb.Cleanup(h.cleanup)
	return h
}

// cleanup detaches the inner client, then SIGTERMs the inner server.
// The outer harness cleanup runs via its own t.Cleanup.
func (h *AmuxHarness) cleanup() {
	// Detach inner client for graceful coverage flush.
	h.sendKeys("C-a", "d")

	// SIGTERM inner server.
	for _, pid := range h.innerServerPIDs() {
		if pid != "" {
			exec.Command("kill", pid).Run()
		}
	}
	h.waitInnerServerGone(3 * time.Second)

	// Clean up inner socket and log.
	socketDir := server.SocketDir()
	for _, suffix := range []string{"", ".log"} {
		exec.Command("rm", "-f", fmt.Sprintf("%s/%s%s", socketDir, h.inner, suffix)).Run()
	}
}

func (h *AmuxHarness) innerServerPIDs() []string {
	out, _ := exec.Command("pgrep", "-f", fmt.Sprintf("amux _server %s$", h.inner)).Output()
	var pids []string
	for _, pid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid != "" {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (h *AmuxHarness) waitInnerServerGone(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	socketPath := server.SocketPath(h.inner)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if len(h.innerServerPIDs()) == 0 {
			if _, err := os.Stat(socketPath); os.IsNotExist(err) {
				return
			}
		}
		<-ticker.C
	}
}

// ---------------------------------------------------------------------------
// Keybinding simulation — keys flow through the outer PTY to inner client
// ---------------------------------------------------------------------------

// sendKeys sends keystrokes to the inner amux client via the outer pane.
func (h *AmuxHarness) sendKeys(keys ...string) {
	h.tb.Helper()
	args := append([]string{"send-keys", "pane-1"}, keys...)
	h.outer.runCmd(args...)
}

// ---------------------------------------------------------------------------
// Synchronization — zero-polling primitives on the inner server
// ---------------------------------------------------------------------------

// waitFor blocks until substr appears in the outer pane's rendered content
// (which shows the inner client's full rendering including status lines).
func (h *AmuxHarness) waitFor(substr string, timeout time.Duration) bool {
	h.tb.Helper()
	out := h.outer.runCmd("wait-for", "pane-1", substr, "--timeout", timeout.String())
	return !strings.Contains(out, "timeout") && !strings.Contains(out, "not found")
}

// generation returns the inner server's layout generation counter.
func (h *AmuxHarness) generation() uint64 {
	h.tb.Helper()
	out := strings.TrimSpace(h.runCmd("generation"))
	n, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		h.tb.Fatalf("parsing inner generation: %v (output: %q)", err, out)
	}
	return n
}

// waitLayout blocks until the inner layout generation exceeds afterGen.
func (h *AmuxHarness) waitLayout(afterGen uint64) {
	h.tb.Helper()
	h.waitLayoutTimeout(afterGen, "5s")
}

// waitLayoutTimeout is like waitLayout but with a custom timeout.
func (h *AmuxHarness) waitLayoutTimeout(afterGen uint64, timeout string) {
	h.tb.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	if strings.Contains(out, "timeout") {
		h.tb.Fatalf("inner wait-layout timed out after generation %d\ncapture:\n%s", afterGen, h.capture())
	}
}

func (h *AmuxHarness) waitLayoutOrTimeout(afterGen uint64, timeout string) bool {
	h.tb.Helper()
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	return !strings.Contains(out, "timeout")
}

// waitDuration pauses for the given duration. Use for tests that verify
// real-time deadline expiry (e.g., repeat timeout). This is NOT for
// synchronization — use waitLayout/waitFor for that.
func (h *AmuxHarness) waitDuration(d time.Duration) {
	<-time.After(d)
}

// waitForFunc polls the inner compositor capture until fn returns true or
// timeout expires. Used for complex predicates that can't be expressed as
// a simple substring match. Prefer waitLayout for layout changes.
func (h *AmuxHarness) waitForFunc(fn func(string) bool, timeout time.Duration) bool {
	h.tb.Helper()
	deadline := time.Now().Add(timeout)
	gen := h.generation()
	for time.Now().Before(deadline) {
		if fn(h.capture()) {
			return true
		}
		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !h.waitLayoutOrTimeout(gen, waitFor.String()) {
			return fn(h.capture())
		}
		gen = h.generation()
	}
	return false
}

// waitForActive polls JSON capture until the named pane is active or timeout.
func (h *AmuxHarness) waitForActive(name string, timeout time.Duration) bool {
	h.tb.Helper()
	deadline := time.Now().Add(timeout)
	gen := h.generation()
	for time.Now().Before(deadline) {
		if h.activePaneName() == name {
			return true
		}
		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !h.waitLayoutOrTimeout(gen, waitFor.String()) {
			return h.activePaneName() == name
		}
		gen = h.generation()
	}
	return false
}

// ---------------------------------------------------------------------------
// Capture — inner compositor and outer rendered content
// ---------------------------------------------------------------------------

// capture returns the inner server's compositor output (plain text 2D grid).
// This is deterministic and does not depend on the client rendering pipeline.
func (h *AmuxHarness) capture() string {
	h.tb.Helper()
	return h.runCmd("capture")
}

// captureAmux is an alias for capture (inner compositor).
func (h *AmuxHarness) captureAmux() string {
	h.tb.Helper()
	return h.capture()
}

// captureOuter returns the outer pane's plain-text output — what the inner
// client actually wrote to its PTY, as seen by the outer emulator.
func (h *AmuxHarness) captureOuter() string {
	h.tb.Helper()
	return h.outer.runCmd("capture", "pane-1")
}

// captureOuterANSI returns the outer pane's ANSI-formatted output.
// Used for border color tests that need to inspect escape sequences.
func (h *AmuxHarness) captureOuterANSI() string {
	h.tb.Helper()
	return h.outer.runCmd("capture", "--ansi", "pane-1")
}

// ---------------------------------------------------------------------------
// Layout-aware screen helpers (same API as TmuxHarness for easy migration)
// ---------------------------------------------------------------------------

// captureAmuxLines returns the inner capture split into rows.
func (h *AmuxHarness) captureAmuxLines() []string {
	h.tb.Helper()
	return strings.Split(h.captureAmux(), "\n")
}

// captureAmuxContentLines returns inner capture lines excluding the global bar.
func (h *AmuxHarness) captureAmuxContentLines() []string {
	h.tb.Helper()
	var out []string
	for _, line := range h.captureAmuxLines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// lines returns inner capture rows, trimming trailing empty lines.
func (h *AmuxHarness) lines() []string {
	h.tb.Helper()
	raw := strings.Split(h.capture(), "\n")
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

// contentLines returns inner capture rows excluding the global bar.
func (h *AmuxHarness) contentLines() []string {
	h.tb.Helper()
	var out []string
	for _, line := range h.lines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// verticalBorderCol finds a consistent vertical border column in contentLines.
func (h *AmuxHarness) verticalBorderCol() int {
	h.tb.Helper()
	return findVerticalBorderCol(h.contentLines())
}

// captureAmuxVerticalBorderCol finds a vertical border in inner capture.
func (h *AmuxHarness) captureAmuxVerticalBorderCol() int {
	h.tb.Helper()
	return findVerticalBorderCol(h.captureAmuxContentLines())
}

// assertScreen fails the test if fn returns false for the inner capture.
func (h *AmuxHarness) assertScreen(msg string, fn func(string) bool) {
	h.tb.Helper()
	screen := h.capture()
	if !fn(screen) {
		h.tb.Errorf("%s\nScreen:\n%s", msg, screen)
	}
}

// captureJSON returns the full-screen JSON capture as a parsed struct.
func (h *AmuxHarness) captureJSON() proto.CaptureJSON {
	h.tb.Helper()
	return captureJSONFor(h.tb, h.runCmd)
}

// jsonPane finds a pane by name in a CaptureJSON, or fails the test.
// Also fails if Position is nil (full-screen captures always set it).
func (h *AmuxHarness) jsonPane(capture proto.CaptureJSON, name string) proto.CapturePane {
	h.tb.Helper()
	return jsonPaneFor(h.tb, capture, name)
}

// assertActive asserts that the named pane is the active pane.
func (h *AmuxHarness) assertActive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if !p.Active {
		h.tb.Errorf("%s should be active, but is not", name)
	}
}

// assertInactive asserts that the named pane is not the active pane.
func (h *AmuxHarness) assertInactive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if p.Active {
		h.tb.Errorf("%s should be inactive, but is active", name)
	}
}

// activePaneName returns the name of the active pane from JSON capture.
func (h *AmuxHarness) activePaneName() string {
	h.tb.Helper()
	return activePaneNameFor(h.tb, h.captureJSON())
}

// globalBar returns the global bar line from the inner capture.
func (h *AmuxHarness) globalBar() string {
	h.tb.Helper()
	return globalBarFromLines(h.lines())
}

// globalBarAmux returns the global bar from the inner capture.
func (h *AmuxHarness) globalBarAmux() string {
	h.tb.Helper()
	return h.globalBar()
}

// ---------------------------------------------------------------------------
// CLI commands — executed against the inner server
// ---------------------------------------------------------------------------

// runCmd runs an amux CLI command targeting the inner session.
func (h *AmuxHarness) runCmd(args ...string) string {
	h.tb.Helper()
	cmdArgs := append([]string{"-s", h.inner}, args...)
	cmd := exec.Command(amuxBin, cmdArgs...)
	if h.outer.coverDir != "" {
		cmd.Env = append(os.Environ(), "GOCOVERDIR="+h.outer.coverDir)
	}
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// ---------------------------------------------------------------------------
// Split helpers — keybinding splits with deterministic synchronization
// ---------------------------------------------------------------------------

func (h *AmuxHarness) doSplit(key string) {
	h.tb.Helper()
	gen := h.generation()
	h.sendKeys("C-a", key)
	h.waitLayout(gen)
}

func (h *AmuxHarness) splitV()     { h.tb.Helper(); h.doSplit("\\") }
func (h *AmuxHarness) splitH()     { h.tb.Helper(); h.doSplit("-") }
func (h *AmuxHarness) splitRootV() { h.tb.Helper(); h.doSplit("|") }
func (h *AmuxHarness) splitRootH() { h.tb.Helper(); h.doSplit("_") }

// ---------------------------------------------------------------------------
// Mouse helpers — raw SGR sequences via outer send-keys --hex
// ---------------------------------------------------------------------------

// sendMouseSGR sends a raw SGR mouse escape sequence to the inner client.
func (h *AmuxHarness) sendMouseSGR(button, x, y int, press bool) {
	h.tb.Helper()
	term := byte('M')
	if !press {
		term = byte('m')
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", button, x, y, term)
	var hexParts []string
	for _, b := range []byte(seq) {
		hexParts = append(hexParts, fmt.Sprintf("%02x", b))
	}
	hexStr := strings.Join(hexParts, "")
	h.outer.runCmd("send-keys", "pane-1", "--hex", hexStr)
}

// clickAt sends a left-click press+release at (x, y) using 1-based coordinates.
func (h *AmuxHarness) clickAt(x, y int) {
	h.tb.Helper()
	h.sendMouseSGR(0, x, y, true)
	time.Sleep(50 * time.Millisecond) // simulate human timing between press/release
	h.sendMouseSGR(0, x, y, false)
}

// dragBorder sends press → motion → release for a border drag.
func (h *AmuxHarness) dragBorder(startX, startY, endX, endY int) {
	h.tb.Helper()
	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(32, endX, endY, true) // motion = button + 32
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, endX, endY, false)
}

// scrollAt sends a scroll wheel event at (x, y).
func (h *AmuxHarness) scrollAt(x, y int, up bool) {
	h.tb.Helper()
	btn := 65
	if up {
		btn = 64
	}
	h.sendMouseSGR(btn, x, y, true)
}

// ---------------------------------------------------------------------------
// ANSI helpers (same API as TmuxHarness for border/color test migration)
// ---------------------------------------------------------------------------

// captureANSI returns the outer pane's ANSI-formatted capture.
func (h *AmuxHarness) captureANSI() string {
	h.tb.Helper()
	return h.captureOuterANSI()
}
