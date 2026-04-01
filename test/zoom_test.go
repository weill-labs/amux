package test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// The headless control client can become command-ready slightly before
	// short-lived CLI subprocesses are able to connect. Establish CLI command
	// readiness before the first split helper issues `_layout-json`.
	_ = h.generation()
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
				gen := h.generation()
				h.runCmd("zoom", "pane-2")
				h.waitLayout(gen)
			},
		},
		{
			name: "unzoom",
			zoom: func() {
				gen := h.generation()
				h.runCmd("zoom", "pane-2")
				h.waitLayout(gen)
				gen = h.generation()
				h.runCmd("zoom", "pane-2")
				h.waitLayout(gen)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: these subtests share a single harness and mutate its layout.
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
			if got, want := strings.TrimLeft(after.Content[0], " "), strings.TrimLeft(healthy.Content[0], " "); got != want {
				t.Fatalf("pane-2 content after %s = %q, want %q", tt.name, got, want)
			}
			if got, want := after.Cursor.Col, healthy.Cursor.Col; got != want {
				t.Fatalf("pane-2 cursor col after %s = %d, want %d", tt.name, got, want)
			}
		})
	}
}

func TestZoomRedrawsAltScreenPaneAtExpandedSize(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "zoom-redraw.log")
	scriptPath := filepath.Join(tmpDir, "zoom-redraw.sh")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import os
import signal
import sys
import threading
import time

log_file = %q
draw_count = 0

sys.stdout.write('\033[?1049h')
sys.stdout.flush()

def draw(*_args):
    global draw_count
    draw_count += 1
    size = os.get_terminal_size()
    size_marker = f"DRAW {draw_count} SIZE {size.columns}x{size.lines}"
    bottom_marker = f"BOTTOM {size.columns}x{size.lines}"
    with open(log_file, "a", encoding="utf-8") as fh:
        fh.write(size_marker + "\n")
    sys.stdout.write('\033[2J\033[H' + size_marker + '\n')
    sys.stdout.write(f'\033[{size.lines};1H{bottom_marker}')
    sys.stdout.flush()

signal.signal(signal.SIGWINCH, draw)
draw()
threading.Timer(0.2, lambda: os.kill(os.getpid(), signal.SIGWINCH)).start()

while True:
    time.sleep(60)
`, logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write alt-screen zoom script: %v", err)
	}

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitForTimeout("pane-1", "DRAW 2 SIZE ", "10s")

	gen := h.generation()
	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Zoomed") {
		t.Fatalf("zoom should confirm, got:\n%s", output)
	}
	h.waitLayout(gen)

	capture := h.captureJSON()
	pane := h.jsonPane(capture, "pane-1")
	if pane.Position == nil {
		t.Fatal("pane-1 position missing after zoom")
	}
	wantRows := pane.Position.Height - 1
	wantSize := fmt.Sprintf("SIZE %dx%d", pane.Position.Width, wantRows)
	wantBottom := fmt.Sprintf("BOTTOM %dx%d", pane.Position.Width, wantRows)

	if !h.waitForCaptureJSON(func(c proto.CaptureJSON) bool {
		for _, p := range c.Panes {
			if p.Name != "pane-1" {
				continue
			}
			if len(p.Content) < wantRows {
				return false
			}
			return strings.Contains(p.Content[0], wantSize) && p.Content[wantRows-1] == wantBottom
		}
		return false
	}, 5*time.Second) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf(
			"zoomed alt-screen pane did not redraw to expanded size %s / %s\nlog:\n%s\ncapture:\n%s",
			wantSize,
			wantBottom,
			logData,
			h.capture(),
		)
	}
}

func TestZoomFirstResizeSignalRedrawsAtExpandedWidth(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "zoom-first-resize.log")
	scriptPath := filepath.Join(tmpDir, "zoom-first-resize.sh")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import os
import signal
import sys
import time

log_file = %q

sys.stdout.write('\033[?1049h')
sys.stdout.flush()

def record(marker):
    with open(log_file, "a", encoding="utf-8") as fh:
        fh.write(marker + "\n")

def draw(*_args):
    size = os.get_terminal_size()
    size_marker = f"SIZE {size.columns}x{size.lines}"
    bottom_marker = f"BOTTOM {size.columns}x{size.lines}"
    record(size_marker)
    sys.stdout.write('\033[2J\033[H' + size_marker + '\n')
    sys.stdout.write(f'\033[{size.lines};1H{bottom_marker}')
    sys.stdout.flush()

signal.signal(signal.SIGWINCH, draw)
size = os.get_terminal_size()
ready = f"READY {size.columns}x{size.lines}"
record(ready)
sys.stdout.write(ready)
sys.stdout.flush()

while True:
    time.sleep(60)
`, logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write one-shot zoom script: %v", err)
	}

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitForTimeout("pane-1", "READY ", "10s")

	gen := h.generation()
	output := h.runCmd("zoom", "pane-1")
	if !strings.Contains(output, "Zoomed") {
		t.Fatalf("zoom should confirm, got:\n%s", output)
	}
	h.waitLayout(gen)

	capture := h.captureJSON()
	pane := h.jsonPane(capture, "pane-1")
	if pane.Position == nil {
		t.Fatal("pane-1 position missing after zoom")
	}
	wantRows := pane.Position.Height - 1
	wantSize := fmt.Sprintf("SIZE %dx%d", pane.Position.Width, wantRows)
	wantBottom := fmt.Sprintf("BOTTOM %dx%d", pane.Position.Width, wantRows)

	if !h.waitForCaptureJSON(func(c proto.CaptureJSON) bool {
		for _, p := range c.Panes {
			if p.Name != "pane-1" {
				continue
			}
			if len(p.Content) < wantRows {
				return false
			}
			return strings.Contains(p.Content[0], wantSize) && p.Content[wantRows-1] == wantBottom
		}
		return false
	}, 5*time.Second) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf(
			"zoomed pane did not redraw to expanded width on first resize signal %s / %s\nlog:\n%s\ncapture:\n%s",
			wantSize,
			wantBottom,
			logData,
			h.capture(),
		)
	}
}

