package test

import (
	"sort"
	"testing"
)

func TestSpawnSpiralBuildsWholeWindowSpiralGrid(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.unsetLead()
	h.runCmd("focus", "pane-1")

	for i := 0; i < 15; i++ {
		h.runCmd("spawn", "--spiral")
	}

	capture := h.captureJSON()
	assertCaptureMatrix(t, h, [][]string{
		{"pane-7", "pane-8", "pane-9", "pane-10"},
		{"pane-6", "pane-1", "pane-2", "pane-11"},
		{"pane-5", "pane-4", "pane-3", "pane-12"},
		{"pane-16", "pane-15", "pane-14", "pane-13"},
	})

	if !h.jsonPane(capture, "pane-1").Active {
		t.Fatal("pane-1 should remain active after repeated spawn --spiral")
	}
}

func TestSpawnSpiralBuildsRightSubtreeWhenLeadIsSet(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.splitV()
	h.runCmd("set-lead", "pane-1")
	h.runCmd("focus", "pane-2")

	for i := 0; i < 8; i++ {
		h.runCmd("spawn", "--spiral")
	}

	capture := h.captureJSON()
	lead := h.jsonPane(capture, "pane-1")
	if lead.Position == nil {
		t.Fatal("lead pane missing position")
	}
	for _, name := range []string{"pane-8", "pane-9", "pane-10", "pane-7", "pane-2", "pane-3", "pane-6", "pane-5", "pane-4"} {
		pane := h.jsonPane(capture, name)
		if pane.Position == nil {
			t.Fatalf("%s missing position", name)
		}
		if lead.Position.X >= pane.Position.X {
			t.Fatalf("lead pane should stay left of %s: lead x=%d pane x=%d", name, lead.Position.X, pane.Position.X)
		}
		if lead.Position.Height <= pane.Position.Height {
			t.Fatalf("lead pane should remain full-height relative to %s: lead h=%d pane h=%d", name, lead.Position.Height, pane.Position.Height)
		}
	}

	assertCaptureMatrix(t, h, [][]string{
		{"pane-8", "pane-9", "pane-10"},
		{"pane-7", "pane-2", "pane-3"},
		{"pane-6", "pane-5", "pane-4"},
	})

	if !h.jsonPane(capture, "pane-2").Active {
		t.Fatal("pane-2 should remain active after lead-mode spawn --spiral")
	}
}

func assertCaptureMatrix(t *testing.T, h *ServerHarness, want [][]string) {
	t.Helper()

	json := h.captureJSON()
	xs := map[int]struct{}{}
	ys := map[int]struct{}{}

	for _, row := range want {
		for _, name := range row {
			pane := h.jsonPane(json, name)
			if pane.Position == nil {
				t.Fatalf("%s missing position", name)
			}
			xs[pane.Position.X] = struct{}{}
			ys[pane.Position.Y] = struct{}{}
		}
	}

	xVals := make([]int, 0, len(xs))
	for x := range xs {
		xVals = append(xVals, x)
	}
	yVals := make([]int, 0, len(ys))
	for y := range ys {
		yVals = append(yVals, y)
	}
	sort.Ints(xVals)
	sort.Ints(yVals)

	if len(xVals) != len(want[0]) {
		t.Fatalf("unique x columns = %d, want %d", len(xVals), len(want[0]))
	}
	if len(yVals) != len(want) {
		t.Fatalf("unique y rows = %d, want %d", len(yVals), len(want))
	}

	for rowIdx, row := range want {
		for colIdx, name := range row {
			pane := h.jsonPane(json, name)
			if pane.Position.X != xVals[colIdx] || pane.Position.Y != yVals[rowIdx] {
				t.Fatalf(
					"%s at (%d,%d), want (%d,%d)",
					name,
					pane.Position.X, pane.Position.Y,
					xVals[colIdx], yVals[rowIdx],
				)
			}
		}
	}
}
