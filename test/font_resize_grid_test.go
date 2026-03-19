package test

import (
	"fmt"
	"strings"
	"testing"
)

func TestFontResize_ThreeByThreeGridReturnsToOriginalLayout(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)

	initial := capturePanePositions(h)

	gen := h.generation()
	h.outer.runCmd("resize-window", "120", "40")
	h.waitLayout(gen)

	larger := capturePanePositions(h)
	assertThreeByThreeGrid(t, larger)

	gen = h.generation()
	h.outer.runCmd("resize-window", "80", "24")
	h.waitLayout(gen)

	final := capturePanePositions(h)
	assertThreeByThreeGrid(t, final)

	if diff := diffPanePositions(initial, final); diff != "" {
		t.Fatalf("3x3 grid drifted after grow/shrink cycle:\n%s", diff)
	}
}

func TestFontResize_UnevenGridReturnsToOriginalLayout(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	makeGridUneven(t, h)

	initial := capturePanePositions(h)

	gen := h.generation()
	h.outer.runCmd("resize-window", "120", "40")
	h.waitLayout(gen)

	gen = h.generation()
	h.outer.runCmd("resize-window", "80", "24")
	h.waitLayout(gen)

	final := capturePanePositions(h)
	if diff := diffPanePositions(initial, final); diff != "" {
		t.Fatalf("uneven grid drifted after grow/shrink cycle:\n%s", diff)
	}
}

func makeThreeByThreeGrid(t *testing.T, h *AmuxHarness) {
	t.Helper()

	runLayoutCommand(t, h, "split", "v", "root")
	runLayoutCommand(t, h, "split", "v", "root")

	for _, pane := range []string{"pane-1", "pane-2", "pane-3"} {
		runLayoutCommand(t, h, "focus", pane)
		runLayoutCommand(t, h, "split")
		runLayoutCommand(t, h, "split")
	}
}

func makeGridUneven(t *testing.T, h *AmuxHarness) {
	t.Helper()

	if out := h.runCmd("resize-pane", "pane-1", "right", "5"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane pane-1 right failed: %s", out)
	}
	if out := h.runCmd("resize-pane", "pane-1", "down", "2"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane pane-1 down failed: %s", out)
	}
	if out := h.runCmd("resize-pane", "pane-9", "left", "3"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane pane-9 left failed: %s", out)
	}
}

func runLayoutCommand(t *testing.T, h *AmuxHarness, args ...string) {
	t.Helper()
	gen := h.generation()
	out := h.runCmd(args...)
	if out != "" && (strings.Contains(out, "error") || strings.Contains(out, "cannot")) {
		t.Fatalf("%v failed: %s", args, out)
	}
	h.waitLayout(gen)
}

type panePos struct {
	x int
	y int
	w int
	h int
}

func capturePanePositions(h *AmuxHarness) map[string]panePos {
	capture := h.captureJSON()
	out := make(map[string]panePos, len(capture.Panes))
	for _, pane := range capture.Panes {
		if pane.Position == nil {
			continue
		}
		out[pane.Name] = panePos{
			x: pane.Position.X,
			y: pane.Position.Y,
			w: pane.Position.Width,
			h: pane.Position.Height,
		}
	}
	return out
}

func assertThreeByThreeGrid(t *testing.T, positions map[string]panePos) {
	t.Helper()
	if len(positions) != 9 {
		t.Fatalf("expected 9 panes, got %d", len(positions))
	}

	xs := map[int]bool{}
	ys := map[int]bool{}
	for _, pos := range positions {
		xs[pos.x] = true
		ys[pos.y] = true
	}
	if len(xs) != 3 || len(ys) != 3 {
		t.Fatalf("expected 3 columns and 3 rows, got %d columns and %d rows", len(xs), len(ys))
	}
}

func diffPanePositions(want, got map[string]panePos) string {
	var out string
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("pane-%d", i)
		if want[name] != got[name] {
			out += fmt.Sprintf("%s: initial=%+v final=%+v\n", name, want[name], got[name])
		}
	}
	return out
}
