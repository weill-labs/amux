package test

import (
	"testing"
	"time"
)

func TestRepeatResize(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split horizontally: [pane-1 | pane-2]
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Focus left pane
	h.sendKeys("C-a", "h")
	time.Sleep(200 * time.Millisecond)

	initialBorder := h.captureAmuxVerticalBorderCol()
	if initialBorder < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	// Press Prefix+L once, then L twice more WITHOUT prefix (repeat mode)
	h.sendKeys("C-a", "L")
	time.Sleep(100 * time.Millisecond)
	h.sendKeys("L")
	time.Sleep(100 * time.Millisecond)
	h.sendKeys("L")
	time.Sleep(300 * time.Millisecond)

	newBorder := h.captureAmuxVerticalBorderCol()
	if newBorder < 0 {
		t.Fatalf("no vertical border found after repeat resize.\nScreen:\n%s", h.captureAmux())
	}

	// Should have moved by ~6 cells (3 presses × 2 cells each)
	moved := newBorder - initialBorder
	if moved < 4 {
		t.Errorf("expected border to move at least 4 cells with repeated L, moved %d (was %d, now %d).\nScreen:\n%s",
			moved, initialBorder, newBorder, h.captureAmux())
	}
}

func TestRepeatFocus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create 3 panes: [pane-1 | pane-2 | pane-3]
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-3]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Focus is on pane-3 (rightmost). Press Prefix+h then h again without prefix.
	// Should end up on pane-1 (two moves left).
	h.sendKeys("C-a", "h")
	time.Sleep(100 * time.Millisecond)
	h.sendKeys("h")
	time.Sleep(300 * time.Millisecond)

	if !h.waitForFunc(func(s string) bool {
		return isPaneActive(s, "pane-1")
	}, 3*time.Second) {
		t.Errorf("expected pane-1 active after repeated h.\nScreen:\n%s", h.capture())
	}
}

func TestRepeatCrossKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create 3 panes: [pane-1 | pane-2 | pane-3]
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-3]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Focus is on pane-3. Press Prefix+h (focus left to pane-2),
	// then l without prefix (focus right back to pane-3).
	// Tests that repeat mode accepts any repeatable key, not just the original.
	h.sendKeys("C-a", "h")
	time.Sleep(100 * time.Millisecond)
	h.sendKeys("l")
	time.Sleep(300 * time.Millisecond)

	if !h.waitForFunc(func(s string) bool {
		return isPaneActive(s, "pane-3")
	}, 3*time.Second) {
		t.Errorf("expected pane-3 active after h then l (cross-key repeat).\nScreen:\n%s", h.capture())
	}
}

func TestRepeatExpiresAfterTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Split: [pane-1 | pane-2]
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Focus left pane
	h.sendKeys("C-a", "h")
	time.Sleep(200 * time.Millisecond)

	initialBorder := h.captureAmuxVerticalBorderCol()
	if initialBorder < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	// Press Prefix+L, then wait longer than repeat timeout (500ms), then press L
	h.sendKeys("C-a", "L")
	time.Sleep(700 * time.Millisecond)
	// This L should be typed into the shell (repeat expired), not trigger resize
	h.sendKeys("L")
	time.Sleep(300 * time.Millisecond)

	newBorder := h.captureAmuxVerticalBorderCol()
	if newBorder < 0 {
		t.Fatalf("no vertical border found after timeout.\nScreen:\n%s", h.captureAmux())
	}
	// Should have moved only 2 cells (one resize), not 4
	moved := newBorder - initialBorder
	if moved > 3 {
		t.Errorf("expected border to move ~2 cells (repeat should have expired), moved %d.\nScreen:\n%s",
			moved, h.captureAmux())
	}
}
