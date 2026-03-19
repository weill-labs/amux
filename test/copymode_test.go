package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyModeEnterExit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode with Ctrl-a [
	h.sendKeys("C-a", "[")

	// Verify [copy] indicator appears (client-side, check outer pane)
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator after entering copy mode\nScreen:\n%s", screen)
	}

	// Exit copy mode with q
	h.sendKeys("q")

	// Verify [copy] indicator disappears (poll outer pane)
	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator to disappear after pressing q\nScreen:\n%s", screen)
	}
}

func TestCopyModeScroll(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Generate 50 numbered lines of output using a temp script
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-scroll-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
for i in $(seq -w 1 50); do echo "SCROLLTEST-$i"; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")

	// Wait for the last line to appear, confirming all output was generated
	if !h.waitFor("SCROLLTEST-50", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected SCROLLTEST-50 in output\nScreen:\n%s", screen)
	}

	// Early lines should have scrolled off screen by now
	screen := h.captureOuter()
	if strings.Contains(screen, "SCROLLTEST-01") {
		t.Log("SCROLLTEST-01 still visible before copy mode, test may not validate scrollback")
	}

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		screen = h.captureOuter()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Scroll to top with g to reach earliest lines
	h.sendKeys("g")

	// Verify earlier lines become visible after scrolling up (client-side rendering)
	if !h.waitFor("SCROLLTEST-01", 3*time.Second) {
		screen = h.captureOuter()
		t.Fatalf("expected SCROLLTEST-01 to be visible after scrolling to top in copy mode\nScreen:\n%s", screen)
	}

	// Exit copy mode
	h.sendKeys("q")
}

func TestCopyModeSearch(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Generate output with a distinctive marker
	h.sendKeys("e", "c", "h", "o", " ", "S", "E", "A", "R", "C", "H", "M", "A", "R", "K", "Enter")
	if !h.waitFor("SEARCHMARK", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected SEARCHMARK in output\nScreen:\n%s", screen)
	}

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Start search with / — the search prompt is now rendered in the
	// status bar as "[copy] /query", so we can waitFor it.
	h.sendKeys("/")
	if !h.waitFor("[copy] /", 3*time.Second) {
		t.Fatalf("expected search prompt in status bar\nScreen:\n%s", h.captureOuter())
	}

	// Type search query and wait for it to render
	h.sendKeys("S", "E", "A", "R", "C", "H", "M", "A", "R", "K")
	if !h.waitFor("/SEARCHMARK", 3*time.Second) {
		t.Fatalf("expected search query in status bar\nScreen:\n%s", h.captureOuter())
	}

	// Confirm search
	h.sendKeys("Enter")

	// Verify copy mode is still active (search doesn't exit copy mode)
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator to remain after search\nScreen:\n%s", screen)
	}

	// Exit copy mode
	h.sendKeys("q")

	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] to disappear after exiting\nScreen:\n%s", screen)
	}
}

func TestCopyModeCLI(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode via CLI command
	h.runCmd("copy-mode", "pane-1")

	// Verify [copy] indicator appears (client-side, check outer pane)
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator after CLI copy-mode command\nScreen:\n%s", screen)
	}

	// Exit copy mode
	h.sendKeys("q")

	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] to disappear after pressing q\nScreen:\n%s", screen)
	}
}

func TestCopyModeEscapeExit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Exit with Escape
	h.sendKeys("Escape")

	// Verify copy mode exits (poll outer pane)
	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] to disappear after pressing Escape\nScreen:\n%s", screen)
	}
}

func TestCopyModeDoesNotForwardInput(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", screen)
	}

	// Type characters that would be visible if forwarded to the shell
	h.sendKeys("h", "e", "l", "l", "o")

	// Exit copy mode and wait for indicator to disappear
	h.sendKeys("q")
	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] to disappear\nScreen:\n%s", screen)
	}

	// Send a marker command — once it appears, any buffered "hello"
	// input would have been processed too.
	h.sendKeys("e", "c", "h", "o", " ", "D", "O", "N", "E", "Enter")
	if !h.waitFor("DONE", 3*time.Second) {
		t.Fatalf("expected DONE marker\nScreen:\n%s", h.captureOuter())
	}

	// Verify "hello" does NOT appear in the shell output
	// (the characters should have been consumed by copy mode)
	screen := h.captureOuter()
	if strings.Contains(screen, "hello") {
		t.Errorf("copy mode should not forward input to the shell, but 'hello' appeared\nScreen:\n%s", screen)
	}
}

