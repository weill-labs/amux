package test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCapturePreservesTruncatedStatusPaddingBeforeBorder(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := strings.TrimSpace(h.runCmd(
		"meta", "set", "pane-1",
		"task=Eliminate double CloneStyledLines in renderer and compositor",
	)); out != "" {
		t.Fatalf("meta set output = %q, want empty", out)
	}
	h.splitV()

	lines := h.captureLines()
	if len(lines) == 0 {
		t.Fatal("capture returned no rows")
	}

	borderCol := h.captureVerticalBorderCol()
	if borderCol <= 0 {
		t.Fatalf("capture did not expose a vertical border:\n%s", h.capture())
	}

	row := []rune(lines[0])
	if len(row) <= borderCol {
		t.Fatalf("status row too short for border column %d: %q", borderCol, lines[0])
	}
	leftPane := string(row[:borderCol])
	trimmed := strings.TrimRight(leftPane, " ")
	if !strings.HasSuffix(trimmed, "…") {
		t.Fatalf("left pane status row %q should end with an ellipsis before padding", leftPane)
	}
	for _, r := range []rune(leftPane[len(trimmed):]) {
		if r != ' ' {
			t.Fatalf("left pane status row %q should pad with spaces after the ellipsis", leftPane)
		}
	}
	if got := row[borderCol]; got != '│' {
		t.Fatalf("border column = %q, want vertical border", string(got))
	}
}

func TestCaptureClearsVacatedCellsAfterShorterRedraw(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	scriptPath := filepath.Join(t.TempDir(), "rewrite-shorter-line.sh")
	mustWriteFile(t, scriptPath, []byte(`#!/bin/sh
set -eu
printf '\033[2J\033[Hbranch history still stays explicit and the worktree is actually'
sleep 0.1
printf '\033[Hbranch h'
sleep 0.1
printf '\033[K\nDONE\n'
`), 0o755)

	h.runShellCommandWithSettle("pane-1", "sh "+scriptPath, "DONE", "200ms")

	serverANSI := h.runCmd("capture", "--ansi", "pane-1")
	if !strings.Contains(serverANSI, "branch h") {
		t.Fatalf("capture --ansi pane-1 should include the rewritten short line, got:\n%s", serverANSI)
	}
	if strings.Contains(serverANSI, "still stays explicit") {
		t.Fatalf("capture --ansi pane-1 should not retain stale content, got:\n%s", serverANSI)
	}

	screen := h.capture()
	if strings.Contains(screen, "still stays explicit") {
		t.Fatalf("capture should not retain stale content after shorter redraw, got:\n%s", screen)
	}

	lines := h.captureLines()
	if len(lines) < 2 {
		t.Fatalf("capture returned fewer than 2 rows:\n%s", screen)
	}
	if got := strings.TrimRight(lines[1], " "); got != "branch h" {
		t.Fatalf("first content row = %q, want %q\nscreen:\n%s", got, "branch h", screen)
	}
}
