package test

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestDisplayPanesOverlayShowsLabels(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()

	h.sendKeys("C-a", "q")
	if !h.waitFor("[1]", 3*time.Second) || !h.waitFor("[2]", 3*time.Second) {
		t.Fatalf("expected pane overlay labels in outer capture, got:\n%s", h.captureOuter())
	}

	if got := h.activePaneName(); got != "pane-2" {
		t.Fatalf("display-panes should not change focus, got %s", got)
	}
}

func TestDisplayPanesQuickJump(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	h.splitV()

	h.sendKeys("C-a", "q")
	if !h.waitFor("[3]", 3*time.Second) {
		t.Fatalf("expected pane overlay labels before jump, got:\n%s", h.captureOuter())
	}

	h.sendKeys("1")
	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("expected pane-1 active after quick jump, got:\n%s", h.capture())
	}

	outer := h.captureOuter()
	if strings.Contains(outer, "[1]") || strings.Contains(outer, "[2]") || strings.Contains(outer, "[3]") {
		t.Fatalf("overlay should clear after jump, got:\n%s", outer)
	}
}

func TestDisplayPanesInvalidKeyDismissesWithoutLeak(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()

	h.sendKeys("C-a", "q")
	if !h.waitFor("[2]", 3*time.Second) {
		t.Fatalf("expected pane overlay labels before invalid key, got:\n%s", h.captureOuter())
	}

	h.sendKeys("0")
	h.sendKeys("Enter")

	if !h.waitFor("$", 3*time.Second) {
		t.Fatalf("expected shell prompt after invalid key dismissal, got:\n%s", h.captureOuter())
	}

	outer := h.captureOuter()
	if strings.Contains(outer, "command not found") {
		t.Fatalf("invalid overlay key should not leak into the shell, got:\n%s", outer)
	}
	if strings.Contains(outer, "[1]") || strings.Contains(outer, "[2]") {
		t.Fatalf("overlay should clear after invalid key, got:\n%s", outer)
	}
	if got := h.activePaneName(); got != "pane-2" {
		t.Fatalf("invalid overlay key should not change focus, got %s", got)
	}
}

func TestDisplayPanesMinimizedPaneStillSelectable(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitH()

	h.runCmd("focus", "pane-1")
	gen := h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	h.sendKeys("C-a", "q")
	if !h.waitFor("[1]", 3*time.Second) {
		t.Fatalf("expected overlay label for minimized pane, got:\n%s", h.captureOuter())
	}

	h.sendKeys("1")
	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("expected minimized pane to become active after quick jump, got:\n%s", h.capture())
	}
}

func TestDisplayPanesZoomedOnlyShowsVisiblePane(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	h.runCmd("zoom", "pane-2")

	h.sendKeys("C-a", "q")
	if !h.waitFor("[1]", 3*time.Second) {
		t.Fatalf("expected overlay label for zoomed pane, got:\n%s", h.captureOuter())
	}

	outer := h.captureOuter()
	if strings.Contains(outer, "[2]") {
		t.Fatalf("zoomed overlay should not show hidden pane labels, got:\n%s", outer)
	}

	h.sendKeys("2")
	if !waitForOuterGone(h, "[1]", 3*time.Second) {
		t.Fatalf("expected overlay to clear after invalid zoomed label\nScreen:\n%s", h.captureOuter())
	}
	if got := h.activePaneName(); got != "pane-2" {
		t.Fatalf("hidden zoomed pane label should not change focus, got %s", got)
	}
}

func TestDisplayPanesWaitUIShownAndHidden(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()

	h.sendKeys("C-a", "q")
	out := h.runCmd("wait-ui", proto.UIEventDisplayPanesShown, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventDisplayPanesShown) {
		t.Fatalf("wait-ui shown output = %q", out)
	}

	h.sendKeys("1")
	out = h.runCmd("wait-ui", proto.UIEventDisplayPanesHidden, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventDisplayPanesHidden) {
		t.Fatalf("wait-ui hidden output = %q", out)
	}
}