func TestCopyModeResizeSurvives(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Generate output so scrollback has content
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-resize-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 30); do echo \"RESIZE-$i\"; done\n"), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("RESIZE-30", 5*time.Second) {
		t.Fatalf("RESIZE-30 not visible\nScreen:\n%s", h.captureOuter())
	}

	// Enter copy mode and scroll to top
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", h.captureOuter())
	}
	h.sendKeys("g")
	// Wait for scroll to reach earlier content
	if !h.waitFor("RESIZE-01", 3*time.Second) {
		t.Fatalf("scrollback not reached after scrolling to top\nScreen:\n%s", h.captureOuter())
	}

	// Resize terminal while in copy mode via the outer server
	gen := h.generation()
	h.outer.runCmd("resize-window", "120", "40")
	h.waitLayout(gen)

	// Copy mode should still be active after resize
	if !h.waitFor("[copy]", 3*time.Second) {
		t.Fatalf("expected [copy] to survive resize\nScreen:\n%s", h.captureOuter())
	}

	// Should still be able to exit. Use inner type-keys here so the assertion
	// only depends on the inner client handling copy-mode exit, not on the
	// outer pane render catching up after the terminal resize.
	h.runCmd("type-keys", "q")
	if !h.waitForFunc(func(s string) bool { return !strings.Contains(s, "[copy]") }, 3*time.Second) {
		t.Fatalf("expected [copy] to disappear after q\nInner:\n%s\nOuter:\n%s", h.capture(), h.captureOuter())
	}
}

func TestCopyModeVimMotions(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Generate output with distinctive words on multiple lines.
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-motions-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
for i in $(seq -w 1 50); do echo "ALPHA BRAVO CHARLIE $i"; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("ALPHA BRAVO CHARLIE 50", 5*time.Second) {
		t.Fatalf("expected output\nScreen:\n%s", h.captureOuter())
	}

	// Enter copy mode.
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		t.Fatalf("expected [copy] indicator\nScreen:\n%s", h.captureOuter())
	}

	// Exercise word motions: W (forward), B (backward), E (end of word).
	for _, key := range []string{"W", "W", "B", "E"} {
		h.sendKeys(key)
		if !h.waitFor("[copy]", 2*time.Second) {
			t.Fatalf("[copy] disappeared after %s\nScreen:\n%s", key, h.captureOuter())
		}
	}

	// Exercise line motions: $ (end), 0 (start), ^ (first non-blank).
	for _, key := range []string{"$", "0", "^"} {
		h.sendKeys(key)
		if !h.waitFor("[copy]", 2*time.Second) {
			t.Fatalf("[copy] disappeared after %s\nScreen:\n%s", key, h.captureOuter())
		}
	}

	// Exercise char search: f + A (find 'A' on line).
	h.sendKeys("f", "A")
	if !h.waitFor("[copy]", 2*time.Second) {
		t.Fatalf("[copy] disappeared after fA\nScreen:\n%s", h.captureOuter())
	}

	// Exercise repeat (;) and reverse repeat (,).
	h.sendKeys(";")
	if !h.waitFor("[copy]", 2*time.Second) {
		t.Fatalf("[copy] disappeared after ;\nScreen:\n%s", h.captureOuter())
	}
	h.sendKeys(",")
	if !h.waitFor("[copy]", 2*time.Second) {
		t.Fatalf("[copy] disappeared after ,\nScreen:\n%s", h.captureOuter())
	}

	// Exercise full-page scroll: Ctrl-f (down), Ctrl-b (up).
	h.sendKeys("C-f")
	if !h.waitFor("[copy]", 2*time.Second) {
		t.Fatalf("[copy] disappeared after Ctrl-f\nScreen:\n%s", h.captureOuter())
	}
	h.sendKeys("C-b")
	if !h.waitFor("[copy]", 2*time.Second) {
		t.Fatalf("[copy] disappeared after Ctrl-b\nScreen:\n%s", h.captureOuter())
	}

	// Scroll to top with g, verify early output is reachable.
	h.sendKeys("g")
	if !h.waitFor("ALPHA BRAVO CHARLIE 01", 3*time.Second) {
		t.Fatalf("expected early output after scrolling to top\nScreen:\n%s", h.captureOuter())
	}

	// Exit copy mode.
	h.sendKeys("q")
	if !waitForOuterGone(h, "[copy]", 3*time.Second) {
		t.Fatalf("expected [copy] to disappear after q\nScreen:\n%s", h.captureOuter())
	}
}

// waitForOuterGone polls the outer pane capture until substr is no longer
// present. This remains polling because the outer client-visible overlays can
// disappear without a server-side wait primitive exposing that transition.
func waitForOuterGone(h *AmuxHarness, substr string, timeout time.Duration) bool {
	h.tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(h.captureOuter(), substr) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
