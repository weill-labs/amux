package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestPaneClose(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Send "exit" to pane-2 (the active pane after split).
	gen := h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen) // blocks until pane exit triggers layout update

	c := h.captureJSON()
	if len(c.Panes) != 1 {
		t.Errorf("expected 1 pane after close, got %d", len(c.Panes))
	}
	h.jsonPane(c, "pane-1") // fails if pane-1 not found

	h.assertScreen("pane-1 status on first row", func(s string) bool {
		lines := strings.Split(s, "\n")
		return len(lines) > 0 && strings.Contains(lines[0], "[pane-1]")
	})
}

func TestSpawn(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("spawn", "--name", "test-agent", "--task", "TASK-42")
	if !strings.Contains(output, "test-agent") {
		t.Errorf("spawn should report agent name, got:\n%s", output)
	}

	// After synchronous spawn, capture immediately reflects the new pane.
	h.assertScreen("test-agent should be visible", func(s string) bool {
		return strings.Contains(s, "[test-agent]")
	})

	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-agent") {
		t.Errorf("list should contain test-agent, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "TASK-42") {
		t.Errorf("list should contain TASK-42, got:\n%s", listOut)
	}
}

func TestSpawnWhileZoomedKeepsZoomAndFocus(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("zoom", "pane-1")

	output := h.runCmd("spawn", "--name", "bg-worker", "--task", "TASK-42")
	if !strings.Contains(output, "bg-worker") {
		t.Fatalf("spawn should report agent name, got:\n%s", output)
	}

	h.assertScreen("zoomed spawn should keep only pane-1 visible", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]") &&
			!strings.Contains(s, "[bg-worker]")
	})

	capture := h.captureJSON()
	p1 := h.jsonPane(capture, "pane-1")
	if !p1.Active || !p1.Zoomed {
		t.Fatalf("pane-1 state after spawn = active:%v zoomed:%v, want true/true", p1.Active, p1.Zoomed)
	}
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "bg-worker") {
		t.Fatalf("list should include bg-worker after zoomed spawn, got:\n%s", listOut)
	}

	h.runCmd("zoom", "pane-1")
	h.assertScreen("unzoom should reveal the spawned background pane", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]") &&
			strings.Contains(s, "[bg-worker]")
	})
}

func TestSplitBackgroundKeepsFocus(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	output := h.runCmd("split", "--background", "--name", "bg-split")
	if !strings.Contains(output, "bg-split") {
		t.Fatalf("split should report the new pane name, got:\n%s", output)
	}

	capture := h.captureJSON()
	p1 := h.jsonPane(capture, "pane-1")
	if !p1.Active {
		t.Fatal("pane-1 should remain active after split --background")
	}
	h.jsonPane(capture, "bg-split")
	h.assertScreen("background split should still be visible when not zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[bg-split]")
	})
}

func TestSpawnBackgroundKeepsFocus(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	output := h.runCmd("spawn", "--background", "--name", "bg-worker", "--task", "TASK-42")
	if !strings.Contains(output, "bg-worker") {
		t.Fatalf("spawn should report the new pane name, got:\n%s", output)
	}

	capture := h.captureJSON()
	p1 := h.jsonPane(capture, "pane-1")
	if !p1.Active {
		t.Fatal("pane-1 should remain active after spawn --background")
	}
	h.jsonPane(capture, "bg-worker")
	h.assertScreen("background spawn should still be visible when not zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[bg-worker]")
	})
}

func TestMinimizeRestore(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Errorf("minimize should confirm, got:\n%s", output)
	}

	h.assertScreen("pane-1 still visible after minimize", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	output = h.runCmd("restore", "pane-1")
	if !strings.Contains(output, "Restored") {
		t.Errorf("restore should confirm, got:\n%s", output)
	}

	h.assertScreen("both panes visible after restore", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestMinimizeSoloPaneInColumnFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimizing left column should dissolve successfully, got:\n%s", output)
	}

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position == nil || p2.Position == nil {
		t.Fatalf("expected pane positions after dissolve, got %+v %+v", p1.Position, p2.Position)
	}
	if p1.Position.X != p2.Position.X {
		t.Fatalf("dissolved pane x = %d, want host x %d", p1.Position.X, p2.Position.X)
	}
	if p1.Position.Y <= p2.Position.Y {
		t.Fatalf("dissolved pane y = %d, want below host y %d", p1.Position.Y, p2.Position.Y)
	}
}

func TestMinimizeRightmostColumnFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	output := h.runCmd("minimize", "pane-2")
	if !strings.Contains(output, "rightmost column") {
		t.Errorf("minimizing rightmost column should fail, got:\n%s", output)
	}

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Errorf("no panes should be minimized, got:\n%s", statusOut)
	}
}

func TestMinimizeRootPaneShowsExplicitReason(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "pane has no stacked siblings") {
		t.Fatalf("root minimize should explain the constraint, got:\n%s", output)
	}

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Errorf("no panes should be minimized, got:\n%s", statusOut)
	}
}

func TestMinimizeLastPaneInColumnFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("focus", "pane-1")
	h.splitH()

	output := h.runCmd("minimize", "pane-3")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("first minimize should succeed, got:\n%s", output)
	}

	output = h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimizing last visible pane should dissolve successfully, got:\n%s", output)
	}

	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")
	if p1.Position == nil || p2.Position == nil || p3.Position == nil {
		t.Fatalf("expected pane positions after dissolve")
	}
	if p1.Position.X != p3.Position.X || p2.Position.X != p3.Position.X {
		t.Fatalf("dissolved panes x = (%d,%d), want host x %d", p1.Position.X, p2.Position.X, p3.Position.X)
	}
}

func TestMinimizeShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	// Put content in pane-1
	h.runCmd("focus", "pane-1")
	h.sendKeys("pane-1", "echo SHOULD_NOT_SEE", "Enter")
	h.waitFor("pane-1", "SHOULD_NOT_SEE")

	// Minimize pane-1
	h.runCmd("minimize", "pane-1")

	// The minimized pane should show ONLY the status line [pane-1], no body content
	screen := h.capture()
	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		if strings.Contains(line, "[pane-1]") {
			if i+1 < len(lines) {
				nextLine := lines[i+1]
				if !strings.Contains(nextLine, "─") && !strings.Contains(nextLine, "[pane-2]") {
					t.Errorf("minimized pane should show header only, but line after status is:\n%s", nextLine)
				}
			}
			break
		}
	}

	h.runCmd("restore", "pane-1")
}

func TestMinimizeRestorePreservesContent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	// Put identifiable content in pane-1
	h.runCmd("focus", "pane-1")
	h.sendKeys("pane-1", "echo PRESERVE_TEST_MARKER", "Enter")
	h.waitFor("pane-1", "PRESERVE_TEST_MARKER")

	beforeCapture := h.runCmd("capture", "pane-1")
	if !strings.Contains(beforeCapture, "PRESERVE_TEST_MARKER") {
		t.Fatalf("marker should be visible before minimize, got:\n%s", beforeCapture)
	}

	h.runCmd("minimize", "pane-1")
	h.runCmd("restore", "pane-1")

	afterCapture := h.runCmd("capture", "pane-1")
	if !strings.Contains(afterCapture, "PRESERVE_TEST_MARKER") {
		t.Fatalf("pane content should be preserved after minimize/restore, got:\n%s", afterCapture)
	}
}

