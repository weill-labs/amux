package test

import (
	"strings"
	"testing"
	"time"
)

func TestHelpOverlayShowsAndDismisses(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?")
	if !h.waitFor("keybindings", 3*time.Second) || !h.waitFor("Navigation", 3*time.Second) {
		t.Fatalf("expected keybinding help overlay, got:\n%s", h.captureOuter())
	}

	h.sendClientKeys("?")
	if !waitForOuterGone(h, "keybindings", 3*time.Second) {
		t.Fatalf("expected help overlay to dismiss on ?\nScreen:\n%s", h.captureOuter())
	}
}

func TestHelpOverlayConsumesDismissKeyOnly(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?", "0", "e", "c", "h", "o", " ", "HELP_OK", "Enter")

	if !h.waitFor("HELP_OK", 3*time.Second) {
		t.Fatalf("expected HELP_OK after dismissing help overlay\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "0echo HELP_OK") {
		t.Fatalf("help overlay should consume only the dismiss key, got leaked input\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "keybindings") {
		t.Fatalf("help overlay should be hidden after dismiss key, got:\n%s", screen)
	}
}

func TestHelpOverlayToggleConsumesPrefixQuestionMark(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?")
	if !h.waitFor("keybindings", 3*time.Second) {
		t.Fatalf("expected keybinding help overlay before toggle test, got:\n%s", h.captureOuter())
	}

	h.sendClientKeys("C-a", "?", "e", "c", "h", "o", " ", "TOGGLE_OK", "Enter")

	if !h.waitFor("TOGGLE_OK", 3*time.Second) {
		t.Fatalf("expected TOGGLE_OK after toggling help overlay off\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "?echo TOGGLE_OK") {
		t.Fatalf("toggle should consume prefix+? without leaking ? into the shell\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "keybindings") {
		t.Fatalf("help overlay should be hidden after prefix+? toggle, got:\n%s", screen)
	}
}
