package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHotReloadKeybinding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "R", "E", "L", "O", "A", "D", "M", "E", "Enter")
	h.waitFor("RELOADME", 3*time.Second)

	h.sendKeys("C-a", "r")

	if !h.waitFor("[pane-", 8*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after Ctrl-a r\nScreen:\n%s", screen)
	}
	time.Sleep(300 * time.Millisecond)

	h.sendKeys("Enter")
	time.Sleep(500 * time.Millisecond)

	h.assertScreen("Ctrl-a r should be consumed, not forwarded (no 'not found' error)", func(s string) bool {
		return !strings.Contains(s, "not found")
	})

	h.assertScreen("RELOADME visible after hot reload", func(s string) bool {
		return strings.Contains(s, "RELOADME")
	})
}

func TestHotReloadAutoDetect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "A", "U", "T", "O", "R", "L", "D", "Enter")
	h.waitFor("AUTORLD", 3*time.Second)

	out, err := exec.Command("go", "build", "-o", amuxBin, "..").CombinedOutput()
	if err != nil {
		t.Fatalf("rebuilding amux binary: %v\n%s", err, out)
	}

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	h.assertScreen("AUTORLD visible after auto-reload", func(s string) bool {
		return strings.Contains(s, "AUTORLD") && strings.Contains(s, "[pane-")
	})
}

func TestServerHotReload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "B", "E", "F", "O", "R", "E", "R", "L", "D", "Enter")
	h.waitFor("BEFORERLD", 3*time.Second)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload-server\nScreen:\n%s", screen)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	h.sendKeys("e", "c", "h", "o", " ", "A", "F", "T", "E", "R", "R", "L", "D", "Enter")
	if !h.waitFor("AFTERRLD", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("PTY should work after reload\nScreen:\n%s", screen)
	}

	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-1") || !strings.Contains(listOut, "pane-2") {
		t.Errorf("list should show both panes after reload, got:\n%s", listOut)
	}
}

func TestServerAutoReload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("e", "c", "h", "o", " ", "S", "R", "V", "A", "U", "T", "O", "Enter")
	h.waitFor("SRVAUTO", 3*time.Second)

	out, err := exec.Command("go", "build", "-o", amuxBin, "..").CombinedOutput()
	if err != nil {
		t.Fatalf("rebuilding amux binary: %v\n%s", err, out)
	}

	if !h.waitFor("[pane-", 15*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	if !h.waitFor("SRVAUTO", 5*time.Second) {
		screen := h.capture()
		t.Fatalf("SRVAUTO should be visible after server auto-reload\nScreen:\n%s", screen)
	}
}

func TestServerReloadWithMinimizedPane(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "-")
	h.waitFor("[pane-2]", 3*time.Second)

	h.runCmd("minimize", "pane-1")
	time.Sleep(500 * time.Millisecond)

	statusBefore := h.runCmd("status")
	if !strings.Contains(statusBefore, "1 minimized") {
		t.Fatalf("expected 1 minimized pane before reload, got:\n%s", statusBefore)
	}

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	statusAfter := h.runCmd("status")
	if !strings.Contains(statusAfter, "1 minimized") {
		t.Errorf("minimized state should be preserved after reload, got:\n%s", statusAfter)
	}
}

func TestServerReloadBorderColors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.sendKeys("C-a", "\\")
	h.waitFor("[pane-2]", 3*time.Second)

	h.sendKeys("C-a", "h")
	time.Sleep(500 * time.Millisecond)

	ansiBefore := h.captureANSI()
	colorsBefore := extractBorderColors(pickContentLine(ansiBefore))

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.capture()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.capture()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}
	time.Sleep(500 * time.Millisecond)

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
	h := newHarness(t)

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
		screen := h.capture()
		t.Fatalf("TUI script did not start\nScreen:\n%s", screen)
	}

	h.runCmd("reload-server")

	if !h.waitFor("TUIMARK_OK", 15*time.Second) {
		screen := h.capture()
		t.Fatalf("TUI marker should be visible after reload (SIGWINCH redraw)\nScreen:\n%s", screen)
	}
}
