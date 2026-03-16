package test

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestFocusByName(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	h.assertScreen("pane-2 active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	output := h.runCmd("focus", "pane-1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus should confirm, got:\n%s", output)
	}

	h.assertScreen("pane-1 active after focus by name", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	h.assertScreen("pane-2 inactive after focus by name", func(s string) bool {
		return isPaneInactive(s, "pane-2")
	})
}

func TestFocusByID(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	output := h.runCmd("focus", "1")
	if !strings.Contains(output, "Focused") {
		t.Errorf("focus by ID should confirm, got:\n%s", output)
	}

	h.assertScreen("pane-1 active after focus by ID", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})
}

func TestFocusNotFound(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("focus", "nonexistent")
	if !strings.Contains(output, "not found") {
		t.Errorf("focus of nonexistent pane should report error, got:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (inner amux inside outer server)
// ---------------------------------------------------------------------------

func TestFocusCycle(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	h.assertScreen("pane-2 should be active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	gen := h.generation()
	h.sendKeys("C-a", "o")
	h.waitLayout(gen)

	h.assertScreen("pane-1 active, pane-2 inactive after cycle", func(s string) bool {
		return isPaneActive(s, "pane-1") && isPaneInactive(s, "pane-2")
	})
}

func TestFocusNavigationThreePanes(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()
	h.splitH()

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		gen := h.generation()
		h.sendKeys("C-a", "o")
		h.waitLayout(gen)
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
	h := newAmuxHarness(t)

	// Create: pane-1 top-left, pane-2 bottom-left, pane-3 right (root split)
	h.splitH()
	h.splitRootV()

	// pane-3 is active (rightmost). Navigate left with h.
	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	screen := h.capture()
	if !isPaneActive(screen, "pane-1") && !isPaneActive(screen, "pane-2") {
		t.Errorf("Ctrl-a h from pane-3 should focus a left pane\nScreen:\n%s", screen)
	}

	// Navigate right with l — should go back to pane-3
	gen = h.generation()
	h.sendKeys("C-a", "l")
	h.waitLayout(gen)

	h.assertScreen("Ctrl-a l should focus pane-3", func(s string) bool {
		return isPaneActive(s, "pane-3")
	})

	// Navigate up/down between pane-1 and pane-2
	gen = h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

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
		gen = h.generation()
		h.sendKeys("C-a", "j") // down to pane-2
		h.waitLayout(gen)
		h.assertScreen("j from pane-1 should reach pane-2", func(s string) bool {
			return isPaneActive(s, "pane-2")
		})
	} else {
		gen = h.generation()
		h.sendKeys("C-a", "k") // up to pane-1
		h.waitLayout(gen)
		h.assertScreen("k from pane-2 should reach pane-1", func(s string) bool {
			return isPaneActive(s, "pane-1")
		})
	}
}

func TestNavigateBackToRightPaneAfterRootHSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	// Root horizontal split: top (pane-1 | pane-2), bottom (pane-3)
	h.splitRootH()

	// pane-3 is active (bottom). Navigate up with k.
	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	screen := h.capture()
	if !isPaneActive(screen, "pane-1") && !isPaneActive(screen, "pane-2") {
		t.Fatalf("k from pane-3 should focus a top pane\nScreen:\n%s", screen)
	}

	// Now navigate right with l to reach pane-2
	gen = h.generation()
	h.sendKeys("C-a", "l")
	h.waitLayout(gen)

	h.assertScreen("l should reach pane-2 (right side of top row)", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}

func TestPrefixArrowFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split horizontally: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	h.assertScreen("pane-2 active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	// Prefix + Left arrow should focus pane-1
	gen := h.generation()
	h.sendKeys("C-a", "Left")
	h.waitLayout(gen)

	h.assertScreen("prefix+Left should focus pane-1", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	// Prefix + Right arrow should focus pane-2
	gen = h.generation()
	h.sendKeys("C-a", "Right")
	h.waitLayout(gen)

	h.assertScreen("prefix+Right should focus pane-2", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}

func TestAltHJKLFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split horizontally: pane-1 (left) | pane-2 (right, active)
	h.splitV()

	h.assertScreen("pane-2 active after split", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})

	// Alt+h should focus left (pane-1)
	gen := h.generation()
	h.sendKeys("M-h")
	h.waitLayout(gen)

	h.assertScreen("Alt+h should focus pane-1", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	// Alt+l should focus right (pane-2)
	gen = h.generation()
	h.sendKeys("M-l")
	h.waitLayout(gen)

	h.assertScreen("Alt+l should focus pane-2", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}

func TestAltHJKLFocusVertical(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split vertically: pane-1 (top) / pane-2 (bottom, active)
	h.splitH()

	// Alt+k should focus up (pane-1)
	gen := h.generation()
	h.sendKeys("M-k")
	h.waitLayout(gen)

	h.assertScreen("Alt+k should focus pane-1", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	// Alt+j should focus down (pane-2)
	gen = h.generation()
	h.sendKeys("M-j")
	h.waitLayout(gen)

	h.assertScreen("Alt+j should focus pane-2", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}

func TestPrefixArrowFocusVertical(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Split vertically: pane-1 (top) / pane-2 (bottom, active)
	h.splitH()

	// Prefix + Up arrow should focus pane-1
	gen := h.generation()
	h.sendKeys("C-a", "Up")
	h.waitLayout(gen)

	h.assertScreen("prefix+Up should focus pane-1", func(s string) bool {
		return isPaneActive(s, "pane-1")
	})

	// Prefix + Down arrow should focus pane-2
	gen = h.generation()
	h.sendKeys("C-a", "Down")
	h.waitLayout(gen)

	h.assertScreen("prefix+Down should focus pane-2", func(s string) bool {
		return isPaneActive(s, "pane-2")
	})
}
