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

func TestZoomToggle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	h.assertScreen("both panes visible before zoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Zoomed") {
		t.Errorf("zoom should confirm, got:\n%s", output)
	}

	h.assertScreen("pane-1 should be visible and pane-2 hidden when zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	status := h.runCmd("status")
	if !strings.Contains(status, "zoomed") {
		t.Errorf("status should report zoomed state, got:\n%s", status)
	}

	output = h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Unzoomed") {
		t.Errorf("unzoom should confirm, got:\n%s", output)
	}

	h.assertScreen("both panes should be visible after unzoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestZoomSinglePaneFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "cannot zoom") {
		t.Errorf("zoom should fail with single pane, got:\n%s", output)
	}
}

func TestZoomKillZoomedPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.splitH()

	h.runCmd("zoom", "pane-2")
	h.assertScreen("pane-2 zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	h.runCmd("kill", "pane-2")

	h.assertScreen("killing zoomed pane should unzoom and show remaining panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-3]") &&
			!strings.Contains(s, "[pane-2]")
	})

	status := h.runCmd("status")
	if strings.Contains(status, "zoomed") {
		t.Errorf("status should not report zoomed after kill, got:\n%s", status)
	}
}

func TestZoomAutoUnzoomOnCLIFocus(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	h.runCmd("zoom", "pane-2")
	h.assertScreen("pane-2 zoomed before CLI focus", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	// Focusing a different pane via CLI should auto-unzoom
	h.runCmd("focus", "pane-1")
	h.assertScreen("CLI focus should auto-unzoom and show all panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestZoomCLIFocusSamePaneNoUnzoom(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	h.runCmd("zoom", "pane-2")
	h.assertScreen("pane-2 zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	// Focusing the already-zoomed pane should NOT unzoom
	h.runCmd("focus", "pane-2")
	h.assertScreen("focusing zoomed pane should stay zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})
}

func TestZoomResyncsStaleCursorState(t *testing.T) {
	t.Parallel()
	h := newServerHarnessWithSize(t, 255, 62)

	h.splitH()
	h.waitFor("pane-2", "$")

	healthyCapture := h.captureJSON()
	healthy := h.jsonPane(healthyCapture, "pane-2")

	tests := []struct {
		name string
		zoom func()
	}{
		{
			name: "zoom",
			zoom: func() {
				h.runCmd("zoom", "pane-2")
			},
		},
		{
			name: "unzoom",
			zoom: func() {
				h.runCmd("zoom", "pane-2")
				h.runCmd("zoom", "pane-2")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate stale client-side cursor state for the active pane before zoom transition.
			h.client.renderer.HandlePaneOutput(2, []byte("\033[1;24H"))

			var before proto.CapturePane
			if err := json.Unmarshal([]byte(h.client.renderer.CapturePaneJSON(2, nil)), &before); err != nil {
				t.Fatalf("unmarshal pane-2 before %s: %v", tt.name, err)
			}
			if got := before.Cursor.Col; got != 23 {
				t.Fatalf("precondition failed before %s: pane-2 cursor col = %d, want 23", tt.name, got)
			}

			tt.zoom()

			afterCapture := h.captureJSON()
			after := h.jsonPane(afterCapture, "pane-2")
			if got, want := after.Content[0], healthy.Content[0]; got != want {
				t.Fatalf("pane-2 content after %s = %q, want %q", tt.name, got, want)
			}
			if got, want := after.Cursor.Col, healthy.Cursor.Col; got != want {
				t.Fatalf("pane-2 cursor col after %s = %d, want %d", tt.name, got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Keybinding tests — AmuxHarness (client for prefix key processing)
// ---------------------------------------------------------------------------

func TestZoomKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	// Focus pane-1 via Ctrl-a k
	gen := h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	// Zoom via Ctrl-a z
	gen = h.generation()
	h.sendKeys("C-a", "z")
	h.waitLayout(gen)

	h.assertScreen("Ctrl-a z should zoom the active pane", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	// Unzoom via Ctrl-a z
	gen = h.generation()
	h.sendKeys("C-a", "z")
	h.waitLayout(gen)

	h.assertScreen("Ctrl-a z should toggle unzoom", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})
}

func TestZoomAutoUnzoomOnSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.runCmd("zoom", "pane-1")
	h.assertScreen("pane-1 zoomed before split", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	// Split while zoomed should auto-unzoom
	gen := h.generation()
	h.sendKeys("C-a", "-")
	h.waitLayout(gen)

	h.assertScreen("split while zoomed should auto-unzoom and show all panes", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]") &&
			strings.Contains(s, "[pane-3]")
	})
}

func TestZoomAutoUnzoomOnFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	// Zoom via Ctrl-a z (active pane is pane-2 after split)
	gen := h.generation()
	h.sendKeys("C-a", "z")
	h.waitLayout(gen)

	h.assertScreen("pane-2 zoomed before focus change", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	// Focus previous pane should auto-unzoom
	gen = h.generation()
	h.sendKeys("C-a", "k")
	h.waitLayout(gen)

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 3*time.Second) {
		screen := h.capture()
		t.Fatalf("focus while zoomed should auto-unzoom\nScreen:\n%s", screen)
	}
}
