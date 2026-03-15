package test

import (
	"strings"
	"testing"
	"time"
)

func TestResizeKeybindHorizontal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a vertical split: [pane-1 | pane-2]
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Measure initial border position
	initialBorder := h.captureAmuxVerticalBorderCol()
	if initialBorder < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	// Focus pane-1 (left), then press Prefix+L to grow it rightward
	h.sendKeys("C-a", "h") // focus left pane
	time.Sleep(200 * time.Millisecond)
	h.sendKeys("C-a", "L") // resize: grow right
	time.Sleep(300 * time.Millisecond)

	newBorder := h.captureAmuxVerticalBorderCol()
	if newBorder < 0 {
		t.Fatalf("no vertical border found after resize.\nScreen:\n%s", h.captureAmux())
	}
	if newBorder <= initialBorder {
		t.Errorf("Prefix+L from left pane should move border right: was %d, now %d.\nScreen:\n%s",
			initialBorder, newBorder, h.captureAmux())
	}

	// Now press Prefix+H to shrink it back (grow left = move border left)
	h.sendKeys("C-a", "H")
	time.Sleep(300 * time.Millisecond)

	shrunkBorder := h.captureAmuxVerticalBorderCol()
	if shrunkBorder >= newBorder {
		t.Errorf("Prefix+H from left pane should move border left: was %d, now %d.\nScreen:\n%s",
			newBorder, shrunkBorder, h.captureAmux())
	}
}

func TestResizeKeybindVertical(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a horizontal split: pane-1 on top, pane-2 on bottom
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Focus pane-1 (top)
	h.sendKeys("C-a", "k")
	time.Sleep(200 * time.Millisecond)

	// Measure initial horizontal border row
	initialRow := findHorizontalBorderRow(h.captureAmuxContentLines())
	if initialRow < 0 {
		t.Fatalf("no horizontal border found.\nScreen:\n%s", h.captureAmux())
	}

	// Press Prefix+J to grow top pane downward (move border down)
	h.sendKeys("C-a", "J")
	time.Sleep(300 * time.Millisecond)

	newRow := findHorizontalBorderRow(h.captureAmuxContentLines())
	if newRow < 0 {
		t.Fatalf("no horizontal border found after resize.\nScreen:\n%s", h.captureAmux())
	}
	if newRow <= initialRow {
		t.Errorf("Prefix+J from top pane should move border down: was row %d, now %d.\nScreen:\n%s",
			initialRow, newRow, h.captureAmux())
	}

	// Press Prefix+K to shrink it back (grow up = move border up)
	h.sendKeys("C-a", "K")
	time.Sleep(300 * time.Millisecond)

	shrunkRow := findHorizontalBorderRow(h.captureAmuxContentLines())
	if shrunkRow >= newRow {
		t.Errorf("Prefix+K from top pane should move border up: was row %d, now %d.\nScreen:\n%s",
			newRow, shrunkRow, h.captureAmux())
	}
}

func TestResizeKeybindFromRightPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// [pane-1 | pane-2], focus stays on pane-2 (right)
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	time.Sleep(200 * time.Millisecond)

	initialBorder := h.captureAmuxVerticalBorderCol()
	if initialBorder < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	// From right pane, Prefix+H should grow left (move border left)
	h.sendKeys("C-a", "H")
	time.Sleep(300 * time.Millisecond)

	newBorder := h.captureAmuxVerticalBorderCol()
	if newBorder >= initialBorder {
		t.Errorf("Prefix+H from right pane should move border left: was %d, now %d.\nScreen:\n%s",
			initialBorder, newBorder, h.captureAmux())
	}
}

func TestResizeKeybindNoEffect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Single pane — resize should be a no-op, not crash
	h.sendKeys("C-a", "H")
	time.Sleep(300 * time.Millisecond)

	// Should still be running with the single pane
	h.assertScreen("amux still running after resize with single pane", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})
}
