// TODO(LAB-83): Make integration tests parallelizable by giving each test
// its own amux session name and socket path.
package test

import (
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
	session string
}

// newHarness creates a tmux session with a shell, runs amux inside it,
// and waits for amux to start. The shell survives amux detach.
func newHarness(t *testing.T) *TmuxHarness {
	t.Helper()
	session := fmt.Sprintf("amux-test-%d", time.Now().UnixNano()%100000)

	h := &TmuxHarness{t: t, session: session}

	// Start tmux session with a shell (not amux directly)
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session,
		"-x", "80", "-y", "24")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starting tmux session: %v\n%s", err, out)
	}

	t.Cleanup(h.cleanup)

	// Launch amux
	h.sendKeys(amuxBin, "Enter")

	// Wait for amux to start (status bar should appear)
	if !h.waitFor("[pane-", 8*time.Second) {
		screen := h.capture()
		t.Fatalf("amux did not start within timeout.\nScreen:\n%s", screen)
	}

	return h
}

// cleanup kills the tmux session and the amux server.
func (h *TmuxHarness) cleanup() {
	// Detach first (so amux server stays running for cleanup)
	exec.Command("tmux", "send-keys", "-t", h.session, "C-a", "d").Run()
	time.Sleep(200 * time.Millisecond)
	exec.Command("tmux", "kill-session", "-t", h.session).Run()
	exec.Command("pkill", "-f", "amux _server").Run()
	exec.Command("rm", "-f", fmt.Sprintf("/tmp/amux-%d/default", os.Getuid())).Run()
	time.Sleep(200 * time.Millisecond)
}

// sendKeys sends keystrokes to the tmux session. Each argument is passed as
// a separate tmux send-keys argument. Use tmux key names like "C-a" for Ctrl-a.
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

// runCmd runs an amux CLI command (e.g., "list") and returns its output.
func (h *TmuxHarness) runCmd(args ...string) string {
	h.t.Helper()
	cmdArgs := append([]string{}, args...)
	out, err := exec.Command(amuxBin, cmdArgs...).CombinedOutput()
	if err != nil {
		return string(out)
	}
	return string(out)
}
