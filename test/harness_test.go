package test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// amuxBin is the path to the built amux binary, set in TestMain.
var amuxBin string

// gocoverDir is the directory for integration test coverage data.
var gocoverDir string

// gocoverOwned is true when TestMain created gocoverDir (vs. inheriting it).
var gocoverOwned bool

// buildAmux builds the amux binary at binPath. When GOCOVERDIR is set,
// the binary is built with -cover so it writes coverage data on exit.
func buildAmux(binPath string) error {
	args := []string{"build"}
	if os.Getenv("GOCOVERDIR") != "" {
		args = append(args, "-cover", "-covermode=atomic")
	}
	args = append(args, "-o", binPath, "..")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("building amux: %v\n%s", err, out)
	}
	return nil
}

func TestMain(m *testing.M) {
	// Skip if tmux isn't available
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Println("SKIP: tmux not found")
		os.Exit(0)
	}

	// Clean up orphaned test sessions from previous runs that may have
	// been killed by a timeout panic (t.Cleanup doesn't run on panic).
	cleanupStaleTestSessions()

	// Set up coverage output directory. If GOCOVERDIR is already set
	// (e.g. by CI), use it; otherwise create a temp dir.
	gocoverDir = os.Getenv("GOCOVERDIR")
	if gocoverDir == "" {
		if dir, err := os.MkdirTemp("", "amux-cov-*"); err == nil {
			gocoverDir = dir
			gocoverOwned = true
			os.Setenv("GOCOVERDIR", dir)
		}
	}

	// Build amux binary for testing
	tmp, err := os.MkdirTemp("", "amux-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	amuxBin = tmp + "/amux"
	if err := buildAmux(amuxBin); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	cleanupStaleTestSessions()

	// Convert coverage data to text profile
	if gocoverDir != "" {
		entries, _ := os.ReadDir(gocoverDir)
		if len(entries) > 0 {
			exec.Command("go", "tool", "covdata", "textfmt",
				"-i="+gocoverDir, "-o=integration-coverage.txt").Run()
		}
		if gocoverOwned {
			os.RemoveAll(gocoverDir)
		}
	}

	os.Exit(code)
}

// cleanupStaleTestSessions removes orphaned tmux sessions, amux server
// processes, sockets, and log files left behind by previous test runs
// that were killed by a timeout panic.
//
// Not safe if multiple `go test` invocations run concurrently — it may
// kill sessions belonging to the other run.
func cleanupStaleTestSessions() {
	// Kill tmux sessions matching the test naming convention (t- + 8 hex chars)
	out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if isTestSession(name) {
			exec.Command("tmux", "kill-session", "-t", name).Run()
		}
	}

	// Kill orphaned amux server processes, validating session name
	out, _ = exec.Command("pgrep", "-fl", "amux _server t-").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && isTestSession(fields[len(fields)-1]) {
			exec.Command("kill", fields[0]).Run()
		}
	}

	// Clean up stale sockets and log files
	socketDir := fmt.Sprintf("/tmp/amux-%d", os.Getuid())
	entries, _ := os.ReadDir(socketDir)
	for _, e := range entries {
		name := e.Name()
		if isTestSession(name) || (strings.HasSuffix(name, ".log") && isTestSession(strings.TrimSuffix(name, ".log"))) {
			os.Remove(filepath.Join(socketDir, name))
		}
	}
}

// isTestSession returns true if the name matches the test session convention: t- followed by 8 hex chars.
func isTestSession(name string) bool {
	if len(name) != 10 || name[:2] != "t-" {
		return false
	}
	_, err := hex.DecodeString(name[2:])
	return err == nil
}

// ---------------------------------------------------------------------------
// TmuxHarness — drives amux inside a tmux session for integration testing
// ---------------------------------------------------------------------------

type TmuxHarness struct {
	t       *testing.T
	session string // unique per-test session name (tmux + amux)
}

