package test

import (
	"strings"
	"testing"
	"time"
)

func TestFocusCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.assertScreen("pane-2 should be active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	h.sendKeys("C-a", "o")
	time.Sleep(500 * time.Millisecond)

	h.assertScreen("pane-1 active, pane-2 inactive after cycle", func(s string) bool {
		return isPaneActive(s, "pane-1") && isPaneInactive(s, "pane-2")
	})
}

func TestFocusNavigationThreePanes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-3]", 3*time.Second)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		h.sendKeys("C-a", "o")
		time.Sleep(400 * time.Millisecond)
		screen := h.capture()
		for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
			if isPaneActive(screen, name) {
				seen[name] = true
			}
		}
	}

	for _, name := range []string{"pane-1", "pane-2", "pane-3"} {
		if !seen[name] {
			t.Errorf("focus cycle never reached %s (saw: %v)", name, seen)
		}
	}
}

func TestDirectionalFocusAfterRootSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Create: pane-1 top-left, pane-2 bottom-left, pane-3 right (root split)
	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "|")
	h.waitFor("[pane-3]", 3*time.Second)

	// pane-3 is active (rightmost). Navigate left with h.
	h.sendKeys("C-a", "h")
	time.Sleep(400 * time.Millisecond)

	screen := h.capture()
	if !isPaneActive(screen, "pane-1") && !isPaneActive(screen, "pane-2") {
		t.Errorf("Ctrl-a h from pane-3 should focus a left pane\nScreen:\n%s", screen)
	}

	// Navigate right with l — should go back to pane-3
	h.sendKeys("C-a", "l")
	time.Sleep(400 * time.Millisecond)

	h.assertScreen("Ctrl-a l should focus pane-3", func(s string) bool {
		return isPaneActive(s, "pane-3")
	})

	// Navigate up/down between pane-1 and pane-2
	h.sendKeys("C-a", "h")
	time.Sleep(400 * time.Millisecond)

	screen = h.capture()
	var activeName string
	if isPaneActive(screen, "pane-1") {
		activeName = "pane-1"
	} else if isPaneActive(screen, "pane-2") {
		activeName = "pane-2"
	}

	if activeName == "" {
		t.Fatal("no left pane is active")
	}

	if activeName == "pane-1" {
		h.sendKeys("C-a", "j") // down to pane-2
		time.Sleep(400 * time.Millisecond)
		h.assertScreen("j from pane-1 should reach pane-2", func(s string) bool {
			return isPaneActive(s, "pane-2")
		})
	} else {
		h.sendKeys("C-a", "k") // up to pane-1
		time.Sleep(400 * time.Millisecond)
		h.assertScreen("k from pane-2 should reach pane-1", func(s string) bool {
			return isPaneActive(s, "pane-1")
		})
	}
}

func TestNavigateBackToRightPaneAfterRootHSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	// Root horizontal split: top (pane-1 | pane-2), bottom (pane-3)
	h.sendKeys("C-a", "_")
	h.waitFor("[pane-3]", 3*time.Second)

	// pane-3 is active (bottom). Navigate up with k.
	h.sendKeys("C-a", "k")
	time.Sleep(400 * time.Millisecond)

	screen := h.capture()
	if !isPaneActive(screen, "pane-1") && !isPaneActive(screen, "pane-2") {
		t.Fatalf("k from pane-3 should focus a top pane\nScreen:\n%s", screen)
	}

	// Now navigate right with l to reach pane-2
	h.sendKeys("C-a", "l")
	time.Sleep(400 * time.Millisecond)

	h.assertScreen("l should reach pane-2 (right side of top row)", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}

func TestFocusByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.assertScreen("pane-2 active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	output := h.runCmd("focus", "pane-1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)

	h.assertScreen("pane-1 active after focus by name", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	h.assertScreen("pane-2 inactive after focus by name", func(s string) bool {
		return isPaneInactive(s, "pane-2")
	})
}

func TestFocusByID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	output := h.runCmd("focus", "1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus by ID should confirm, got:\n%s", output)
	}

	time.Sleep(500 * time.Millisecond)

	h.assertScreen("pane-1 active after focus by ID", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})
}

func TestFocusNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	output := h.runCmd("focus", "nonexistent")
	if !strings.Contains(output, "not found") {
		t.Errorf("focus of nonexistent pane should report error, got:\n%s", output)
	}
}