func TestZoomedPaneReattachUsesZoomWidthOnSecondClient(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	h.splitV()

	gen := h.generation()
	output := h.runCmd("zoom", "pane-2")
	if !strings.Contains(output, "Zoomed") {
		t.Fatalf("zoom should confirm, got:\n%s", output)
	}
	h.waitLayout(gen)

	const wideLine = "LAB352-01234567890123456789012345678901234567890123456789012345"
	h.sendKeys("pane-2", "clear; printf '%s\\n' '"+wideLine+"'", "Enter")
	h.waitIdle("pane-2")
	if !h.waitForCaptureJSON(func(c proto.CaptureJSON) bool {
		for _, p := range c.Panes {
			if p.Name != "pane-2" || len(p.Content) == 0 {
				continue
			}
			return p.Content[0] == wideLine
		}
		return false
	}, 5*time.Second) {
		t.Fatalf("first client never settled on zoomed replay content %q\ncapture:\n%s", wideLine, h.capture())
	}

	healthy := h.captureJSON()
	healthyPane := h.jsonPane(healthy, "pane-2")
	if got := healthyPane.Content[0]; got != wideLine {
		t.Fatalf("first client pane-2 first line = %q, want %q", got, wideLine)
	}
	if healthyPane.Position == nil {
		t.Fatal("first client pane-2 position missing")
	}
	if got := healthyPane.Position.Width; got != 80 {
		t.Fatalf("first client zoomed pane width = %d, want 80", got)
	}

	h.client.close()
	h.client = nil

	client := newPTYClientHarnessWithReadyOutput(t, h, "[pane-2]")
	secondScreen := client.screen(80, 24)
	if !strings.Contains(secondScreen, wideLine) {
		t.Fatalf("second client did not preserve zoom width for replayed content %q\nscreen:\n%s", wideLine, secondScreen)
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

func TestZoomSplitKeepsZoomAndFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.runCmd("zoom", "pane-1")
	h.assertScreen("pane-1 zoomed before split", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})

	// Split while zoomed should keep the active pane zoomed and create the new pane off-screen until unzoom.
	gen := h.generation()
	h.sendKeys("C-a", "-")
	h.waitLayout(gen)

	h.assertScreen("split while zoomed should keep only pane-1 visible", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]") &&
			!strings.Contains(s, "[pane-3]")
	})

	capture := h.captureJSON()
	p1 := h.jsonPane(capture, "pane-1")
	if !p1.Active || !p1.Zoomed {
		t.Fatalf("pane-1 state after split = active:%v zoomed:%v, want true/true", p1.Active, p1.Zoomed)
	}
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-3") {
		t.Fatalf("list should include pane-3 after zoomed split, got:\n%s", listOut)
	}

	h.runCmd("zoom", "pane-1")
	h.assertScreen("unzoom should reveal the split pane", func(s string) bool {
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