// newHarness creates a tmux session with a shell, runs amux with a unique
// session name, and waits for it to start. Safe for parallel tests.
func newHarness(t *testing.T) *TmuxHarness {
	t.Helper()
	var b [4]byte
	rand.Read(b[:])
	session := fmt.Sprintf("t-%x", b)

	h := &TmuxHarness{t: t, session: session}

	// Start tmux session with a shell
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session,
		"-x", "80", "-y", "24")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starting tmux session: %v\n%s", err, out)
	}

	t.Cleanup(h.cleanup)

	// Export GOCOVERDIR inside the tmux shell so the amux client and
	// server daemon both inherit it and write coverage data on exit.
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		h.sendKeys("export GOCOVERDIR="+dir, "Enter")
		time.Sleep(200 * time.Millisecond)
	}

	// Launch amux with this test's unique session name (-s flag)
	h.sendKeys(amuxBin, " -s ", session, "Enter")

	// Wait for amux to start (status bar should appear)
	if !h.waitFor("[pane-", 8 * time.Second) {
		screen := h.capture()
		t.Fatalf("amux did not start within timeout.\nScreen:\n%s", screen)
	}

	return h
}

// cleanup detaches the client gracefully (for coverage flush), then kills
// the tmux session and server. Only targets this test's resources.
func (h *TmuxHarness) cleanup() {
	// Detach client gracefully so it exits cleanly and flushes coverage data
	exec.Command("tmux", "send-keys", "-t", h.session, "C-a", "d").Run()
	time.Sleep(200 * time.Millisecond)

	// Kill the tmux session
	exec.Command("tmux", "kill-session", "-t", h.session).Run()

	// SIGTERM the server daemon (exact match on session name).
	// The server handles SIGTERM gracefully via os.Exit(0), which
	// triggers Go's atexit coverage flush.
	out, _ := exec.Command("pgrep", "-f", fmt.Sprintf("amux _server %s$", h.session)).Output()
	for _, pid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid != "" {
			exec.Command("kill", pid).Run()
		}
	}
	time.Sleep(200 * time.Millisecond) // wait for coverage flush

	// Clean up socket
	os.Remove(filepath.Join(fmt.Sprintf("/tmp/amux-%d", os.Getuid()), h.session))
}

// sendKeys sends keystrokes to the tmux session.
func (h *TmuxHarness) sendKeys(keys ...string) {
	h.t.Helper()
	args := append([]string{"send-keys", "-t", h.session}, keys...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		h.t.Fatalf("send-keys %v: %v\n%s", keys, err, out)
	}
}

// capture returns the current visible content of the tmux pane.
func (h *TmuxHarness) capture() string {
	out, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p").Output()
	if err != nil {
		h.t.Fatalf("capture-pane: %v", err)
	}
	return string(out)
}

