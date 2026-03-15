package test

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CLI-only tests — ServerHarness (zero polling, zero sleep)
// ---------------------------------------------------------------------------

func TestNewWindowCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create new window via CLI
	out := h.runCmd("new-window")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("new-window command not recognized: %s", out)
	}

	// Should switch to the new window showing pane-2
	h.assertScreen("pane-2 should be visible in new window", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})

	// pane-1 should not be visible
	h.assertScreen("pane-1 should not be visible after new-window", func(s string) bool {
		return !strings.Contains(s, "[pane-1]")
	})
}

func TestListWindows(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create a second window
	h.runCmd("new-window")

	// list-windows should show both
	out := h.runCmd("list-windows")
	if !strings.Contains(out, "1:") || !strings.Contains(out, "2:") {
		t.Errorf("list-windows should show 2 windows, got:\n%s", out)
	}
}

func TestWindowAutoCloseOnLastPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create a second window
	h.runCmd("new-window")

	// Kill the pane in window 2 — window should close, switch to window 1
	h.runCmd("kill", "pane-2")

	h.assertScreen("should show pane-1 after window 2 closes", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	// Only one window should remain
	bar := h.globalBar()
	if hasWindowTab(bar, 2) {
		t.Errorf("window 2 should be gone from global bar, got: %q", bar)
	}
}

func TestSelectWindowCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create second window
	h.runCmd("new-window")

	// Switch back via CLI
	out := h.runCmd("select-window", "1")
	if strings.Contains(out, "unknown command") {
		t.Fatalf("select-window not recognized: %s", out)
	}

	h.assertScreen("select-window 1 should show pane-1", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})
}

func TestNextPrevWindowCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("new-window")

	// prev-window via CLI
	h.runCmd("prev-window")
	h.assertScreen("prev-window should show pane-1", func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	})

	// next-window via CLI
	h.runCmd("next-window")
	h.assertScreen("next-window should show pane-2", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})
}

// ---------------------------------------------------------------------------
// Keybinding tests — TmuxHarness (requires real terminal for key simulation)
// ---------------------------------------------------------------------------

func TestNewWindowKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Start with one window showing pane-1
	h.waitFor("[pane-1]", 3*time.Second)

	// Create a new window via Ctrl-a c
	h.sendKeys("C-a", "c")

	// New window should show pane-2 (new pane in new window)
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("new window did not appear.\nScreen:\n%s", h.capture())
	}

	// pane-1 should NOT be visible (it's in window 1, we're in window 2)
	h.assertScreen("pane-1 should not be visible in window 2", func(s string) bool {
		return !strings.Contains(s, "[pane-1]")
	})

	// Global bar should show 2 windows
	bar := h.globalBar()
	if !hasWindowTab(bar, 1) || !hasWindowTab(bar, 2) {
		t.Errorf("global bar should show 2 windows, got: %q", bar)
	}
}

func TestNextPrevWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.waitFor("[pane-1]", 3*time.Second)

	// Create a second window
	h.sendKeys("C-a", "c")
	h.waitFor("[pane-2]", 3*time.Second)

	// Go to previous window (Ctrl-a p) — should show pane-1
	h.sendKeys("C-a", "p")
	if !h.waitFor("[pane-1]", 3*time.Second) {
		t.Fatalf("prev window should show pane-1.\nScreen:\n%s", h.capture())
	}

	// Go to next window (Ctrl-a n) — should show pane-2
	h.sendKeys("C-a", "n")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("next window should show pane-2.\nScreen:\n%s", h.capture())
	}

	// Next again wraps to window 1
	h.sendKeys("C-a", "n")
	if !h.waitFor("[pane-1]", 3*time.Second) {
		t.Fatalf("next window should wrap to pane-1.\nScreen:\n%s", h.capture())
	}
}

func TestSelectWindowByNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.waitFor("[pane-1]", 3*time.Second)

	// Create 2 more windows (total 3)
	h.sendKeys("C-a", "c")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("C-a", "c")
	h.waitFor("[pane-3]", 3*time.Second)

	// Ctrl-a 1 → window 1 (pane-1)
	h.sendKeys("C-a", "1")
	if !h.waitFor("[pane-1]", 3*time.Second) {
		t.Fatalf("Ctrl-a 1 should select window 1.\nScreen:\n%s", h.capture())
	}

	// Ctrl-a 3 → window 3 (pane-3)
	h.sendKeys("C-a", "3")
	if !h.waitFor("[pane-3]", 3*time.Second) {
		t.Fatalf("Ctrl-a 3 should select window 3.\nScreen:\n%s", h.capture())
	}

	// Ctrl-a 2 → window 2 (pane-2)
	h.sendKeys("C-a", "2")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("Ctrl-a 2 should select window 2.\nScreen:\n%s", h.capture())
	}
}

func TestWindowPaneIsolation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.waitFor("[pane-1]", 3*time.Second)

	// Type something in window 1
	h.sendKeys("e", "c", "h", "o", " ", "W", "I", "N", "1", "Enter")
	h.waitFor("WIN1", 3*time.Second)

	// Create window 2 and type something different
	h.sendKeys("C-a", "c")
	h.waitFor("[pane-2]", 3*time.Second)
	h.sendKeys("e", "c", "h", "o", " ", "W", "I", "N", "2", "Enter")
	h.waitFor("WIN2", 3*time.Second)

	// Window 2 should show WIN2 but not WIN1
	h.assertScreen("window 2 should show WIN2", func(s string) bool {
		return strings.Contains(s, "WIN2")
	})
	h.assertScreen("window 2 should not show WIN1", func(s string) bool {
		return !strings.Contains(s, "WIN1")
	})

	// Switch back to window 1
	h.sendKeys("C-a", "1")
	if !h.waitFor("WIN1", 3*time.Second) {
		t.Fatalf("window 1 should show WIN1.\nScreen:\n%s", h.capture())
	}

	// Window 1 should not show WIN2
	h.assertScreen("window 1 should not show WIN2", func(s string) bool {
		return !strings.Contains(s, "WIN2")
	})
}

func TestSplitWithinWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.waitFor("[pane-1]", 3*time.Second)

	// Create window 2
	h.sendKeys("C-a", "c")
	h.waitFor("[pane-2]", 3*time.Second)

	// Split within window 2
	h.splitV()

	// Both pane-2 and pane-3 should be visible (same window)
	h.assertScreen("window 2 should show both pane-2 and pane-3", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && strings.Contains(s, "[pane-3]")
	})

	// Switch to window 1 — should only show pane-1
	h.sendKeys("C-a", "1")
	if !h.waitFor("[pane-1]", 3*time.Second) {
		t.Fatalf("window 1 should show pane-1.\nScreen:\n%s", h.capture())
	}
	h.assertScreen("window 1 should not show pane-2 or pane-3", func(s string) bool {
		return !strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-3]")
	})
}
