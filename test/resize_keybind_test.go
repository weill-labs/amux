package test

import (
	"strings"
	"testing"
)

func TestResizeKeybindHorizontal(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split horizontally: [pane-1 | pane-2]
	h.splitV()

	// Measure initial pane-1 width
	c := h.captureJSON()
	initialW := h.jsonPane(c, "pane-1").Position.Width

	// Focus pane-1 (left), then press Prefix+L to grow it rightward
	gen := h.generation()
	h.sendKeys("C-a", "h") // focus left pane
	h.waitLayout(gen)

	gen = h.generation()
	h.sendKeys("C-a", "L") // resize: grow right
	h.waitLayout(gen)

	c = h.captureJSON()
	newW := h.jsonPane(c, "pane-1").Position.Width
	if newW <= initialW {
		t.Errorf("Prefix+L from left pane should grow it: width was %d, now %d", initialW, newW)
	}

	// Now press Prefix+H to shrink it back
	gen = h.generation()
	h.sendKeys("C-a", "H")
	h.waitLayout(gen)

	c = h.captureJSON()
	shrunkW := h.jsonPane(c, "pane-1").Position.Width
	if shrunkW >= newW {
		t.Errorf("Prefix+H from left pane should shrink it: width was %d, now %d", newW, shrunkW)
	}
}

func TestResizeKeybindVertical(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Create a horizontal split: pane-1 on top, pane-2 on bottom
	h.splitH()

	// Focus pane-1 (top)
	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	// Measure initial pane-1 height
	c := h.captureJSON()
	initialH := h.jsonPane(c, "pane-1").Position.Height

	// Press Prefix+J to grow top pane downward
	gen = h.generation()
	h.sendKeys("C-a", "J")
	h.waitLayout(gen)

	c = h.captureJSON()
	newH := h.jsonPane(c, "pane-1").Position.Height
	if newH <= initialH {
		t.Errorf("Prefix+J from top pane should grow it: height was %d, now %d", initialH, newH)
	}

	// Press Prefix+K to shrink it back
	gen = h.generation()
	h.sendKeys("C-a", "K")
	h.waitLayout(gen)

	c = h.captureJSON()
	shrunkH := h.jsonPane(c, "pane-1").Position.Height
	if shrunkH >= newH {
		t.Errorf("Prefix+K from top pane should shrink it: height was %d, now %d", newH, shrunkH)
	}
}

func TestResizeKeybindFromRightPane(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// [pane-1 | pane-2], focus stays on pane-2 (right)
	h.splitV()

	c := h.captureJSON()
	initialW := h.jsonPane(c, "pane-2").Position.Width

	// From right pane, Prefix+H should grow left (pane-2 grows, pane-1 shrinks)
	gen := h.generation()
	h.sendKeys("C-a", "H")
	h.waitLayout(gen)

	c = h.captureJSON()
	newW := h.jsonPane(c, "pane-2").Position.Width
	if newW <= initialW {
		t.Errorf("Prefix+H from right pane should grow it: width was %d, now %d", initialW, newW)
	}
}

func TestResizeKeybindNoEffect(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Single pane — resize should be a no-op, not crash
	h.sendKeys("C-a", "H")

	// No layout change occurs on a single pane, so just assert immediately.
	h.assertScreen("amux still running after resize with single pane", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})
}
