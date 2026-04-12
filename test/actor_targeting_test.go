package test

import (
	"strings"
	"testing"
)

func TestExplicitPaneCommandsPreferActorWindowWithoutChangingFocus(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("spawn", "--name", "shared"); out == "" {
		t.Fatal("spawn shared in window-1 should report success")
	}

	if out := h.runCmd("new-window", "--name", "window-2"); out == "" {
		t.Fatal("new-window should report success")
	}
	if out := h.runCmd("spawn", "--name", "shared"); out == "" {
		t.Fatal("spawn shared in window-2 should report success")
	}

	h.sendKeys("2", "echo WINDOW_ONE", "Enter")
	h.waitFor("2", "WINDOW_ONE")
	h.sendKeys("4", "echo WINDOW_TWO", "Enter")
	h.waitFor("4", "WINDOW_TWO")

	h.runCmd("select-window", "1")
	if got := h.captureJSON().Window.Index; got != 1 {
		t.Fatalf("active window index = %d, want 1 before actor-targeted commands", got)
	}

	h.sendKeys("3", nestedAmuxCommand(amuxBin, h.session, "send-keys", "shared", "echo ACTOR_ROUTE", "Enter"), "Enter")
	h.waitFor("4", "ACTOR_ROUTE")
	h.waitIdle("3")
	if paneOne := h.runCmd("capture", "2"); strings.Contains(paneOne, "ACTOR_ROUTE") {
		t.Fatalf("window-1 shared pane should not receive actor-routed input:\n%s", paneOne)
	}

	h.runShellCommand("3", nestedAmuxCommand(amuxBin, h.session, "capture", "shared")+" | grep WINDOW_TWO && echo CAPTURE_OK", "CAPTURE_OK")

	h.runShellCommand("3", nestedAmuxCommand(amuxBin, h.session, "wait", "content", "shared", "WINDOW_TWO", "--timeout", "5s")+" && echo WAIT_OK", "WAIT_OK")

	if got := h.captureJSON().Window.Index; got != 1 {
		t.Fatalf("active window index = %d, want 1 after actor-targeted commands", got)
	}
}
