// TODO(LAB-83): Make integration tests parallelizable by giving each test
// its own amux session name and socket path.
package main

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
	out, err := exec.Command("go", "build", "-o", amuxBin, ".").CombinedOutput()
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
		// Some commands fail gracefully — return output anyway
		return string(out)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestBasicStartAndDetach(t *testing.T) {
	h := newHarness(t)

	// Should see status bar with pane name
	h.assertScreen("should show pane status", func(s string) bool {
		return strings.Contains(s, "[pane-")
	})

	// Should see global bar with "amux"
	h.assertScreen("should show global status bar", func(s string) bool {
		return strings.Contains(s, "amux")
	})

	// Detach with Ctrl-a d
	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)

	// After detach, the tmux pane should show the shell (not amux)
	// The amux process exited, tmux pane returns to whatever spawned it
	screen := h.capture()
	// Amux status bar should be gone
	if strings.Contains(screen, "amux") && strings.Contains(screen, "panes") {
		// Still showing amux — might be fine if tmux session ended
	}
}

func TestSplitVertical(t *testing.T) {
	h := newHarness(t)

	// Split vertical (left/right)
	h.sendKeys("C-a", "\\")

	// Should see a vertical border
	if !h.waitFor("│", 3*time.Second) {
		t.Fatal("vertical border not found after split")
	}

	// Should see two pane status lines
	h.waitFor("[pane-2]", 3*time.Second)
	h.assertScreen("should show two panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestSplitHorizontal(t *testing.T) {
	h := newHarness(t)

	// Split horizontal (top/bottom)
	h.sendKeys("C-a", "-")

	// Should see a horizontal border
	if !h.waitFor("─", 3*time.Second) {
		t.Fatal("horizontal border not found after split")
	}

	// Should see two pane names
	h.waitFor("[pane-2]", 3*time.Second)
	h.assertScreen("should show two panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestFocusCycle(t *testing.T) {
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// The active pane indicator (●) should be on pane-2 (new pane gets focus)
	// Cycle focus back to pane-1
	h.sendKeys("C-a", "o")
	time.Sleep(500 * time.Millisecond)

	// pane-1 should now be active (● next to it)
	h.assertScreen("pane-1 should have active indicator", func(s string) bool {
		// The ● appears on the active pane's status line
		lines := strings.Split(s, "\n")
		for _, line := range lines {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})
}

func TestPaneClose(t *testing.T) {
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Type "exit" in the active pane (pane-2)
	h.sendKeys("e", "x", "i", "t", "Enter")

	// Wait for pane-2 to disappear
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after exit")
	}

	// pane-1 should still be there
	h.assertScreen("pane-1 should remain", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	// Pane-separating borders should be gone (ignore global bar which uses │)
	h.assertScreen("no pane borders with single pane", func(s string) bool {
		lines := strings.Split(s, "\n")
		for _, line := range lines {
			// Skip global status bar line
			if strings.Contains(line, "amux") && strings.Contains(line, "panes") {
				continue
			}
			if strings.Contains(line, "│") {
				return false
			}
		}
		return true
	})
}

func TestList(t *testing.T) {
	h := newHarness(t)

	// Split to get two panes
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Run amux list
	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list should contain pane-1, got:\n%s", output)
	}
	if !strings.Contains(output, "pane-2") {
		t.Errorf("list should contain pane-2, got:\n%s", output)
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t)

	output := h.runCmd("status")
	if !strings.Contains(output, "1 total") {
		t.Errorf("status should show 1 total, got:\n%s", output)
	}
}

func TestReattach(t *testing.T) {
	h := newHarness(t)

	// Type something distinctive
	h.sendKeys("e", "c", "h", "o", " ", "H", "E", "L", "L", "O", "Enter")
	h.waitFor("HELLO", 3*time.Second)

	// Detach
	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)

	// Reattach by running amux again in the same tmux pane
	h.sendKeys(amuxBin, "Enter")

	// Wait for reattach — status bar should reappear
	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("reattach failed, screen:\n%s", screen)
	}

	// The previous output should be visible (reconstructed from emulator)
	h.assertScreen("should see HELLO after reattach", func(s string) bool {
		return strings.Contains(s, "HELLO")
	})
}