// waitFor polls capture-pane until substr appears or timeout expires.
func (h *TmuxHarness) waitFor(substr string, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(h.capture(), substr) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitForFunc polls capture-pane until fn returns true or timeout expires.
func (h *TmuxHarness) waitForFunc(fn func(string) bool, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn(h.capture()) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// assertScreen fails the test if fn returns false for the current screen.
func (h *TmuxHarness) assertScreen(msg string, fn func(string) bool) {
	h.t.Helper()
	screen := h.capture()
	if !fn(screen) {
		h.t.Errorf("%s\nScreen:\n%s", msg, screen)
	}
}

// runCmd runs an amux CLI command targeting this test's session.
func (h *TmuxHarness) runCmd(args ...string) string {
	h.t.Helper()
	cmdArgs := append([]string{"-s", h.session}, args...)
	out, _ := exec.Command(amuxBin, cmdArgs...).CombinedOutput()
	return string(out)
}

// captureAmux returns the server-side composited screen via `amux capture`.
// Unlike h.capture() (tmux capture-pane), this tests the server's compositor
// directly and returns a deterministic plain-text 2D grid.
func (h *TmuxHarness) captureAmux() string {
	h.t.Helper()
	return h.runCmd("capture")
}

// captureAmuxLines returns the amux capture output split into rows.
func (h *TmuxHarness) captureAmuxLines() []string {
	h.t.Helper()
	return strings.Split(h.captureAmux(), "\n")
}

// captureAmuxContentLines returns amux capture lines excluding the global bar.
func (h *TmuxHarness) captureAmuxContentLines() []string {
	h.t.Helper()
	var out []string
	for _, line := range h.captureAmuxLines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// captureAmuxVerticalBorderCol finds a consistent vertical border column
// in the amux capture output.
func (h *TmuxHarness) captureAmuxVerticalBorderCol() int {
	h.t.Helper()
	return findVerticalBorderCol(h.captureAmuxContentLines())
}

// sendMouseSGR sends a raw SGR mouse escape sequence to the tmux pane.
// button: 0=left, 1=middle, 2=right, 64=scroll-up, 65=scroll-down
// x, y: 1-based terminal coordinates
// press: true for press (M), false for release (m)
func (h *TmuxHarness) sendMouseSGR(button, x, y int, press bool) {
	h.t.Helper()
	term := byte('M')
	if !press {
		term = byte('m')
	}
	// Build the SGR sequence: \033[<button;x;yM or \033[<button;x;ym
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", button, x, y, term)
	// Convert to hex for tmux send-keys -H
	var hexArgs []string
	for _, b := range []byte(seq) {
		hexArgs = append(hexArgs, fmt.Sprintf("%02x", b))
	}
	args := append([]string{"send-keys", "-t", h.session, "-H"}, hexArgs...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		h.t.Fatalf("send-keys -H: %v\n%s", err, out)
	}
}

// clickAt sends a left-click press at (x, y) using 1-based coordinates.
func (h *TmuxHarness) clickAt(x, y int) {
	h.t.Helper()
	h.sendMouseSGR(0, x, y, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, x, y, false)
}

// dragBorder sends a left-click press at (startX, startY), then a motion
// event at (endX, endY), then release at (endX, endY).
func (h *TmuxHarness) dragBorder(startX, startY, endX, endY int) {
	h.t.Helper()
	// Press
	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	// Motion (button 0 + 32 motion flag = 32)
	h.sendMouseSGR(32, endX, endY, true)
	time.Sleep(50 * time.Millisecond)
	// Release
	h.sendMouseSGR(0, endX, endY, false)
}

// scrollAt sends a scroll wheel event at (x, y). up=true for scroll up.
func (h *TmuxHarness) scrollAt(x, y int, up bool) {
	h.t.Helper()
	btn := 65 // scroll down
	if up {
		btn = 64
	}
	h.sendMouseSGR(btn, x, y, true)
}

// ---------------------------------------------------------------------------
// Split helpers — wrap the common "send split key + wait for new pane" pattern
// ---------------------------------------------------------------------------

// doSplit sends a prefix + key split combo and waits for a new pane to appear.
func (h *TmuxHarness) doSplit(key string) {
	h.t.Helper()
	n := strings.Count(h.capture(), "[pane-")
	h.sendKeys("C-a", key)
	if !h.waitForFunc(func(s string) bool {
		return strings.Count(s, "[pane-") > n
	}, 3 * time.Second) {
		h.t.Fatalf("split (%s): new pane did not appear\nScreen:\n%s", key, h.capture())
	}
}

func (h *TmuxHarness) splitV()     { h.t.Helper(); h.doSplit("\\") }
func (h *TmuxHarness) splitH()     { h.t.Helper(); h.doSplit("-") }
func (h *TmuxHarness) splitRootV() { h.t.Helper(); h.doSplit("|") }
func (h *TmuxHarness) splitRootH() { h.t.Helper(); h.doSplit("_") }

// ---------------------------------------------------------------------------
// Shared ANSI / color helpers (used by border, hotreload, and mouse tests)
// ---------------------------------------------------------------------------

// captureANSI captures the tmux pane with ANSI escape sequences preserved.
func (h *TmuxHarness) captureANSI() string {
	h.t.Helper()
	out, err := exec.Command("tmux", "capture-pane", "-t", h.session, "-p", "-e").Output()
	if err != nil {
		h.t.Fatalf("capture-pane -e: %v", err)
	}
	return string(out)
}

// isPaneActive returns true if the captured screen shows the named pane
// with the active indicator (● [name]).
func isPaneActive(screen, paneName string) bool {
	target := "[" + paneName + "]"
	for _, line := range strings.Split(screen, "\n") {
		idx := strings.Index(line, target)
		if idx < 0 {
			continue
		}
		if strings.Contains(line[:idx], "●") {
			return true
		}
	}
	return false
}

// isPaneInactive returns true if the captured screen shows the named pane
// with the inactive indicator (○ [name]).
func isPaneInactive(screen, paneName string) bool {
	target := "[" + paneName + "]"
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, target) && strings.Contains(line, "○") {
			return true
		}
	}
	return false
}

// pickContentLine returns a middle content line from ANSI-escaped screen output,
// skipping status lines and empty lines.
func pickContentLine(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) / 2; i < len(lines); i++ {
		if strings.Contains(lines[i], "│") && !strings.Contains(lines[i], "amux") {
			return lines[i]
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "│") && !strings.Contains(lines[0], "[pane-") {
			return line
		}
	}
	return ""
}

