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
	if !h.waitFor("? help", 3*time.Second) || !h.waitFor("nav", 3*time.Second) || !h.waitFor("layout", 3*time.Second) {
		t.Fatalf("expected bottom help bar, got:\n%s", h.captureOuter())
	}
	screen := h.captureOuter()
	if strings.Contains(screen, "keybindings") || strings.Contains(screen, "Navigation") {
		t.Fatalf("expected compact help bar instead of old modal overlay, got:\n%s", screen)
	}

	h.sendClientKeys("?")
	if !waitForOuterGone(h, "nav", 3*time.Second) {
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
	if strings.Contains(screen, " nav ") {
		t.Fatalf("help bar should be hidden after dismiss key, got:\n%s", screen)
	}
}

func TestHelpBarToggleConsumesPrefixQuestionMark(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendClientKeys("C-a", "?")
	if !h.waitFor("nav", 3*time.Second) {
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
	if strings.Contains(screen, " nav ") {
		t.Fatalf("help bar should be hidden after prefix+? toggle, got:\n%s", screen)
	}
}

func TestHelpBarGlobalBarClickToggles(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	bar := h.globalBar()
	x := strings.Index(bar, "? help")
	if x < 0 {
		t.Fatalf("global bar %q missing ? help toggle", bar)
	}

	h.clickAt(x+1, 24)
	if !h.waitFor("nav", 3*time.Second) {
		t.Fatalf("expected global bar click to show help bar\nScreen:\n%s", h.captureOuter())
	}

	h.clickAt(x+1, 24)
	if !waitForOuterGone(h, "nav", 3*time.Second) {
		t.Fatalf("expected second global bar click to hide help bar\nScreen:\n%s", h.captureOuter())
	}
}
