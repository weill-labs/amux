package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHotReloadKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo RELOADME", "Enter")
	if !h.waitFor("RELOADME", 3*time.Second) {
		t.Fatalf("RELOADME not visible\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "r")

	if !h.waitFor("[pane-", 8*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after Ctrl-a r\nScreen:\n%s", screen)
	}
	time.Sleep(400 * time.Millisecond)

	h.sendKeys("Enter")
	time.Sleep(400 * time.Millisecond)

	screen := h.captureOuter()
	if strings.Contains(screen, "not found") {
		t.Errorf("Ctrl-a r should be consumed, not forwarded (no 'not found' error)\nScreen:\n%s", screen)
	}

	if !strings.Contains(screen, "RELOADME") {
		t.Errorf("RELOADME should be visible after hot reload\nScreen:\n%s", screen)
	}
}

func TestHotReloadAutoDetect(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo AUTORLD", "Enter")
	if !h.waitFor("AUTORLD", 3*time.Second) {
		t.Fatalf("AUTORLD not visible\nScreen:\n%s", h.captureOuter())
	}

	if err := buildAmux(amuxBin); err != nil {
		t.Fatalf("rebuilding amux binary: %v", err)
	}

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	screen := h.captureOuter()
	if !strings.Contains(screen, "AUTORLD") || !strings.Contains(screen, "[pane-") {
		t.Errorf("AUTORLD should be visible after auto-reload\nScreen:\n%s", screen)
	}
}

func TestServerHotReload(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo BEFORERLD", "Enter")
	h.waitFor("BEFORERLD", 3*time.Second)

	h.splitV()

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after reload-server\nScreen:\n%s", screen)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	h.sendKeys("echo AFTERRLD", "Enter")
	if !h.waitFor("AFTERRLD", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("PTY should work after reload\nScreen:\n%s", screen)
	}

	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-1") || !strings.Contains(listOut, "pane-2") {
		t.Errorf("list should show both panes after reload, got:\n%s", listOut)
	}
}

func TestServerAutoReload(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo SRVAUTO", "Enter")
	if !h.waitFor("SRVAUTO", 3*time.Second) {
		t.Fatalf("SRVAUTO not visible\nScreen:\n%s", h.captureOuter())
	}

	if err := buildAmux(amuxBin); err != nil {
		t.Fatalf("rebuilding amux binary: %v", err)
	}

	if !h.waitFor("[pane-", 15*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	if !h.waitFor("SRVAUTO", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("SRVAUTO should be visible after server auto-reload\nScreen:\n%s", screen)
	}
}

func TestServerReloadWithMinimizedPane(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.runCmd("minimize", "pane-1")

	statusBefore := h.runCmd("status")
	if !strings.Contains(statusBefore, "1 minimized") {
		t.Fatalf("expected 1 minimized pane before reload, got:\n%s", statusBefore)
	}

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	statusAfter := h.runCmd("status")
	if !strings.Contains(statusAfter, "1 minimized") {
		t.Errorf("minimized state should be preserved after reload, got:\n%s", statusAfter)
	}
}

func TestServerReloadMinimizedPanePreservesContent(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	// Put content in pane-1
	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)
	h.sendKeys("echo RELOAD_MARKER", "Enter")
	h.waitFor("RELOAD_MARKER", 3*time.Second)

	// Minimize pane-1, then reload server
	h.runCmd("minimize", "pane-1")
	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	// Wait for the SIGWINCH force-redraw loop to complete (fires 500ms
	// after reload, with a 200ms gap between passes).
	time.Sleep(1500 * time.Millisecond)

	// Check content BEFORE restore — the minimized pane's emulator
	// should not have been garbled by the SIGWINCH loop.
	paneBeforeRestore := h.runCmd("capture", "pane-1")
	if !strings.Contains(paneBeforeRestore, "RELOAD_MARKER") {
		t.Fatalf("minimized pane emulator should not be garbled by SIGWINCH loop after reload, got:\n%s", paneBeforeRestore)
	}

	// Restore pane-1 and verify content survived
	h.runCmd("restore", "pane-1")

	if !h.waitFor("RELOAD_MARKER", 5*time.Second) {
		paneOut := h.runCmd("capture", "pane-1")
		t.Fatalf("minimized pane content should survive server reload, got:\n%s", paneOut)
	}
}

func TestServerReloadBorderColors(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	ansiBefore := h.captureANSI()
	colorsBefore := extractBorderColors(pickContentLine(ansiBefore))

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	ansiAfter := h.captureANSI()
	colorsAfter := extractBorderColors(pickContentLine(ansiAfter))

	if len(colorsBefore) == 0 {
		t.Fatalf("no border colors found before reload\nScreen:\n%s", ansiBefore)
	}
	if len(colorsAfter) == 0 {
		t.Fatalf("no border colors found after reload\nScreen:\n%s", ansiAfter)
	}

	if colorsBefore[0] != colorsAfter[0] {
		t.Errorf("border color changed after reload:\n  before: %s\n  after:  %s", colorsBefore[0], colorsAfter[0])
	}
}

func TestServerReloadTUIRedraw(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-tui-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
printf '\033[?1049h'
draw() { printf '\033[2J\033[H'; echo TUIMARK_OK; }
trap draw WINCH
draw
while true; do sleep 60; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("TUIMARK_OK", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("TUI script did not start\nScreen:\n%s", screen)
	}

	h.runCmd("reload-server")

	if !h.waitFor("TUIMARK_OK", 15*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("TUI marker should be visible after reload (SIGWINCH redraw)\nScreen:\n%s", screen)
	}
}
