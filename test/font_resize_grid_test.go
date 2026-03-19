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
	resizeRoundTrip(t, h)

	larger := capturePanePositions(h)
	assertThreeByThreeGrid(t, larger)

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
	resizeRoundTrip(t, h)

	final := capturePanePositions(h)
	if diff := diffPanePositions(initial, final); diff != "" {
		t.Fatalf("uneven grid drifted after grow/shrink cycle:\n%s", diff)
	}
}

func TestFontResize_ZoomedGridReturnsToOriginalLayout(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	makeGridUneven(t, h)

	initial := capturePanePositions(h)

	runLayoutCommand(t, h, "zoom", "pane-5")
	resizeRoundTrip(t, h)
	runLayoutCommand(t, h, "zoom", "pane-5")

	final := capturePanePositions(h)
	if diff := diffPanePositions(initial, final); diff != "" {
		t.Fatalf("zoomed grid drifted after grow/shrink cycle:\n%s", diff)
	}
}

func TestFontResize_MinimizeRestoreGridReturnsToOriginalLayout(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	makeGridUneven(t, h)

	initial := capturePanePositions(h)

	runLayoutCommand(t, h, "minimize", "pane-1")
	resizeRoundTrip(t, h)
	runLayoutCommand(t, h, "restore", "pane-1")

	final := capturePanePositions(h)
	if diff := diffPanePositions(initial, final); diff != "" {
		t.Fatalf("minimize/restore grid drifted after grow/shrink cycle:\n%s", diff)
	}
}

func TestFontResize_RepeatedManualResizesReturnToBaseline(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	makeThreeByThreeGrid(t, h)
	for _, tc := range []struct {
		name  string
		apply func(*testing.T, *AmuxHarness)
	}{
		{name: "first", apply: makeGridVeryUneven},
		{name: "second", apply: makeGridUnevenAgain},
	} {
		tc.apply(t, h)
		assertRoundTripPreservesLayout(t, h, tc.name+" repeated-resize baseline")
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
	applyResizeSteps(t, h, []resizeStep{
		{pane: "pane-1", dir: "right", amount: "5"},
		{pane: "pane-1", dir: "down", amount: "2"},
		{pane: "pane-9", dir: "left", amount: "3"},
	})
}

type resizeStep struct {
	pane   string
	dir    string
	amount string
}

func makeGridVeryUneven(t *testing.T, h *AmuxHarness) {
	t.Helper()
	applyResizeSteps(t, h, []resizeStep{
		{pane: "pane-1", dir: "right", amount: "5"},
		{pane: "pane-1", dir: "down", amount: "2"},
		{pane: "pane-9", dir: "left", amount: "3"},
		{pane: "pane-4", dir: "down", amount: "1"},
		{pane: "pane-8", dir: "up", amount: "2"},
	})
}

func makeGridUnevenAgain(t *testing.T, h *AmuxHarness) {
	t.Helper()
	applyResizeSteps(t, h, []resizeStep{
		{pane: "pane-2", dir: "left", amount: "2"},
		{pane: "pane-7", dir: "up", amount: "1"},
		{pane: "pane-6", dir: "right", amount: "1"},
	})
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

func resizeRoundTrip(t *testing.T, h *AmuxHarness) {
	t.Helper()
	gen := h.generation()
	h.outer.runCmd("resize-window", "120", "40")
	h.waitLayout(gen)

	gen = h.generation()
	h.outer.runCmd("resize-window", "80", "24")
	h.waitLayout(gen)
}

func assertRoundTripPreservesLayout(t *testing.T, h *AmuxHarness, name string) {
	t.Helper()
	baseline := capturePanePositions(h)
	resizeRoundTrip(t, h)
	final := capturePanePositions(h)
	if diff := diffPanePositions(baseline, final); diff != "" {
		t.Fatalf("%s drifted after grow/shrink cycle:\n%s", name, diff)
	}
}

func applyResizeSteps(t *testing.T, h *AmuxHarness, steps []resizeStep) {
	t.Helper()
	for _, step := range steps {
		if out := h.runCmd("resize-pane", step.pane, step.dir, step.amount); !strings.Contains(out, "Resized") {
			t.Fatalf("resize-pane %s %s failed: %s", step.pane, step.dir, out)
		}
	}
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
