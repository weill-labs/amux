package test

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// amuxBin is the path to the built amux binary, set in TestMain.
var amuxBin string

func TestMain(m *testing.M) {
	// Skip if tmux isn't available
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Println("SKIP: tmux not found")
		os.Exit(0)
	}

	// Build amux binary for testing
	tmp, err := os.MkdirTemp("", "amux-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	amuxBin = tmp + "/amux"
	out, err := exec.Command("go", "build", "-o", amuxBin, "..").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "building amux: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
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

	// Launch amux with this test's unique session name (-s flag)
	h.sendKeys(amuxBin, " -s ", session, "Enter")

	// Wait for amux to start (status bar should appear)
	if !h.waitFor("[pane-", 8*time.Second) {
		screen := h.capture()
		t.Fatalf("amux did not start within timeout.\nScreen:\n%s", screen)
	}

	return h
}

// cleanup kills the tmux session and its amux server.
// Only targets this test's resources — never kills other amux sessions.
func (h *TmuxHarness) cleanup() {
	// Kill the tmux session (this also terminates the amux client inside it)
	exec.Command("tmux", "kill-session", "-t", h.session).Run()

	// Kill only this test's server daemon (exact match on session name)
	out, _ := exec.Command("pgrep", "-f", fmt.Sprintf("amux _server %s$", h.session)).Output()
	for _, pid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid != "" {
			exec.Command("kill", pid).Run()
		}
	}

	// Clean up socket
	exec.Command("rm", "-f", fmt.Sprintf("/tmp/amux-%d/%s", os.Getuid(), h.session)).Run()
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
	out, err := exec.Command(amuxBin, cmdArgs...).CombinedOutput()
	if err != nil {
		return string(out)
	}
	return string(out)
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
func isGlobalBar(line string) bool {
	return strings.Contains(line, "amux") && strings.Contains(line, "panes")
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

// verticalBorderCol finds the column where a vertical border (│) appears
// consistently across content lines. Returns -1 if no consistent border found.
func (h *TmuxHarness) verticalBorderCol() int {
	h.t.Helper()
	lines := h.contentLines()
	if len(lines) == 0 {
		return -1
	}

	// Find all columns that have │ on the first content line
	candidates := map[int]bool{}
	for i, r := range []rune(lines[0]) {
		if r == '│' {
			candidates[i] = true
		}
	}

	// Keep only columns where │ appears on most lines (>50%)
	for col := range candidates {
		count := 0
		for _, line := range lines {
			runes := []rune(line)
			if col < len(runes) && runes[col] == '│' {
				count++
			}
		}
		if count < len(lines)/2 {
			delete(candidates, col)
		}
	}

	// Return the first consistent column
	for col := range candidates {
		return col
	}
	return -1
}

// horizontalBorderRow finds a row index where a horizontal border (─)
// spans most of the width. Returns -1 if none found.
func (h *TmuxHarness) horizontalBorderRow() int {
	h.t.Helper()
	for i, line := range h.contentLines() {
		count := strings.Count(line, "─")
		if count > 10 { // at least 10 ─ chars indicates a border
			return i
		}
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
