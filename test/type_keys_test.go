package test

import (
	"strings"
	"testing"
	"time"
)

func TestTypeKeysSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// type-keys C-a - sends prefix + split-horizontal keybinding through
	// the client input pipeline, triggering a layout change.
	gen := h.generation()
	h.runCmd("type-keys", "C-a", "-")
	h.waitLayout(gen)

	out := h.runCmd("status")
	if !strings.Contains(out, "2 total") {
		t.Fatalf("expected 2 panes after type-keys split, got: %s", out)
	}
}

func TestTypeKeysLiteral(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Type literal text — should pass through to the active pane's PTY.
	h.runCmd("type-keys", "echo", "Space", "TYPEKEYS_MARKER", "Enter")

	if !h.waitFor("TYPEKEYS_MARKER", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected TYPEKEYS_MARKER in pane output\nScreen:\n%s", screen)
	}
}

func TestTypeKeysNoClient(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Close the headless client so there are no attached clients.
	h.client.close()
	h.client = nil

	out := h.runCmd("type-keys", "hello")
	if !strings.Contains(out, "no client attached") {
		t.Fatalf("expected 'no client attached' error, got: %s", out)
	}
}

func TestTypeKeysFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split first to have two panes.
	gen := h.generation()
	h.runCmd("type-keys", "C-a", "-")
	h.waitLayout(gen)

	// pane-2 should be active after split.
	h.assertActive("pane-2")

	// type-keys C-a o cycles focus to the next pane.
	gen = h.generation()
	h.runCmd("type-keys", "C-a", "o")
	h.waitLayout(gen)

	h.assertActive("pane-1")
}

func TestTypeKeysCopyMode(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode via type-keys.
	h.runCmd("type-keys", "C-a", "[")

	if !h.waitFor("[copy]", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] indicator after type-keys C-a [\nScreen:\n%s", screen)
	}

	// Exit copy mode via type-keys.
	h.runCmd("type-keys", "q")

	if !waitForOuter(h, func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected [copy] to disappear after type-keys q\nScreen:\n%s", screen)
	}
}

func TestTypeKeysCopyModeScroll(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Generate scrollback content.
	h.sendKeys("printf 'TKSCROLL-%02d\\n' {1..50}", "Enter")
	if !h.waitFor("TKSCROLL-50", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected TKSCROLL-50\nScreen:\n%s", screen)
	}

	// Enter copy mode and scroll to top via type-keys.
	h.runCmd("type-keys", "C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		t.Fatal("failed to enter copy mode")
	}

	h.runCmd("type-keys", "g")

	if !waitForOuter(h, func(s string) bool {
		return strings.Contains(s, "TKSCROLL-01")
	}, 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected TKSCROLL-01 visible after scrolling to top\nScreen:\n%s", screen)
	}
}
