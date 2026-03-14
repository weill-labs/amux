package integration

import (
	"strings"
	"testing"
	"time"
)

func TestBasicStartAndDetach(t *testing.T) {
	h := newHarness(t)

	h.assertScreen("should show pane status", func(s string) bool {
		return strings.Contains(s, "[pane-")
	})

	h.assertScreen("should show global status bar", func(s string) bool {
		return strings.Contains(s, "amux")
	})

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)
}

func TestSplitVertical(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("C-a", "\\")

	if !h.waitFor("│", 3*time.Second) {
		t.Fatal("vertical border not found after split")
	}

	h.waitFor("[pane-2]", 3*time.Second)
	h.assertScreen("should show two panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestSplitHorizontal(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("C-a", "-")

	if !h.waitFor("─", 3*time.Second) {
		t.Fatal("horizontal border not found after split")
	}

	h.waitFor("[pane-2]", 3*time.Second)
	h.assertScreen("should show two panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestFocusCycle(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("C-a", "o")
	time.Sleep(500 * time.Millisecond)

	h.assertScreen("pane-1 should have active indicator", func(s string) bool {
		lines := strings.Split(s, "\n")
		for _, line := range lines {
			if strings.Contains(line, "[pane-1]") && strings.Contains(line, "●") {
				return true
			}
		}
		return false
	})
}

func TestPaneClose(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("e", "x", "i", "t", "Enter")

	if !h.waitForFunc(func(s string) bool {
		return !strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		t.Fatal("pane-2 should disappear after exit")
	}

	h.assertScreen("pane-1 should remain", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	h.assertScreen("no pane borders with single pane", func(s string) bool {
		lines := strings.Split(s, "\n")
		for _, line := range lines {
			if strings.Contains(line, "amux") && strings.Contains(line, "panes") {
				continue
			}
			if strings.Contains(line, "│") {
				return false
			}
		}
		return true
	})
}

func TestList(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("list")
	if !strings.Contains(output, "pane-1") {
		t.Errorf("list should contain pane-1, got:\n%s", output)
	}
	if !strings.Contains(output, "pane-2") {
		t.Errorf("list should contain pane-2, got:\n%s", output)
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t)

	output := h.runCmd("status")
	if !strings.Contains(output, "1 total") {
		t.Errorf("status should show 1 total, got:\n%s", output)
	}
}

func TestReattach(t *testing.T) {
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "H", "E", "L", "L", "O", "Enter")
	h.waitFor("HELLO", 3*time.Second)

	h.sendKeys("C-a", "d")
	time.Sleep(500 * time.Millisecond)

	h.sendKeys(amuxBin, "Enter")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("reattach failed, screen:\n%s", screen)
	}

	h.assertScreen("should see HELLO after reattach", func(s string) bool {
		return strings.Contains(s, "HELLO")
	})
}
