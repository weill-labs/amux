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
