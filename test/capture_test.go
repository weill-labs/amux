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

func TestCapture(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo SCREENCAP", "Enter")
	h.waitFor("pane-1", "SCREENCAP")

	out := h.capture()
	if !strings.Contains(out, "SCREENCAP") {
		t.Errorf("amux capture should contain typed text, got:\n%s", out)
	}
	if !strings.Contains(out, "[pane-") {
		t.Errorf("amux capture should contain pane status, got:\n%s", out)
	}
	if !strings.Contains(out, "amux") {
		t.Errorf("amux capture should contain global bar, got:\n%s", out)
	}
}

func TestCapturePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo OUTPUTMARKER", "Enter")
	h.waitFor("pane-1", "OUTPUTMARKER")

	output := h.runCmd("capture", "pane-1")
	if !strings.Contains(output, "OUTPUTMARKER") {
		t.Errorf("amux capture <pane> should contain OUTPUTMARKER, got:\n%s", output)
	}
}

func TestCapturePaneHistory(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-history-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 50); do echo \"HISTORY-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "HISTORY-50")

	plain := h.runCmd("capture", "pane-1")
	if strings.Contains(plain, "HISTORY-01") {
		t.Fatalf("plain pane capture should not include off-screen history, got:\n%s", plain)
	}

	out := h.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(out, "HISTORY-01") || !strings.Contains(out, "HISTORY-50") {
		t.Fatalf("history capture should contain full browsable buffer, got:\n%s", out)
	}
}

func TestCapturePaneHistoryJSON(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-history-json-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 40); do echo \"JSONHIST-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "JSONHIST-40")

	out := h.runCmd("capture", "--history", "--format", "json", "pane-1")
	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(pane.History) == 0 {
		t.Fatalf("history JSON should include retained history, got: %+v", pane)
	}
	if joined := strings.Join(pane.History, "\n"); !strings.Contains(joined, "JSONHIST-01") {
		t.Fatalf("history should include retained off-screen lines, got:\n%s", joined)
	}
	if joined := strings.Join(pane.Content, "\n"); !strings.Contains(joined, "JSONHIST-40") {
		t.Fatalf("content should include visible screen, got:\n%s", joined)
	}
	if strings.Join(pane.Content, "\n") == "" {
		t.Fatal("content should not be empty")
	}
}

func TestCapturePaneHistoryWithoutAttachedClient(t *testing.T) {
	t.Parallel()
	h := newServerHarnessPersistent(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-history-headless-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 35); do echo \"NOCLIENT-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "NOCLIENT-35")

	h.client.close()
	h.client = nil

	if out := h.runCmd("capture", "pane-1"); !strings.Contains(out, "no client attached") {
		t.Fatalf("screen capture without client should still fail, got: %s", out)
	}

	out := h.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(out, "NOCLIENT-01") || !strings.Contains(out, "NOCLIENT-35") {
		t.Fatalf("history capture should work without attached client, got:\n%s", out)
	}
}

func TestCapturePaneHistoryRejectsInvalidFlags(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	if out := h.runCmd("capture", "--history", "--ansi", "pane-1"); !strings.Contains(out, "--history is mutually exclusive with --ansi, --colors, and --display") {
		t.Fatalf("history capture with invalid flags should fail, got:\n%s", out)
	}

	if out := h.runCmd("capture", "--history"); !strings.Contains(out, "--history requires a pane target") {
		t.Fatalf("history capture without pane should fail, got:\n%s", out)
	}

	if out := h.runCmd("capture", "--rewrap", "80", "pane-1"); !strings.Contains(out, "--rewrap requires --history") {
		t.Fatalf("rewrap without history should fail, got:\n%s", out)
	}

	if out := h.runCmd("capture", "--history", "--rewrap", "0", "pane-1"); !strings.Contains(out, "--rewrap requires a positive integer width") {
		t.Fatalf("history capture with invalid rewrap width should fail, got:\n%s", out)
	}
}

func TestCapturePaneHistoryRewrapsNarrowLiveHistoryAndContent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("resize-window", "20", "4")
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-history-rewrap-%s.sh", h.session))
	script := "#!/bin/bash\n" +
		"printf 'history narrow panes should rewrap cleanly for agents to read\\n'\n" +
		"printf 'visible content should also rewrap cleanly for agents to read\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("writing rewrap history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "visible content")

	raw := h.runCmd("capture", "--history", "pane-1")
	if strings.Contains(raw, "history narrow panes should rewrap cleanly") {
		t.Fatalf("raw history should still contain narrow-width breaks, got:\n%s", raw)
	}

	rewrapped := h.runCmd("capture", "--history", "--rewrap", "80", "pane-1")
	if !strings.Contains(rewrapped, "history narrow panes should rewrap cleanly for agents to read") {
		t.Fatalf("rewrapped history should reconstruct the full history line, got:\n%s", rewrapped)
	}
	if !strings.Contains(rewrapped, "visible content should also rewrap cleanly for agents to read") {
		t.Fatalf("rewrapped history should reconstruct visible content too, got:\n%s", rewrapped)
	}
}