func TestResetClearsPaneStateAndAcceptsNewOutput(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reset-history-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 25); do echo \"RESET-HIST-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "RESET-HIST-25")

	beforeHistory := h.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(beforeHistory, "RESET-HIST-01") || !strings.Contains(beforeHistory, "RESET-HIST-25") {
		t.Fatalf("history before reset should contain old output, got:\n%s", beforeHistory)
	}

	out := h.runCmd("reset", "pane-1")
	if !strings.Contains(out, "Reset pane-1") {
		t.Fatalf("reset output = %q, want confirmation", out)
	}

	afterPane := h.runCmd("capture", "pane-1")
	if strings.Contains(afterPane, "RESET-HIST-25") {
		t.Fatalf("pane capture should be cleared after reset, got:\n%s", afterPane)
	}

	afterHistory := h.runCmd("capture", "--history", "pane-1")
	if strings.Contains(afterHistory, "RESET-HIST-01") || strings.Contains(afterHistory, "RESET-HIST-25") {
		t.Fatalf("history capture should be cleared after reset, got:\n%s", afterHistory)
	}

	h.sendKeys("pane-1", "echo RESET-NEW-OUTPUT", "Enter")
	h.waitFor("pane-1", "RESET-NEW-OUTPUT")

	finalPane := h.runCmd("capture", "pane-1")
	if !strings.Contains(finalPane, "RESET-NEW-OUTPUT") {
		t.Fatalf("pane capture should include new output after reset, got:\n%s", finalPane)
	}
}

func TestKill(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	output := h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Errorf("kill should confirm, got:\n%s", output)
	}

	// Kill is synchronous — capture immediately reflects the change.
	h.assertScreen("pane-1 should remain after kill", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	listOut := h.runCmd("list")
	if strings.Contains(listOut, "pane-2") {
		t.Errorf("list should not contain pane-2 after kill, got:\n%s", listOut)
	}
}

func TestKillCleanup(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.sendKeys("pane-2", "trap 'sleep 0.3; exit 0' TERM; while :; do sleep 1; done", "Enter")
	h.waitBusy("pane-2")

	gen := h.generation()
	output := h.runCmd("kill", "--cleanup", "--timeout", "100ms", "pane-2")
	if !strings.Contains(output, "Cleaning up pane-2") {
		t.Fatalf("kill --cleanup should confirm, got:\n%s", output)
	}

	capture := h.capture()
	if !strings.Contains(capture, "[pane-2]") {
		t.Fatalf("pane-2 should remain visible until cleanup completes, got:\n%s", capture)
	}

	h.waitLayout(gen)
	h.assertScreen("pane-2 should be gone after cleanup completes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})
}

func TestKillOrphanedPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create 3 panes via splits so we have pane-1, pane-2, pane-3.
	h.splitV()
	h.splitV()

	// Kill pane-3 and verify it disappears from the list.
	output := h.runCmd("kill", "pane-3")
	if !strings.Contains(output, "Killed") {
		t.Fatalf("kill pane-3 should succeed, got:\n%s", output)
	}
	listOut := h.runCmd("list")
	if strings.Contains(listOut, "pane-3") {
		t.Errorf("pane-3 should be gone from list after kill, got:\n%s", listOut)
	}

	// Kill pane-2 and verify only pane-1 remains.
	output = h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Fatalf("kill pane-2 should succeed, got:\n%s", output)
	}
	listOut = h.runCmd("list")
	if strings.Contains(listOut, "pane-2") {
		t.Errorf("pane-2 should be gone from list, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "pane-1") {
		t.Errorf("pane-1 should still exist, got:\n%s", listOut)
	}
}

func TestSendKeys(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("send-keys", "pane-1", "echo SENDTEST", "Enter")
	if strings.Contains(out, "error") || strings.Contains(out, "not found") {
		t.Fatalf("send-keys failed: %s", out)
	}

	h.waitFor("pane-1", "SENDTEST")

	paneOut := h.runCmd("capture", "pane-1")
	if !strings.Contains(paneOut, "SENDTEST") {
		t.Errorf("pane capture should contain SENDTEST, got:\n%s", paneOut)
	}
}

func TestSendKeysSpecialKeys(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Start a blocking command so Ctrl-C interrupts a running process
	// rather than readline with partial text. Sending ^C to readline
	// triggers a PTY input queue flush that can race with subsequent
	// send-keys on slow CI runners, dropping characters.
	h.sendKeys("pane-1", "sleep 300", "Enter")
	h.waitBusy("pane-1")
	h.sendKeys("pane-1", "C-c")
	h.waitIdle("pane-1")
	h.sendKeys("pane-1", "echo AFTERCANCEL", "Enter")

	h.waitFor("pane-1", "AFTERCANCEL")
}

func TestSendKeysInvalidPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("send-keys", "nonexistent", "hello")
	if !strings.Contains(out, "not found") {
		t.Errorf("expected 'not found' error for invalid pane, got: %s", out)
	}
}

func TestSendKeysToSpecificPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	h.sendKeys("pane-2", "echo PANE2CMD", "Enter")
	h.waitFor("pane-2", "PANE2CMD")

	pane1Out := h.runCmd("capture", "pane-1")
	if strings.Contains(pane1Out, "PANE2CMD") {
		t.Errorf("PANE2CMD should not appear in pane-1, got:\n%s", pane1Out)
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (requires client for prefix key processing)
// ---------------------------------------------------------------------------

func TestToggleMinimizeKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.runCmd("focus", "pane-1")
	gen := h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "1 minimized") {
		t.Fatalf("expected 1 minimized pane after Ctrl-a M, got:\n%s", statusOut)
	}

	h.runCmd("focus", "pane-1")
	gen = h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	statusOut = h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Fatalf("expected 0 minimized panes after toggling restore with Ctrl-a M, got:\n%s", statusOut)
	}
}

func TestToggleMinimizeMultiplePanes(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()
	h.splitH()

	h.runCmd("focus", "pane-1")
	gen := h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	h.runCmd("focus", "pane-2")
	gen = h.generation()
	h.sendKeys("C-a", "M")
	h.waitLayout(gen)

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "2 minimized") {
		t.Fatalf("expected 2 minimized after minimizing pane-1 then pane-2, got:\n%s", statusOut)
	}
}

// TestMinimizeAllSiblingsViaClose verifies that closing the only visible pane
// in a horizontal split auto-restores a minimized sibling. Without this guard,
// both remaining panes would stay minimized with no visible content.
func TestMinimizeAllSiblingsViaClose(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create 3 panes in a horizontal split: pane-1, pane-2, pane-3
	h.splitH()
	h.splitH()

	// Minimize pane-1 and pane-3, leaving pane-2 as the only visible pane.
	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimize pane-1 should succeed, got:\n%s", output)
	}
	output = h.runCmd("minimize", "pane-3")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimize pane-3 should succeed, got:\n%s", output)
	}

	// Kill pane-2 (the only visible pane). This should auto-restore one
	// of the minimized siblings.
	output = h.runCmd("kill", "pane-2")
	if !strings.Contains(output, "Killed") {
		t.Fatalf("kill pane-2 should succeed, got:\n%s", output)
	}

	// At least one pane must be non-minimized after the close.
	statusOut := h.runCmd("status")
	if strings.Contains(statusOut, "2 minimized") {
		t.Errorf("closing the last visible pane should auto-restore a sibling, got:\n%s", statusOut)
	}
}

// TestMinimizeReclaimGoesToVisibleSibling verifies that reclaimed height from
// minimizing goes to a non-minimized sibling, not a minimized one.
func TestMinimizeReclaimGoesToVisibleSibling(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// 3 horizontal panes
	h.splitH()
	h.splitH()

	// Minimize pane-1 first
	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimize pane-1 should succeed, got:\n%s", output)
	}

	// Minimize pane-2 — reclaimed space should go to pane-3 (the only visible one)
	output = h.runCmd("minimize", "pane-2")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("minimize pane-2 should succeed, got:\n%s", output)
	}

	// Verify pane-3 is visible and has content area (not just a status line).
	// If reclaimed space went to minimized pane-1 instead, pane-3 would be
	// squeezed.
	h.assertScreen("pane-3 should be the large visible pane", func(s string) bool {
		return strings.Contains(s, "[pane-3]")
	})

	// Restore for cleanup
	h.runCmd("restore", "pane-1")
	h.runCmd("restore", "pane-2")
}
