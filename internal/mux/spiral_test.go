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

func TestPlanSpiralAddRejectsNonCanonicalColumnHeights(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, fakePaneID(3), SplitOptions{}); err != nil {
		t.Fatalf("SplitPane pane-3: %v", err)
	}

	if _, err := w.PlanSpiralAdd(); err == nil {
		t.Fatal("PlanSpiralAdd should reject reversed 3-pane column heights")
	}
}

func TestSpiralAddCanvasErrors(t *testing.T) {
	t.Parallel()

	t.Run("no layout", func(t *testing.T) {
		t.Parallel()

		w := &Window{}
		if _, err := w.spiralAddCanvas(); err == nil || err.Error() != "no layout" {
			t.Fatalf("spiralAddCanvas() error = %v, want no layout", err)
		}
	})

	t.Run("lead missing right subtree", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		left := NewLeaf(p1, 0, 0, 39, 24)
		w := &Window{
			Root: &LayoutCell{
				X: 0, Y: 0, W: 80, H: 24,
				Dir:      SplitVertical,
				Children: []*LayoutCell{left, nil},
			},
			ActivePane: p1,
			Width:      80,
			Height:     24,
			LeadPaneID: p1.ID,
		}
		left.Parent = w.Root

		if _, err := w.spiralAddCanvas(); err == nil || err.Error() != "lead pane has no right subtree" {
			t.Fatalf("spiralAddCanvas() error = %v, want missing right subtree", err)
		}
	})

	t.Run("no panes in canvas", func(t *testing.T) {
		t.Parallel()

		w := &Window{Root: &LayoutCell{}}
		if _, err := w.spiralAddCanvas(); err == nil || err.Error() != "no panes in spiral canvas" {
			t.Fatalf("spiralAddCanvas() error = %v, want empty canvas", err)
		}
	})
}

func TestSpiralColumnsRejectInvalidShapes(t *testing.T) {
	t.Parallel()

	if _, err := spiralColumns(nil); err == nil || err.Error() != "spiral layout requires a canvas" {
		t.Fatalf("spiralColumns(nil) error = %v, want canvas error", err)
	}

	horizontalRoot := &LayoutCell{
		Dir: SplitHorizontal,
		Children: []*LayoutCell{
			NewLeaf(fakePaneID(1), 0, 0, 20, 11),
			NewLeaf(fakePaneID(2), 0, 12, 20, 11),
		},
	}
	if _, err := spiralColumns(horizontalRoot); err == nil {
		t.Fatal("spiralColumns should reject a horizontal root")
	}

	verticalChildRoot := &LayoutCell{
		Dir: SplitVertical,
		Children: []*LayoutCell{
			{
				Dir: SplitVertical,
				Children: []*LayoutCell{
					NewLeaf(fakePaneID(1), 0, 0, 9, 23),
					NewLeaf(fakePaneID(2), 10, 0, 9, 23),
				},
			},
			NewLeaf(fakePaneID(3), 20, 0, 20, 23),
		},
	}
	if _, err := spiralColumns(verticalChildRoot); err == nil {
		t.Fatal("spiralColumns should reject a non-horizontal column subtree")
	}
}

func TestSpiralColumnLeavesRejectInvalidCells(t *testing.T) {
	t.Parallel()

	if _, ok := spiralColumnLeaves(nil); ok {
		t.Fatal("spiralColumnLeaves(nil) should fail")
	}

	if _, ok := spiralColumnLeaves(&LayoutCell{}); ok {
		t.Fatal("spiralColumnLeaves should reject a leaf with no pane")
	}

	vertical := &LayoutCell{
		Dir: SplitVertical,
		Children: []*LayoutCell{
			NewLeaf(fakePaneID(1), 0, 0, 20, 11),
			NewLeaf(fakePaneID(2), 21, 0, 20, 11),
		},
	}
	if _, ok := spiralColumnLeaves(vertical); ok {
		t.Fatal("spiralColumnLeaves should reject vertical subtrees")
	}
}

