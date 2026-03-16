package test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMouseClickFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	h.assertActive("pane-2")

	// Click at column 10, row 5 — inside pane-1 (left half of 80-col terminal)
	h.clickAt(10, 5)

	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Errorf("after clicking left pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}

	// Click on pane-2 (column 60) to switch back
	h.clickAt(60, 5)

	if !h.waitForActive("pane-2", 3*time.Second) {
		t.Errorf("after clicking right pane, pane-2 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseClickFocusHorizontalSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.assertActive("pane-2")

	// Click at top of screen (row 3) — inside pane-1
	h.clickAt(40, 3)

	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Errorf("after clicking top pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseBorderDrag(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	borderCol := h.captureAmuxVerticalBorderCol()
	if borderCol < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	dragDelta := 5
	gen := h.generation()
	h.dragBorder(borderCol+1, 10, borderCol+1+dragDelta, 10)
	h.waitLayout(gen)

	newBorderCol := h.captureAmuxVerticalBorderCol()
	if newBorderCol < 0 {
		t.Fatalf("no vertical border found after drag.\nScreen:\n%s", h.captureAmux())
	}
	if newBorderCol <= borderCol {
		t.Errorf("border should have moved right: was at %d, now at %d.\nScreen:\n%s",
			borderCol, newBorderCol, h.captureAmux())
	}
}

func TestMouseScrollWheel(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	for i := 0; i < 30; i++ {
		h.sendKeys(fmt.Sprintf("echo line-%d", i), "Enter")
		time.Sleep(30 * time.Millisecond)
	}
	h.waitFor("line-29", 3*time.Second)

	screen := h.capture()
	if !strings.Contains(screen, "line-29") {
		t.Fatalf("expected line-29 visible before scroll.\nScreen:\n%s", screen)
	}

	h.scrollAt(40, 12, true)
	h.scrollAt(40, 12, true)
	h.scrollAt(40, 12, true)
	time.Sleep(200 * time.Millisecond)

	if !h.waitFor("[pane-", 3*time.Second) {
		t.Errorf("amux should still be running after scroll.\nScreen:\n%s", h.capture())
	}
}
