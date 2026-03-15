package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyModeEnterExit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Enter copy mode with Ctrl-a [
	h.sendKeys("C-a", "[")

	// Verify [copy] indicator appears in the screen
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator after entering copy mode\nScreen:\n%s", screen)
	}

	// Exit copy mode with q
	h.sendKeys("q")

	// Verify [copy] indicator disappears
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator to disappear after pressing q\nScreen:\n%s", screen)
	}
}

func TestCopyModeScroll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Generate 50 numbered lines of output using a temp script
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-scroll-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
for i in $(seq -w 1 50); do echo "SCROLLTEST-$i"; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")

	// Wait for the last line to appear, confirming all output was generated
	if !h.waitFor("SCROLLTEST-50", 5 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected SCROLLTEST-50 in output\nScreen:\n%s", screen)
	}

	// Early lines should have scrolled off screen by now
	screen := h.capture()
	if strings.Contains(screen, "SCROLLTEST-01") {
		t.Log("SCROLLTEST-01 still visible before copy mode, test may not validate scrollback")
	}

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen = h.capture()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Scroll up with k several times to reach earlier lines
	for i := 0; i < 40; i++ {
		h.sendKeys("k")
	}
	time.Sleep(400 * time.Millisecond)

	// Verify earlier lines become visible after scrolling up
	if !h.waitFor("SCROLLTEST-01", 3 * time.Second) {
		screen = h.capture()
		t.Fatalf("expected SCROLLTEST-01 to be visible after scrolling up in copy mode\nScreen:\n%s", screen)
	}

	// Exit copy mode
	h.sendKeys("q")
}

func TestCopyModeSearch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Generate output with a distinctive marker
	h.sendKeys("e", "c", "h", "o", " ", "S", "E", "A", "R", "C", "H", "M", "A", "R", "K", "Enter")
	if !h.waitFor("SEARCHMARK", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected SEARCHMARK in output\nScreen:\n%s", screen)
	}

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Start search with /
	h.sendKeys("/")
	time.Sleep(200 * time.Millisecond)

	// Type search query
	h.sendKeys("S", "E", "A", "R", "C", "H", "M", "A", "R", "K")
	time.Sleep(200 * time.Millisecond)

	// Confirm search
	h.sendKeys("Enter")
	time.Sleep(400 * time.Millisecond)

	// Verify copy mode is still active (search doesn't exit copy mode)
	h.assertScreen("expected [copy] indicator to remain after search", func(s string) bool {
		return strings.Contains(s, "[copy]")
	})

	// Exit copy mode
	h.sendKeys("q")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] to disappear after exiting\nScreen:\n%s", screen)
	}
}

func TestCopyModeCLI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Enter copy mode via CLI command
	h.runCmd("copy-mode", "pane-1")

	// Verify [copy] indicator appears
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator after CLI copy-mode command\nScreen:\n%s", screen)
	}

	// Exit copy mode
	h.sendKeys("q")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] to disappear after pressing q\nScreen:\n%s", screen)
	}
}

func TestCopyModeEscapeExit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Exit with Escape
	h.sendKeys("Escape")

	// Verify copy mode exits
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] to disappear after pressing Escape\nScreen:\n%s", screen)
	}
}

func TestCopyModeDoesNotForwardInput(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Type characters that would be visible if forwarded to the shell
	h.sendKeys("h", "e", "l", "l", "o")
	time.Sleep(400 * time.Millisecond)

	// Exit copy mode
	h.sendKeys("q")
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		screen := h.capture()
		t.Fatalf("expected [copy] to disappear\nScreen:\n%s", screen)
	}

	// Wait a moment for any buffered input to be processed
	time.Sleep(400 * time.Millisecond)

	// Verify "hello" does NOT appear in the shell output
	// (the characters should have been consumed by copy mode)
	screen := h.capture()
	if strings.Contains(screen, "hello") {
		t.Errorf("copy mode should not forward input to the shell, but 'hello' appeared\nScreen:\n%s", screen)
	}
}

func TestCopyModeResizeSurvives(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Generate output so scrollback has content
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-resize-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 30); do echo \"RESIZE-$i\"; done\n"), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	h.waitFor("RESIZE-30", 5 * time.Second)

	// Enter copy mode and scroll up
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3 * time.Second) {
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", h.capture())
	}
	for i := 0; i < 20; i++ {
		h.sendKeys("k")
	}
	time.Sleep(400 * time.Millisecond)

	// Resize terminal while in copy mode
	exec.Command("tmux", "resize-pane", "-t", h.session, "-x", "120", "-y", "40").Run()
	time.Sleep(1 * time.Second)

	// Copy mode should still be active after resize
	h.assertScreen("copy mode survives resize", func(s string) bool {
		return strings.Contains(s, "[copy]")
	})

	// Should still be able to exit
	h.sendKeys("q")
	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3 * time.Second) {
		t.Fatalf("expected [copy] to disappear after q\nScreen:\n%s", h.capture())
	}
}