func TestSpiralMathEdgeCases(t *testing.T) {
	t.Parallel()

	if got := spiralExpectedColumnHeights(0); got != nil {
		t.Fatalf("spiralExpectedColumnHeights(0) = %v, want nil", got)
	}
	if got := spiralCeilSqrt(0); got != 0 {
		t.Fatalf("spiralCeilSqrt(0) = %d, want 0", got)
	}
	if spiralIsPerfectSquare(-1) {
		t.Fatal("spiralIsPerfectSquare(-1) should be false")
	}
}

func TestApplySpiralAddPlanErrors(t *testing.T) {
	t.Parallel()

	t.Run("split target missing", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.ApplySpiralAddPlan(SpiralAddPlan{SplitTargetPaneID: 99}, fakePaneID(2), SplitOptions{}); err == nil || err.Error() != "pane 99 not found in layout" {
			t.Fatalf("ApplySpiralAddPlan missing target error = %v", err)
		}
	})

	t.Run("swap fails across lead column", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		p2 := fakePaneID(2)
		w := NewWindow(p1, 120, 40)
		if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
			t.Fatalf("SplitRoot pane-2: %v", err)
		}
		if err := w.SetLead(p1.ID); err != nil {
			t.Fatalf("SetLead: %v", err)
		}

		_, err := w.ApplySpiralAddPlan(
			SpiralAddPlan{SplitTargetPaneID: p2.ID, SwapWithTarget: true},
			fakePaneID(p1.ID),
			SplitOptions{KeepFocus: true},
		)
		if err == nil || err.Error() != "cannot swap lead pane across columns" {
			t.Fatalf("ApplySpiralAddPlan swap error = %v, want lead swap error", err)
		}
	})
}

func TestSplitSubtreeRootWithOptions(t *testing.T) {
	t.Parallel()

	t.Run("rejects nil root", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.splitSubtreeRootWithOptions(nil, SplitVertical, fakePaneID(2), false, SplitOptions{}); err == nil || err.Error() != "no layout" {
			t.Fatalf("splitSubtreeRootWithOptions(nil) error = %v, want no layout", err)
		}
	})

	t.Run("wraps vertical leaf and focuses new pane", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		p2 := fakePaneID(2)
		w := NewWindow(p1, 80, 24)
		w.ZoomedPaneID = p1.ID

		if _, err := w.splitSubtreeRootWithOptions(w.Root, SplitVertical, p2, true, SplitOptions{}); err != nil {
			t.Fatalf("splitSubtreeRootWithOptions vertical: %v", err)
		}
		if w.Root.Dir != SplitVertical {
			t.Fatalf("root dir = %v, want vertical", w.Root.Dir)
		}
		if got := w.Root.Children[0].Pane.ID; got != p2.ID {
			t.Fatalf("left child pane = %d, want %d", got, p2.ID)
		}
		if w.ActivePane.ID != p2.ID {
			t.Fatalf("active pane = %d, want %d", w.ActivePane.ID, p2.ID)
		}
		if w.ZoomedPaneID != 0 {
			t.Fatalf("zoomed pane = %d, want 0", w.ZoomedPaneID)
		}
	})

	t.Run("wraps horizontal leaf without taking focus", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		p2 := fakePaneID(2)
		w := NewWindow(p1, 80, 24)

		if _, err := w.splitSubtreeRootWithOptions(w.Root, SplitHorizontal, p2, false, SplitOptions{KeepFocus: true}); err != nil {
			t.Fatalf("splitSubtreeRootWithOptions horizontal: %v", err)
		}
		if w.Root.Dir != SplitHorizontal {
			t.Fatalf("root dir = %v, want horizontal", w.Root.Dir)
		}
		if got := w.Root.Children[1].Pane.ID; got != p2.ID {
			t.Fatalf("bottom child pane = %d, want %d", got, p2.ID)
		}
		if w.ActivePane.ID != p1.ID {
			t.Fatalf("active pane = %d, want %d", w.ActivePane.ID, p1.ID)
		}
	})
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
