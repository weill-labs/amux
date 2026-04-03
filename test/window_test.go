package test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
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

func TestNewWindowCLIFirstPaneDefaultsToLead(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.runCmd("new-window")

	h.assertScreen("pane-2 should show lead status", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && strings.Contains(s, "[lead]")
	})

	capture := h.captureJSON()
	pane := h.jsonPane(capture, "pane-2")
	if !pane.Lead {
		t.Fatal("pane-2 should be marked lead in full-screen capture")
	}

	single := h.runCmd("capture", "--format", "json", "pane-2")
	var singlePane proto.CapturePane
	if err := json.Unmarshal([]byte(single), &singlePane); err != nil {
		t.Fatalf("unmarshal pane-2 capture: %v\nraw output:\n%s", err, single)
	}
	if !singlePane.Lead {
		t.Fatal("pane-2 should be marked lead in single-pane capture")
	}

	line := listLineForPane(h.runCmd("list"), "pane-2")
	if !strings.Contains(line, "lead") {
		t.Fatalf("pane-2 list line should include lead metadata, got: %q", line)
	}
}

func TestNewWindowLeadPaneCanSpawnIntoAnchoredLayout(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.runCmd("new-window")

	out := h.runCmd("spawn", "--name", "worker")
	if strings.Contains(out, "cannot operate on lead pane") {
		t.Fatalf("spawn should not reject the initial lead pane, got: %s", out)
	}

	capture := h.captureJSON()
	assertAnchoredLeadSpawnLayout(t, h, capture, "pane-2", "worker")
}

func TestNewWindowLeadPaneSpawnIgnoresOuterActorPaneEnv(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithOptions(t, 80, 24, "", false, false, "AMUX_PANE=1")
	h.runCmd("new-window")

	out := h.runCmd("spawn", "--name", "worker")
	if strings.Contains(out, "cannot operate on lead pane") {
		t.Fatalf("spawn should not reject the initial lead pane, got: %s", out)
	}

	capture := h.captureJSON()
	assertAnchoredLeadSpawnLayout(t, h, capture, "pane-2", "worker")
}

func TestSetLeadSinglePaneWindowSurvivesCollapseAndRegrowsAnchoredLayout(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	setLead(t, h, "pane-1")

	capture := h.captureJSON()
	pane := h.jsonPane(capture, "pane-1")
	if !pane.Lead {
		t.Fatal("pane-1 should be marked lead after set-lead on a single-pane window")
	}

	single := h.runCmd("capture", "--format", "json", "pane-1")
	var singlePane proto.CapturePane
	if err := json.Unmarshal([]byte(single), &singlePane); err != nil {
		t.Fatalf("unmarshal pane-1 capture: %v\nraw output:\n%s", err, single)
	}
	if !singlePane.Lead {
		t.Fatal("pane-1 should be marked lead in single-pane capture after set-lead")
	}

	gen := h.generation()
	out := h.runCmd("split", "pane-1", "v")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		t.Fatalf("split after single-pane set-lead failed: %s", out)
	}
	h.waitLayout(gen)

	capture = h.captureJSON()
	assertAnchoredLeadSpawnLayout(t, h, capture, "pane-1", "pane-2")

	gen = h.generation()
	out = h.runCmd("kill", "pane-2")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("kill pane-2 failed: %s", out)
	}
	h.waitLayout(gen)

	capture = h.captureJSON()
	if len(capture.Panes) != 1 {
		t.Fatalf("expected 1 pane after collapse, got %d", len(capture.Panes))
	}
	pane = h.jsonPane(capture, "pane-1")
	if !pane.Lead {
		t.Fatal("pane-1 should remain lead after the window collapses back to one pane")
	}

	gen = h.generation()
	out = h.runCmd("split", "pane-1", "h")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		t.Fatalf("split after collapse failed: %s", out)
	}
	h.waitLayout(gen)

	capture = h.captureJSON()
	assertAnchoredLeadSpawnLayout(t, h, capture, "pane-1", "pane-3")
}

func listLineForPane(listOut, paneName string) string {
	for _, line := range strings.Split(listOut, "\n") {
		if strings.Contains(line, paneName) {
			return line
		}
	}
	return ""
}

