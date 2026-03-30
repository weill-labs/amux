package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	h.assertScreen("unzoom should reveal the spawned pane", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]") &&
			strings.Contains(s, "[bg-worker]")
	})
}

func TestSplitKeepsFocusByDefault(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	output := h.runCmd("split", "pane-1", "--name", "bg-split")
	if !strings.Contains(output, "bg-split") {
		t.Fatalf("split should report the new pane name, got:\n%s", output)
	}

	capture := h.captureJSON()
	bgSplit := h.jsonPane(capture, "bg-split")
	if bgSplit.Active {
		t.Fatal("bg-split should not become active after split")
	}
	if !h.jsonPane(capture, "pane-1").Active {
		t.Fatal("pane-1 should remain active after split")
	}
	h.assertScreen("split should still be visible when not zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[bg-split]")
	})
}

func TestSpawnKeepsFocusByDefault(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	output := h.runCmd("spawn", "--name", "bg-worker", "--task", "TASK-42")
	if !strings.Contains(output, "bg-worker") {
		t.Fatalf("spawn should report the new pane name, got:\n%s", output)
	}

	capture := h.captureJSON()
	bgWorker := h.jsonPane(capture, "bg-worker")
	if bgWorker.Active {
		t.Fatal("bg-worker should not become active after spawn")
	}
	if !h.jsonPane(capture, "pane-1").Active {
		t.Fatal("pane-1 should remain active after spawn")
	}
	h.assertScreen("spawn should still be visible when not zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[bg-worker]")
	})
}

func TestSpawnFocusFlagActivatesNewPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.runCmd("focus", "pane-1")

	output := h.runCmd("spawn", "--focus", "--name", "fg-worker", "--task", "TASK-42")
	if !strings.Contains(output, "fg-worker") {
		t.Fatalf("spawn should report the new pane name, got:\n%s", output)
	}

	capture := h.captureJSON()
	if !h.jsonPane(capture, "fg-worker").Active {
		t.Fatal("fg-worker should become active with --focus")
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

// TestShutdownLeavesNoOrphans verifies that when a server shuts down, all
// pane shell processes are reaped — no orphaned children survive.
func TestShutdownLeavesNoOrphans(t *testing.T) {
	t.Parallel()
	h := newServerHarnessPersistent(t)
	h.unsetLead()

	// Create 3 panes
	h.runCmd("split", "pane-1")
	h.runCmd("split", "pane-1")
	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}

	// Collect child PIDs of the server before shutdown
	serverPid := h.cmd.Process.Pid
	childPids := childPidsOf(serverPid)
	if len(childPids) == 0 {
		t.Fatal("server should have child processes (pane shells)")
	}

	// Trigger graceful shutdown
	if err := h.signalServer(os.Interrupt); err != nil {
		t.Fatalf("interrupting server: %v", err)
	}
	done := make(chan struct{})
	go func() {
		h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = h.signalServer(syscall.SIGKILL)
		t.Fatal("server didn't shut down within 10 seconds")
	}

	// Verify all child PIDs are dead (poll instead of sleep)
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range childPids {
		for time.Now().Before(deadline) {
			if syscall.Kill(pid, 0) != nil {
				break
			}
			runtime.Gosched()
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Errorf("child PID %d still alive after server shutdown", pid)
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// childPidsOf returns PIDs of direct children of the given process.
func childPidsOf(pid int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if p, err := strconv.Atoi(line); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}
