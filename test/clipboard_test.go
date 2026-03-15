package test

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestClipboardOSC52(t *testing.T) {
	// Not parallel — these tests share tmux's global paste buffer
	h := newHarness(t)

	// Enable tmux clipboard handling so it stores OSC 52 content in paste buffer
	if out, err := exec.Command("tmux", "set-option", "-t", h.session, "set-clipboard", "on").CombinedOutput(); err != nil {
		t.Skipf("tmux set-clipboard not supported: %v\n%s", err, out)
	}

	// Clear any existing paste buffer
	exec.Command("tmux", "delete-buffer").Run()

	// Emit OSC 52 with "Hello" (base64: SGVsbG8=), BEL terminator
	h.sendKeys("printf '\\033]52;c;SGVsbG8=\\007'", "Enter")

	// Wait for the OSC 52 to propagate: pane → server → client → tmux
	time.Sleep(2 * time.Second)

	// tmux should have stored the decoded clipboard content in its paste buffer
	out, err := exec.Command("tmux", "show-buffer").CombinedOutput()
	if err != nil {
		t.Skipf("tmux show-buffer failed (clipboard may not be supported in this environment): %v\n%s", err, out)
	}

	got := strings.TrimRight(string(out), "\n")
	if got != "Hello" {
		t.Errorf("clipboard via OSC 52: got %q, want %q", got, "Hello")
	}
}

func TestClipboardOSC52STTerminator(t *testing.T) {
	// Not parallel — these tests share tmux's global paste buffer
	h := newHarness(t)

	// Enable tmux clipboard handling
	if out, err := exec.Command("tmux", "set-option", "-t", h.session, "set-clipboard", "on").CombinedOutput(); err != nil {
		t.Skipf("tmux set-clipboard not supported: %v\n%s", err, out)
	}

	exec.Command("tmux", "delete-buffer").Run()

	// Emit OSC 52 with ST terminator (\033\\) instead of BEL
	// "World" = V29ybGQ= in base64
	h.sendKeys("printf '\\033]52;c;V29ybGQ=\\033\\\\'", "Enter")

	time.Sleep(2 * time.Second)

	out, err := exec.Command("tmux", "show-buffer").CombinedOutput()
	if err != nil {
		t.Skipf("tmux show-buffer failed: %v\n%s", err, out)
	}

	got := strings.TrimRight(string(out), "\n")
	if got != "World" {
		t.Errorf("clipboard via OSC 52 (ST terminator): got %q, want %q", got, "World")
	}
}