// extractBorderColors finds each │ in an ANSI-escaped line and returns
// the most recent \033[...m escape sequence before each one.
func extractBorderColors(line string) []string {
	var colors []string
	lastEscape := ""
	i := 0
	for i < len(line) {
		if line[i] == '\033' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				lastEscape = line[i : j+1]
				i = j + 1
				continue
			}
		}
		if i+2 < len(line) && line[i] == '\xe2' && line[i+1] == '\x94' && line[i+2] == '\x82' {
			colors = append(colors, lastEscape)
			i += 3
			continue
		}
		i++
	}
	return colors
}

// findHorizontalBorderRow returns the first row index containing a horizontal
// border (>10 horizontal box-drawing chars), or -1 if not found.
func findHorizontalBorderRow(lines []string) int {
	for i, line := range lines {
		count := 0
		for _, r := range line {
			if r == '─' || r == '┼' || r == '┬' || r == '┴' {
				count++
			}
		}
		if count > 10 {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Layout-aware screen helpers
// ---------------------------------------------------------------------------

// lines returns the captured screen split into rows, excluding empty trailing lines.
func (h *TmuxHarness) lines() []string {
	h.t.Helper()
	raw := strings.Split(h.capture(), "\n")
	// Trim trailing empty lines
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

// isGlobalBar returns true if the line looks like the global status bar.
// Matches the structural pattern: " amux │ ... panes │ HH:MM "
func isGlobalBar(line string) bool {
	return strings.Contains(line, " amux ") && strings.Contains(line, "panes │")
}

// hasWindowTab returns true if the global bar contains a tab for the given
// 1-based window index (e.g., "1:window-" or "[2:window-").
func hasWindowTab(bar string, index int) bool {
	prefix := fmt.Sprintf("%d:window-", index)
	return strings.Contains(bar, prefix)
}

// contentLines returns screen rows excluding the global status bar.
func (h *TmuxHarness) contentLines() []string {
	h.t.Helper()
	var out []string
	for _, line := range h.lines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// isBorderRune returns true for any box-drawing character used in borders.
func isBorderRune(r rune) bool {
	switch r {
	case '│', '─', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

// isVerticalBorderRune returns true for box-drawing characters with a vertical component.
func isVerticalBorderRune(r rune) bool {
	switch r {
	case '│', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

// verticalBorderCol finds the column where a vertical border appears
// consistently across content lines. Returns -1 if no consistent border found.
func (h *TmuxHarness) verticalBorderCol() int {
	h.t.Helper()
	return findVerticalBorderCol(h.contentLines())
}

// findVerticalBorderCol finds a consistent vertical border column in lines.
func findVerticalBorderCol(lines []string) int {
	if len(lines) == 0 {
		return -1
	}

	// Find all columns that have a vertical border char on the first content line
	candidates := map[int]bool{}
	for i, r := range []rune(lines[0]) {
		if isVerticalBorderRune(r) {
			candidates[i] = true
		}
	}

	// Keep only columns where a vertical border char appears on most lines (>50%)
	for col := range candidates {
		count := 0
		for _, line := range lines {
			runes := []rune(line)
			if col < len(runes) && isVerticalBorderRune(runes[col]) {
				count++
			}
		}
		if count < len(lines)/2 {
			delete(candidates, col)
		}
	}

	for col := range candidates {
		return col
	}
	return -1
}

// paneNameRow returns the row index where [name] appears, or -1.
func paneNameRow(lines []string, name string) int {
	target := "[" + name + "]"
	for i, line := range lines {
		if strings.Contains(line, target) {
			return i
		}
	}
	return -1
}

// paneNameCol returns the column where [name] starts, or -1.
func paneNameCol(lines []string, name string) int {
	target := "[" + name + "]"
	for _, line := range lines {
		if idx := strings.Index(line, target); idx >= 0 {
			return idx
		}
	}
	return -1
}

// globalBar returns the global bar line from the screen, or "".
func (h *TmuxHarness) globalBar() string {
	h.t.Helper()
	for _, line := range h.lines() {
		if isGlobalBar(line) {
			return line
		}
	}
	return ""
}

// globalBarAmux returns the global bar from amux capture output.
func (h *TmuxHarness) globalBarAmux() string {
	h.t.Helper()
	for _, line := range h.captureAmuxLines() {
		if isGlobalBar(line) {
			return line
		}
	}
	return ""
}