func assertAnchoredLeadSpawnLayout(t *testing.T, h *ServerHarness, capture proto.CaptureJSON, leadName, workerName string) {
	t.Helper()

	lead := h.jsonPane(capture, leadName)
	worker := h.jsonPane(capture, workerName)
	if !lead.Lead {
		t.Fatalf("%s should remain the lead pane after spawn", leadName)
	}
	if worker.Lead {
		t.Fatalf("%s should not be marked as lead", workerName)
	}
	if lead.Position == nil || worker.Position == nil {
		t.Fatalf("spawned lead layout should include positions, lead=%+v worker=%+v", lead.Position, worker.Position)
	}
	if lead.Position.X >= worker.Position.X {
		t.Fatalf("lead pane should stay left of worker: lead.x=%d worker.x=%d", lead.Position.X, worker.Position.X)
	}
	if lead.Position.Y != worker.Position.Y || lead.Position.Height != worker.Position.Height {
		t.Fatalf("lead spawn should materialize a side-by-side layout: lead=%+v worker=%+v", lead.Position, worker.Position)
	}
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
	out := h.runCmd("kill", "pane-2")
	if !strings.Contains(out, "closed") {
		t.Errorf("kill last pane in window should report closure, got: %s", out)
	}

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

func TestWindowSwitchResyncsStaleCursorState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		switchFn func(*ServerHarness)
	}{
		{
			name: "select-window",
			switchFn: func(h *ServerHarness) {
				h.runCmd("select-window", "1")
			},
		},
		{
			name: "next-window",
			switchFn: func(h *ServerHarness) {
				h.runCmd("next-window")
			},
		},
		{
			name: "prev-window",
			switchFn: func(h *ServerHarness) {
				h.runCmd("prev-window")
			},
		},
		{
			name: "last-window",
			switchFn: func(h *ServerHarness) {
				h.runCmd("last-window")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newServerHarnessWithSize(t, 255, 62)
			h.waitFor("pane-1", "$")

			healthyCapture := h.captureJSON()
			healthy := h.jsonPane(healthyCapture, "pane-1")

			h.runCmd("new-window")
			h.waitFor("pane-2", "$")

			// Simulate stale client-side cursor state for the hidden pane in window 1.
			h.client.renderer.HandlePaneOutput(1, []byte("\033[1;24H"))

			var before proto.CapturePane
			if err := json.Unmarshal([]byte(h.client.renderer.CapturePaneJSON(1, nil)), &before); err != nil {
				t.Fatalf("unmarshal pane-1 before switch: %v", err)
			}
			if got := before.Cursor.Col; got != 23 {
				t.Fatalf("precondition failed: pane-1 cursor col = %d, want 23", got)
			}

			tt.switchFn(h)

			afterCapture := h.captureJSON()
			after := h.jsonPane(afterCapture, "pane-1")
			if got, want := after.Content[0], healthy.Content[0]; got != want {
				t.Fatalf("pane-1 content after switch = %q, want %q", got, want)
			}
			if got, want := after.Cursor.Col, healthy.Cursor.Col; got != want {
				t.Fatalf("pane-1 cursor col after switch = %d, want %d", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (inner amux inside outer server)
// ---------------------------------------------------------------------------

func TestNewWindowKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

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
	h := newAmuxHarness(t)

	// Create a second window
	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("new window did not appear.\nScreen:\n%s", h.capture())
	}

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
	h := newAmuxHarness(t)

	// Create 2 more windows (total 3)
	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}
	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-3]", 3*time.Second) {
		t.Fatalf("window 3 did not appear.\nScreen:\n%s", h.capture())
	}

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

func TestLastWindowKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}

	h.sendKeys("C-a", ";")
	if !h.waitFor("[pane-1]", 3*time.Second) {
		t.Fatalf("Ctrl-a ; should switch back to pane-1.\nScreen:\n%s", h.capture())
	}

	h.sendKeys("C-a", ";")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("second Ctrl-a ; should switch back to pane-2.\nScreen:\n%s", h.capture())
	}
}

func TestWindowPaneIsolation(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Type something in window 1
	h.sendKeys("e", "c", "h", "o", " ", "W", "I", "N", "1", "Enter")
	if !h.waitFor("WIN1", 3*time.Second) {
		t.Fatalf("WIN1 should appear in window 1.\nScreen:\n%s", h.capture())
	}

	// Create window 2 and type something different
	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}
	h.sendKeys("e", "c", "h", "o", " ", "W", "I", "N", "2", "Enter")
	if !h.waitFor("WIN2", 3*time.Second) {
		t.Fatalf("WIN2 should appear in window 2.\nScreen:\n%s", h.capture())
	}

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
	h := newAmuxHarness(t)

	// Create window 2
	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}

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
