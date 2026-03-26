package mux

import (
	"reflect"
	"sort"
	"testing"
)

func TestSpiralExpectedColumnHeights(t *testing.T) {
	t.Parallel()

	tests := []struct {
		count int
		want  []int
	}{
		{count: 1, want: []int{1}},
		{count: 2, want: []int{1, 1}},
		{count: 3, want: []int{1, 2}},
		{count: 4, want: []int{2, 2}},
		{count: 5, want: []int{1, 2, 2}},
		{count: 7, want: []int{3, 2, 2}},
		{count: 8, want: []int{3, 3, 2}},
		{count: 9, want: []int{3, 3, 3}},
		{count: 10, want: []int{3, 3, 3, 1}},
		{count: 13, want: []int{3, 3, 3, 4}},
		{count: 15, want: []int{3, 4, 4, 4}},
		{count: 16, want: []int{4, 4, 4, 4}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run("count", func(t *testing.T) {
			t.Parallel()
			if got := spiralExpectedColumnHeights(tt.count); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("spiralExpectedColumnHeights(%d) = %v, want %v", tt.count, got, tt.want)
			}
		})
	}
}

func TestPlanSpiralAddRejectsNonCanonicalLayout(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, fakePaneID(3)); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}

	if _, err := w.PlanSpiralAdd(); err == nil {
		t.Fatal("PlanSpiralAdd should reject a non-canonical 3-pane layout")
	}
}

func TestPlanAndApplySpiralAddBuildsWholeWindowGrid(t *testing.T) {
	t.Parallel()

	w := NewWindow(fakePaneID(1), 120, 40)
	w.FocusPane(w.ActivePane)

	for id := uint32(2); id <= 16; id++ {
		plan, err := w.PlanSpiralAdd()
		if err != nil {
			t.Fatalf("PlanSpiralAdd(%d): %v", id, err)
		}
		if _, err := w.ApplySpiralAddPlan(plan, fakePaneID(id), SplitOptions{KeepFocus: true}); err != nil {
			t.Fatalf("ApplySpiralAddPlan(%d): %v", id, err)
		}
	}

	assertPaneMatrix(t, w, [][]uint32{
		{7, 8, 9, 10},
		{6, 1, 2, 11},
		{5, 4, 3, 12},
		{16, 15, 14, 13},
	})
	if w.ActivePane.ID != 1 {
		t.Fatalf("active pane = %d, want 1", w.ActivePane.ID)
	}
}

func TestPlanAndApplySpiralAddBuildsLeadRightSubtreeGrid(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 140, 40)
	w.FocusPane(p1)
	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	w.FocusPane(p2)

	for id := uint32(3); id <= 10; id++ {
		plan, err := w.PlanSpiralAdd()
		if err != nil {
			t.Fatalf("PlanSpiralAdd(%d): %v", id, err)
		}
		if !plan.LeadMode {
			t.Fatalf("plan for pane-%d should be in lead mode", id)
		}
		if _, err := w.ApplySpiralAddPlan(plan, fakePaneID(id), SplitOptions{KeepFocus: true}); err != nil {
			t.Fatalf("ApplySpiralAddPlan(%d): %v", id, err)
		}
	}

	lead := w.Root.FindPane(1)
	if lead == nil {
		t.Fatal("lead pane not found")
	}
	if lead.H != w.Height {
		t.Fatalf("lead pane height = %d, want %d", lead.H, w.Height)
	}

	assertPaneMatrix(t, w, [][]uint32{
		{8, 9, 10},
		{7, 2, 3},
		{6, 5, 4},
	})
	if w.ActivePane.ID != 2 {
		t.Fatalf("active pane = %d, want 2", w.ActivePane.ID)
	}
}

func assertPaneMatrix(t *testing.T, w *Window, want [][]uint32) {
	t.Helper()

	xs := map[int]struct{}{}
	ys := map[int]struct{}{}

	for _, row := range want {
		for _, paneID := range row {
			cell := w.Root.FindPane(paneID)
			if cell == nil {
				t.Fatalf("pane-%d not found", paneID)
			}
			xs[cell.X] = struct{}{}
			ys[cell.Y] = struct{}{}
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
		for colIdx, paneID := range row {
			cell := w.Root.FindPane(paneID)
			if cell.X != xVals[colIdx] || cell.Y != yVals[rowIdx] {
				t.Fatalf(
					"pane-%d at (%d,%d), want grid cell (%d,%d) => (%d,%d)",
					paneID,
					cell.X, cell.Y,
					rowIdx, colIdx,
					xVals[colIdx], yVals[rowIdx],
				)
			}
		}
	}
}
