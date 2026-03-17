package test

import (
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
	if !strings.Contains(output, "cannot") {
		t.Errorf("minimizing sole pane in column should fail, got:\n%s", output)
	}

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Errorf("no panes should be minimized, got:\n%s", statusOut)
	}
}

func TestMinimizeLastPaneInColumnFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	output := h.runCmd("minimize", "pane-1")
	if !strings.Contains(output, "Minimized") {
		t.Fatalf("first minimize should succeed, got:\n%s", output)
	}

	output = h.runCmd("minimize", "pane-2")
	if !strings.Contains(output, "cannot") {
		t.Errorf("minimizing last visible pane in column should fail, got:\n%s", output)
	}

	h.runCmd("restore", "pane-1")
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

	h.runCmd("send-keys", "pane-1", "partial-text")
	h.runCmd("send-keys", "pane-1", "C-c")
	// Wait for ^C to appear on screen, proving the shell processed the
	// interrupt. Waiting for "$" is unreliable since the initial prompt
	// already has a "$" on screen.
	h.waitFor("pane-1", "^C")
	h.runCmd("send-keys", "pane-1", "echo AFTERCANCEL", "Enter")

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
	h.sendKeys("C-a", "m")
	h.waitLayout(gen)

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "1 minimized") {
		t.Fatalf("expected 1 minimized pane after Ctrl-a m, got:\n%s", statusOut)
	}

	h.runCmd("focus", "pane-1")
	gen = h.generation()
	h.sendKeys("C-a", "m")
	h.waitLayout(gen)

	statusOut = h.runCmd("status")
	if !strings.Contains(statusOut, "0 minimized") {
		t.Fatalf("expected 0 minimized panes after toggling restore, got:\n%s", statusOut)
	}
}

func TestToggleMinimizeMultiplePanes(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()
	h.splitH()

	h.runCmd("focus", "pane-1")
	gen := h.generation()
	h.sendKeys("C-a", "m")
	h.waitLayout(gen)

	h.runCmd("focus", "pane-2")
	gen = h.generation()
	h.sendKeys("C-a", "m")
	h.waitLayout(gen)

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "2 minimized") {
		t.Fatalf("expected 2 minimized after minimizing pane-1 then pane-2, got:\n%s", statusOut)
	}
}
