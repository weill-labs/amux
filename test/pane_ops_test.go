package test

import (
	"strings"
	"testing"
	"time"
)

func TestPaneClose(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("e", "x", "i", "t", "Enter")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after exit")
	}

	capLines := h.captureAmuxContentLines()
	hasPane1 := false
	for _, line := range capLines {
		if strings.Contains(line, "[pane-1]") {
			hasPane1 = true
		}
		if strings.Contains(line, "│") {
			t.Errorf("capture: no vertical borders expected after close, got: %q", line)
			break
		}
	}
	if !hasPane1 {
		t.Errorf("capture: pane-1 should still be visible\n%s", strings.Join(capLines, "\n"))
	}

	h.assertScreen("pane-1 status on first row", func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 0 && strings.Contains(lines[0], "[pane-1]")
	})
}

func TestSpawn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("spawn", "--name", "test-agent", "--task", "TASK-42")
	if !strings.Contains(output, "test-agent") {
		t.Errorf("spawn should report agent name, got:\n%s", output)
	}

	h.waitFor("[test-agent]", 3*time.Second)
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-agent") {
		t.Errorf("list should contain test-agent, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "TASK-42") {
		t.Errorf("list should contain TASK-42, got:\n%s", listOut)
	}
}

func TestMinimizeRestore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Errorf("minimize should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)
	h.assertScreen("pane-1 still visible after minimize", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	output = h.runCmd("restore", "pane-1")
	if !strings.Contains(output, "Restored") {
		t.Errorf("restore should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)
	h.assertScreen("both panes visible after restore", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestMinimizeRestorePreservesContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Put identifiable content in pane-1
	h.runCmd("focus", "pane-1")
	time.Sleep(200 * time.Millisecond)
	h.sendKeys("echo PRESERVE_TEST_MARKER", "Enter")
	h.waitFor("PRESERVE_TEST_MARKER", 3*time.Second)

	// Capture pane content before minimize
	beforeCapture := h.runCmd("capture", "pane-1")
	if !strings.Contains(beforeCapture, "PRESERVE_TEST_MARKER") {
		t.Fatalf("marker should be visible before minimize, got:\n%s", beforeCapture)
	}

	// Minimize then restore pane-1
	h.runCmd("minimize", "pane-1")
	time.Sleep(500 * time.Millisecond)
	h.runCmd("restore", "pane-1")
	time.Sleep(1 * time.Second)

	// Pane content should be preserved — not blank
	afterCapture := h.runCmd("capture", "pane-1")
	if !strings.Contains(afterCapture, "PRESERVE_TEST_MARKER") {
		t.Fatalf("pane content should be preserved after minimize/restore, got:\n%s", afterCapture)
	}
}

func TestToggleMinimizeKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a second pane (horizontal split)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	// Focus pane-1 and press Ctrl-a m to minimize it
	h.runCmd("focus", "pane-1")
	time.Sleep(300 * time.Millisecond)
	h.sendKeys("C-a", "m")

	// Verify pane-1 is minimized via status command
	time.Sleep(500 * time.Millisecond)
	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "1 minimized") {
		t.Fatalf("expected 1 minimized pane after Ctrl-a m, got:\n%s", statusOut)
	}

	// Press Ctrl-a m again — should restore pane-1 (most recently minimized)
	h.sendKeys("C-a", "m")

	// Verify no panes are minimized
	time.Sleep(500 * time.Millisecond)
	statusOut = h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Fatalf("expected 0 minimized panes after second Ctrl-a m, got:\n%s", statusOut)
	}
}

func TestToggleMinimizeLIFO(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create 3 panes
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)

	// Minimize pane-1 then pane-2 via CLI
	h.runCmd("minimize", "pane-1")
	time.Sleep(300 * time.Millisecond)
	h.runCmd("minimize", "pane-2")
	time.Sleep(300 * time.Millisecond)

	// Toggle should restore pane-2 first (LIFO)
	h.sendKeys("C-a", "m")
	time.Sleep(500 * time.Millisecond)

	// Verify: pane-2 restored (1 minimized remains = pane-1)
	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "1 minimized") {
		t.Fatalf("expected 1 minimized after first toggle (pane-1 still minimized), got:\n%s", statusOut)
	}
	// Confirm pane-2 is restored by trying to minimize it (should succeed, not error)
	out := h.runCmd("minimize", "pane-2")
	if strings.Contains(out, "already minimized") {
		t.Errorf("pane-2 should have been restored by LIFO toggle, got:\n%s", out)
	}
	// Undo: restore pane-2 back for next step
	h.runCmd("restore", "pane-2")
	time.Sleep(300 * time.Millisecond)

	// Toggle again should restore pane-1
	h.sendKeys("C-a", "m")
	time.Sleep(500 * time.Millisecond)

	statusOut = h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Fatalf("expected 0 minimized after second toggle, got:\n%s", statusOut)
	}
}

func TestKill(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Errorf("kill should confirm, got:\n%s", output)
	}

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after kill")
	}

	h.assertScreen("pane-1 should remain after kill", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	listOut := h.runCmd("list")
	if strings.Contains(listOut, "pane-2") {
		t.Errorf("list should not contain pane-2 after kill, got:\n%s", listOut)
	}
}

func TestSendKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Send literal text + Enter to pane-1 via CLI
	out := h.runCmd("send-keys", "pane-1", "echo SENDTEST", "Enter")
	if strings.Contains(out, "error") || strings.Contains(out, "not found") {
		t.Fatalf("send-keys failed: %s", out)
	}

	// Verify the command executed in the pane
	if !h.waitFor("SENDTEST", 3*time.Second) {
		t.Fatalf("send-keys text not visible in pane\nScreen:\n%s", h.capture())
	}

	// Verify via amux capture of the specific pane
	paneOut := h.runCmd("capture", "pane-1")
	if !strings.Contains(paneOut, "SENDTEST") {
		t.Errorf("pane capture should contain SENDTEST, got:\n%s", paneOut)
	}
}

func TestSendKeysSpecialKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Type partial text, then C-c to cancel, then a new command
	h.runCmd("send-keys", "pane-1", "partial-text")
	time.Sleep(200 * time.Millisecond)
	h.runCmd("send-keys", "pane-1", "C-c")
	time.Sleep(200 * time.Millisecond)
	h.runCmd("send-keys", "pane-1", "echo AFTERCANCEL", "Enter")

	if !h.waitFor("AFTERCANCEL", 3*time.Second) {
		t.Fatalf("C-c + new command not visible\nScreen:\n%s", h.capture())
	}
}

func TestSendKeysInvalidPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out := h.runCmd("send-keys", "nonexistent", "hello")
	if !strings.Contains(out, "not found") {
		t.Errorf("expected 'not found' error for invalid pane, got: %s", out)
	}
}

func TestSendKeysToSpecificPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create a second pane
	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Send keys specifically to pane-2 (not the active pane)
	h.runCmd("send-keys", "pane-2", "echo PANE2CMD", "Enter")

	// Verify it appeared in pane-2's output
	ok := h.waitForFunc(func(screen string) bool {
		paneOut := h.runCmd("capture", "pane-2")
		return strings.Contains(paneOut, "PANE2CMD")
	}, 3*time.Second)
	if !ok {
		t.Fatalf("send-keys to pane-2 did not work\npane-2 output:\n%s", h.runCmd("capture", "pane-2"))
	}

	// Verify it did NOT appear in pane-1
	pane1Out := h.runCmd("capture", "pane-1")
	if strings.Contains(pane1Out, "PANE2CMD") {
		t.Errorf("PANE2CMD should not appear in pane-1, got:\n%s", pane1Out)
	}
}
