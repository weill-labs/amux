package test

import "testing"

func TestExplicitPaneCommandsPreferActorWindowWithoutChangingFocus(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("spawn", "--background", "--name", "shared"); out == "" {
		t.Fatal("spawn shared in window-1 should report success")
	}

	if out := h.runCmd("new-window", "--name", "window-2"); out == "" {
		t.Fatal("new-window should report success")
	}
	if out := h.runCmd("spawn", "--background", "--name", "shared"); out == "" {
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

	h.sendKeys("3", amuxBin+" -s "+h.session+" send-keys shared 'echo ACTOR_ROUTE' Enter", "Enter")
	h.waitFor("4", "ACTOR_ROUTE")
	if paneOne := h.runCmd("capture", "2"); contains(paneOne, "ACTOR_ROUTE") {
		t.Fatalf("window-1 shared pane should not receive actor-routed input:\n%s", paneOne)
	}

	h.sendKeys("3", amuxBin+" -s "+h.session+" capture shared | grep WINDOW_TWO && echo CAPTURE_OK", "Enter")
	h.waitFor("3", "CAPTURE_OK")

	h.sendKeys("3", amuxBin+" -s "+h.session+" wait-for shared WINDOW_TWO --timeout 1s && echo WAIT_OK", "Enter")
	h.waitFor("3", "WAIT_OK")

	if got := h.captureJSON().Window.Index; got != 1 {
		t.Fatalf("active window index = %d, want 1 after actor-targeted commands", got)
	}
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) > 0 && (func() bool { return stringIndex(s, substr) >= 0 })()
}

func stringIndex(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