func TestCapturePaneHistoryJSONRewrapsCursorAndContent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("resize-window", "20", "4")
	h.sendKeys("pane-1", `printf '12345678901234567890abcdefghij'`, "Enter")
	h.waitFor("pane-1", "abcdefghij")

	out := h.runCmd("capture", "--history", "--rewrap", "80", "--format", "json", "pane-1")
	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if joined := strings.Join(pane.Content, "\n"); !strings.Contains(joined, "12345678901234567890abcdefghij") {
		t.Fatalf("rewrapped JSON content should reconstruct the full visible line, got:\n%s", joined)
	}
	if pane.Cursor.Row != 0 || pane.Cursor.Col != len("12345678901234567890abcdefghij") {
		t.Fatalf("rewrapped JSON cursor = (%d,%d), want (0,%d)", pane.Cursor.Row, pane.Cursor.Col, len("12345678901234567890abcdefghij"))
	}
}

func TestCapturePaneANSI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Write colored text so the pane has ANSI sequences.
	// Split the done-marker across two printf calls so it only appears as a
	// contiguous string in the OUTPUT, not in the echoed command text.
	h.sendKeys("pane-1", `printf '\033[31mRED\033[m\n' && printf COL; printf 'DONE\n'`, "Enter")
	h.waitFor("pane-1", "COLDONE")

	// Per-pane capture without --ansi should be plain text
	plain := h.runCmd("capture", "pane-1")
	if strings.Contains(plain, "\033[") {
		t.Errorf("capture pane without --ansi should be plain text, got ANSI escapes:\n%s", plain)
	}
	if !strings.Contains(plain, "RED") {
		t.Errorf("capture pane should contain RED, got:\n%s", plain)
	}

	// Per-pane capture with --ansi should preserve ANSI sequences
	ansi := h.runCmd("capture", "--ansi", "pane-1")
	if !strings.Contains(ansi, "\033[") {
		t.Errorf("capture pane --ansi should contain ANSI escapes, got:\n%s", ansi)
	}
	if !strings.Contains(ansi, "RED") {
		t.Errorf("capture pane --ansi should contain RED, got:\n%s", ansi)
	}
}

func TestCapturePaneANSI_PreservesStyleAfterSGRReset(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Match the tmux regress cases where foreground and background styles
	// should survive/reset correctly in ANSI capture output.
	h.sendKeys("pane-1",
		`clear; printf '\033[31;42;1mabc\033[0;31mdef\n'; printf '\033[m\033[100m bright bg \033[m\n'; printf STY; printf 'DONE\n'`,
		"Enter")
	h.waitFor("pane-1", "STYDONE")

	ansi := h.runCmd("capture", "--ansi", "pane-1")
	wantStyled := "\033[31;42;1mabc\033[49;22mdef\033[m"
	if !strings.Contains(ansi, wantStyled) {
		t.Fatalf("capture pane --ansi should preserve post-reset style state, want substring %q in:\n%s", wantStyled, ansi)
	}

	wantBrightBG := "\033[100m bright bg \033[m"
	if !strings.Contains(ansi, wantBrightBG) {
		t.Fatalf("capture pane --ansi should preserve bright background reset, want substring %q in:\n%s", wantBrightBG, ansi)
	}
}

func TestCapturePaneANSI_PreservesOSC8Hyperlinks(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	link := "\033]8;https://example.com;\atest-link\033]8;;\a"
	h.sendKeys("pane-1",
		`clear; printf '\033]8;;https://example.com\033\\test-link\033]8;;\033\\\n'; printf OSC; printf 'DONE\n'`,
		"Enter")
	h.waitFor("pane-1", "OSCDONE")

	ansi := h.runCmd("capture", "--ansi", "pane-1")
	if !strings.Contains(ansi, link) {
		t.Fatalf("capture pane --ansi should preserve OSC 8 hyperlink semantics, want substring %q in:\n%s", link, ansi)
	}
}

func TestCursorBlockOnlyInActivePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Split so we have two panes with shell prompts
	h.splitV()

	// Focus pane-2 — pane-1 becomes inactive.
	// Use per-pane --ansi capture (returns emulator Render() output)
	// to check each pane independently, avoiding false positives from
	// the compositor's own ANSI sequences or shell prompt styling.
	h.doFocus("pane-2")

	inactive := h.runCmd("capture", "--ansi", "pane-1")
	if strings.Contains(inactive, "\033[7m") {
		t.Errorf("inactive pane should have no reverse-video cursor blocks, got:\n%s", inactive)
	}
}

func TestCaptureIdleIndicator(t *testing.T) {
	t.Parallel()
	h := newServerHarnessPersistent(t)

	// Split so pane-1 becomes inactive (pane-2 gets focus)
	h.splitV()

	// Wait for the idle timer to fire. The inactive pane's shell is at
	// the prompt with no children, so it transitions to idle and shows ◇.
	h.waitFor("pane-2", "$")
	h.waitIdle("pane-1")

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "◇")
	}, 5*time.Second) {
		t.Fatalf("capture should show idle diamond indicator for inactive idle pane, got:\n%s", h.capture())
	}
}

func TestCaptureWithSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo LEFTPANE", "Enter")
	h.waitFor("pane-1", "LEFTPANE")

	h.splitV()
	h.sendKeys("pane-2", "echo RIGHTPANE", "Enter")
	h.waitFor("pane-2", "RIGHTPANE")

	out := h.capture()
	if !strings.Contains(out, "LEFTPANE") {
		t.Errorf("amux capture should contain left pane text, got:\n%s", out)
	}
	if !strings.Contains(out, "RIGHTPANE") {
		t.Errorf("amux capture should contain right pane text, got:\n%s", out)
	}
	if !strings.Contains(out, "[pane-1]") || !strings.Contains(out, "[pane-2]") {
		t.Errorf("amux capture should contain both pane names, got:\n%s", out)
	}
}
