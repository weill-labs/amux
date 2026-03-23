package test

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
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
	h := newServerHarnessPersistent(t)

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
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	// Exit copy mode via type-keys.
	h.runCmd("type-keys", "q")
	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
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
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	h.runCmd("type-keys", "g")

	if !h.waitFor("TKSCROLL-01", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("expected TKSCROLL-01 visible after scrolling to top\nScreen:\n%s", screen)
	}
}

func TestTypeKeysMinimizeWithShiftM(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	gen := h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	out := h.runCmd("status")
	if !strings.Contains(out, "1 minimized") {
		t.Fatalf("expected 1 minimized after type-keys C-a M, got: %s", out)
	}
}

func TestTypeKeysMinimizeFailureShowsReasonInVerticalSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("expected split layout to render before minimize test\nScreen:\n%s", h.captureOuter())
	}
	h.runCmd("focus", "pane-2")
	h.runCmd("type-keys", "C-a", "M")

	msg := "rightmost column"
	if !h.waitFor(msg, 3*time.Second) {
		t.Fatalf("expected minimize failure reason in outer capture\nScreen:\n%s", h.captureOuter())
	}

	out := h.runCmd("status")
	if !strings.Contains(out, "0 minimized") {
		t.Fatalf("vertical split minimize should not change minimized count, got: %s", out)
	}
}

func TestTypeKeysMinimizeFailureShowsReasonAtRoot(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.runCmd("type-keys", "C-a", "M")

	if !h.waitFor("pane has no stacked siblings", 3*time.Second) {
		t.Fatalf("expected root minimize failure reason in outer capture\nScreen:\n%s", h.captureOuter())
	}

	out := h.runCmd("status")
	if !strings.Contains(out, "0 minimized") {
		t.Fatalf("root minimize should not change minimized count, got: %s", out)
	}
}

func TestMinimizeFailureFeedbackClearsOnNextLocalInput(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("expected split layout to render before clear test\nScreen:\n%s", h.captureOuter())
	}
	h.runCmd("focus", "pane-2")
	h.runCmd("type-keys", "C-a", "M")

	msg := "rightmost column"
	if !h.waitFor(msg, 3*time.Second) {
		t.Fatalf("expected minimize failure reason before clear test\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("Enter")
	if !waitForOuterGone(h, msg, 3*time.Second) {
		t.Fatalf("expected local input to clear minimize failure feedback\nScreen:\n%s", h.captureOuter())
	}
}

func TestTypeKeysOldMinimizeKeyDoesNotLeakInput(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	before := h.runCmd("status")
	if !strings.Contains(before, "0 minimized") {
		t.Fatalf("expected 0 minimized before old key test, got: %s", before)
	}

	h.runCmd("type-keys", "C-a", "m")
	h.runCmd("type-keys", "e", "c", "h", "o", " ", "OLDKEY_OK", "Enter")

	if !h.waitFor("OLDKEY_OK", 3*time.Second) {
		t.Fatalf("expected OLDKEY_OK marker after old key test\nScreen:\n%s", h.captureOuter())
	}

	after := h.runCmd("status")
	if !strings.Contains(after, "0 minimized") {
		t.Fatalf("old C-a m should not minimize, got: %s", after)
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "mecho OLDKEY_OK") {
		t.Fatalf("old C-a m should not leak literal input into the shell\nScreen:\n%s", screen)
	}
}

func TestTypeKeysDisplayPanesConsumesOnlyOneKey(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	before := h.activePaneName()
	h.runCmd("type-keys", "C-a", "q", "0", "e", "c", "h", "o", " ", "BATCH_OK", "Enter")

	if !h.waitFor("BATCH_OK", 3*time.Second) {
		t.Fatalf("expected BATCH_OK after invalid overlay key plus batched shell input\nScreen:\n%s", h.captureOuter())
	}
	if got := h.activePaneName(); got != before {
		t.Fatalf("invalid overlay key should not change focus, got %s want %s", got, before)
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "0echo BATCH_OK") {
		t.Fatalf("overlay should consume only the first key, got leaked batched input\nScreen:\n%s", screen)
	}
}

func TestTypeKeysUnsupportedPrefixKeyDoesNotLeakInput(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.runCmd("type-keys", "C-a", "f", "e", "c", "h", " ", "UNBOUND_OK", "Enter")

	if !h.waitFor("UNBOUND_OK", 3*time.Second) {
		t.Fatalf("expected UNBOUND_OK marker after unsupported key test\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "fech UNBOUND_OK") || strings.Contains(screen, "fecho UNBOUND_OK") {
		t.Fatalf("unsupported prefix key should not leak literal input into the shell\nScreen:\n%s", screen)
	}
}
