package test

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestChooseWindowShowsModalAndSelectsWindow(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.runCmd("rename-window", "editor")
	h.runCmd("new-window", "--name", "logs")
	h.runCmd("select-window", "1")

	h.sendClientKeys("C-a", "w")
	if !h.waitFor("choose-window", 3*time.Second) || !h.waitFor("2:logs", 3*time.Second) {
		t.Fatalf("expected choose-window modal, got:\n%s", h.captureOuter())
	}

	out := h.runCmd("wait", "ui", proto.UIEventChooseWindowShown, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventChooseWindowShown) {
		t.Fatalf("wait-ui shown output = %q", out)
	}

	gen := h.generation()
	h.sendClientKeys("l", "o", "g", "s", "Enter")
	h.waitLayout(gen)

	if got := h.captureJSON().Window.Name; got != "logs" {
		t.Fatalf("selected window = %q, want logs", got)
	}
	out = h.runCmd("wait", "ui", proto.UIEventChooseWindowHidden, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventChooseWindowHidden) {
		t.Fatalf("wait-ui hidden output = %q", out)
	}
}

func TestChooseTreeFocusesPaneAcrossWindows(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	gen := h.generation()
	h.sendKeys("C-a", "\\")
	h.waitLayout(gen)

	h.runCmd("new-window", "--name", "logs")
	h.runCmd("select-window", "1")

	h.sendClientKeys("C-a", "s")
	if !h.waitFor("choose-tree", 3*time.Second) || !h.waitFor("2:logs", 3*time.Second) {
		t.Fatalf("expected choose-tree modal, got:\n%s", h.captureOuter())
	}

	gen = h.generation()
	h.sendClientKeys("p", "a", "n", "e", "-", "3", "Enter")
	h.waitLayout(gen)

	if got := h.activePaneName(); got != "pane-3" {
		t.Fatalf("active pane = %q, want pane-3", got)
	}
	if got := h.captureJSON().Window.Name; got != "logs" {
		t.Fatalf("selected window = %q, want logs", got)
	}
}

func TestChooserDismissDoesNotLeakInput(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.runCmd("rename-window", "editor")
	h.runCmd("new-window", "--name", "logs")
	h.runCmd("select-window", "1")

	h.sendClientKeys("C-a", "w")
	if !h.waitFor("choose-window", 3*time.Second) {
		t.Fatalf("expected choose-window modal, got:\n%s", h.captureOuter())
	}

	h.sendClientKeys("l", "o", "g", "s", "Escape")
	if !waitForOuterGone(h, "choose-window", 3*time.Second) {
		t.Fatalf("expected chooser to dismiss\nScreen:\n%s", h.captureOuter())
	}

	h.sendClientKeys("Enter")
	outer := h.captureOuter()
	if strings.Contains(outer, "command not found") {
		t.Fatalf("chooser input leaked into the shell, got:\n%s", outer)
	}
}

func TestChooseTreeDismissesOnLayoutChange(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.sendKeys("C-a", "s")
	if !h.waitFor("choose-tree", 3*time.Second) {
		t.Fatalf("expected choose-tree modal, got:\n%s", h.captureOuter())
	}

	gen := h.generation()
	h.runCmd("split", "pane-1")
	h.waitLayout(gen)

	out := h.runCmd("wait", "ui", proto.UIEventChooseTreeHidden, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventChooseTreeHidden) {
		t.Fatalf("wait-ui hidden output = %q", out)
	}
	if strings.Contains(h.captureOuter(), "choose-tree") {
		t.Fatalf("chooser should clear on layout change, got:\n%s", h.captureOuter())
	}
}
