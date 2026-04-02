package test

import (
	"strings"
	"testing"
	"time"
)

func TestHelpBarShowsAndDismisses(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?")
	if !h.waitFor("? close", 3*time.Second) || !h.waitFor("q panes", 3*time.Second) || !h.waitFor("root-vsplit", 3*time.Second) {
		t.Fatalf("expected bottom help bar, got:\n%s", h.captureOuter())
	}
	screen := h.captureOuter()
	if strings.Contains(screen, "keybindings") || strings.Contains(screen, "Navigation") {
		t.Fatalf("expected compact help bar instead of old modal overlay, got:\n%s", screen)
	}

	h.sendClientKeys("?")
	if !waitForOuterGone(h, "? close", 3*time.Second) {
		t.Fatalf("expected help bar to dismiss on ?\nScreen:\n%s", h.captureOuter())
	}
}

func TestHelpBarConsumesDismissKeyOnly(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?", "0", "e", "c", "h", "o", " ", "HELP_OK", "Enter")

	if !h.waitFor("HELP_OK", 3*time.Second) {
		t.Fatalf("expected HELP_OK after dismissing help bar\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "0echo HELP_OK") {
		t.Fatalf("help bar should consume only the dismiss key, got leaked input\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "? close") {
		t.Fatalf("help bar should be hidden after dismiss key, got:\n%s", screen)
	}
}

func TestHelpBarToggleConsumesPrefixQuestionMark(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?")
	if !h.waitFor("? close", 3*time.Second) {
		t.Fatalf("expected help bar before toggle test, got:\n%s", h.captureOuter())
	}

	h.sendClientKeys("C-a", "?", "e", "c", "h", "o", " ", "TOGGLE_OK", "Enter")

	if !h.waitFor("TOGGLE_OK", 3*time.Second) {
		t.Fatalf("expected TOGGLE_OK after toggling help bar off\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "?echo TOGGLE_OK") {
		t.Fatalf("toggle should consume prefix+? without leaking ? into the shell\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "? close") {
		t.Fatalf("help bar should be hidden after prefix+? toggle, got:\n%s", screen)
	}
}

func TestHelpBarGlobalBarClickToggles(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	if !h.waitFor("? help", 3*time.Second) {
		t.Fatalf("expected initial global bar to render ? help\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	bar := ""
	for _, line := range strings.Split(screen, "\n") {
		if isGlobalBar(line) {
			bar = line
		}
	}
	if bar == "" {
		t.Fatalf("expected global bar in outer capture, got:\n%s", screen)
	}
	panesIdx := strings.Index(bar, "1 panes")
	helpIdx := strings.Index(bar, "? help")
	if panesIdx < 0 || helpIdx < 0 {
		t.Fatalf("expected pane count and ? help in outer capture, got:\n%s", screen)
	}
	if helpIdx <= panesIdx {
		t.Fatalf("expected ? help to appear to the right of the pane count in the global bar, got:\n%s", screen)
	}
	x, y, ok := outerTextCoords(screen, "? help")
	if !ok {
		t.Fatalf("expected ? help in outer capture, got:\n%s", screen)
	}

	h.clickAt(x+1, y)
	if !h.waitFor("? close", 3*time.Second) {
		t.Fatalf("expected global bar click to show help bar\nScreen:\n%s", h.captureOuter())
	}

	h.clickAt(x+1, y)
	if !waitForOuterGone(h, "? close", 3*time.Second) {
		t.Fatalf("expected second global bar click to hide help bar\nScreen:\n%s", h.captureOuter())
	}
}
