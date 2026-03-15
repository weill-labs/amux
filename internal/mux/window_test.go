package mux

import "testing"

// buildLayout creates a Window with manually positioned leaf cells for
// testing directional focus. Each pane is placed at explicit (x,y,w,h)
// coordinates — no splits needed.
func buildLayout(active uint32, panes []struct {
	id         uint32
	x, y, w, h int
}) *Window {
	if len(panes) == 0 {
		panic("buildLayout: need at least one pane")
	}

	// Create leaves
	leaves := make([]*LayoutCell, len(panes))
	var activePane *Pane
	for i, p := range panes {
		pane := fakePaneID(p.id)
		leaves[i] = NewLeaf(pane, p.x, p.y, p.w, p.h)
		if p.id == active {
			activePane = pane
		}
	}

	// Wrap all leaves in a single horizontal parent (the exact tree
	// structure doesn't matter — Focus() only uses Walk and cell geometry).
	root := &LayoutCell{
		X: 0, Y: 0, W: 200, H: 200,
		Dir:      SplitHorizontal,
		Children: leaves,
	}
	for _, l := range leaves {
		l.Parent = root
	}

	return &Window{
		Root:       root,
		ActivePane: activePane,
		Width:      200,
		Height:     200,
	}
}

func TestFocusUpWithOverlap(t *testing.T) {
	t.Parallel()
	// Two panes stacked vertically in the same column.
	//   pane 1: (0,0)  40x12
	//   pane 2: (0,13) 40x12  <- active
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})

	w.Focus("up")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(up) = pane %d, want pane 1", w.ActivePane.ID)
	}
}

func TestFocusUpNoOverlap(t *testing.T) {
	t.Parallel()
	// Two panes where the target is above but in a different column
	// with no horizontal overlap.
	//   pane 1: (0,0)   40x12   (columns 0-39)
	//   pane 2: (50,20) 40x12   (columns 50-89) <- active
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 50, 20, 40, 12},
	})

	w.Focus("up")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(up) = pane %d, want pane 1 (fallback)", w.ActivePane.ID)
	}
}

func TestFocusUpPrefersOverlap(t *testing.T) {
	t.Parallel()
	// Three panes:
	//   pane 1: (0,0)   30x10  — above, no X overlap, closer by distance
	//   pane 2: (50,5)  40x10  — above, HAS X overlap with active
	//   pane 3: (50,20) 40x10  — active
	//
	// pane 1 center: (15, 5)   — distance from active center (70,25): dx=55, dy=20 → 3425
	// pane 2 center: (70, 10)  — distance from active center (70,25): dx=0,  dy=15 → 225
	//
	// Both are "above" active. Pane 2 has X overlap and should win via
	// the strict first pass, regardless of distance.
	w := buildLayout(3, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 30, 10},
		{2, 50, 5, 40, 10},
		{3, 50, 20, 40, 10},
	})

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) = pane %d, want pane 2 (overlap preferred)", w.ActivePane.ID)
	}
}

func TestFocusLeftNoOverlap(t *testing.T) {
	t.Parallel()
	// Verify fallback works for the "left" direction too.
	//   pane 1: (0,0)   30x10  (rows 0-9)
	//   pane 2: (50,20) 30x10  (rows 20-29) <- active, no Y overlap
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 30, 10},
		{2, 50, 20, 30, 10},
	})

	w.Focus("left")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(left) = pane %d, want pane 1 (fallback)", w.ActivePane.ID)
	}
}

func TestFocusDownNoOverlap(t *testing.T) {
	t.Parallel()
	// Verify fallback works for the "down" direction.
	//   pane 1: (0,0)   30x10  <- active
	//   pane 2: (50,20) 30x10  — below, no X overlap
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 30, 10},
		{2, 50, 20, 30, 10},
	})

	w.Focus("down")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(down) = pane %d, want pane 2 (fallback)", w.ActivePane.ID)
	}
}

func TestFocusRightNoOverlap(t *testing.T) {
	t.Parallel()
	// Verify fallback works for the "right" direction.
	//   pane 1: (0,0)   30x10  <- active
	//   pane 2: (50,20) 30x10  — right, no Y overlap
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 30, 10},
		{2, 50, 20, 30, 10},
	})

	w.Focus("right")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(right) = pane %d, want pane 2 (fallback)", w.ActivePane.ID)
	}
}

// ---------------------------------------------------------------------------
// Swap and Rotate (LAB-93)
// ---------------------------------------------------------------------------

// collectPaneIDs returns pane IDs in depth-first walk order.
func collectPaneIDs(w *Window) []uint32 {
	var ids []uint32
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			ids = append(ids, c.Pane.ID)
		}
	})
	return ids
}

func TestSwapPanes(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p1.Meta.Name = "alpha"
	p2.Meta.Name = "beta"

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitHorizontal, p2)

	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	if err := w.SwapPanes(1, 2); err != nil {
		t.Fatalf("SwapPanes: %v", err)
	}

	left := root.Children[0]
	right := root.Children[1]

	// Pane pointers should be exchanged
	if left.Pane.ID != 2 || right.Pane.ID != 1 {
		t.Errorf("after swap: left=%d right=%d, want 2,1", left.Pane.ID, right.Pane.ID)
	}

	// Metadata follows the pane (swap-with-meta)
	if left.Pane.Meta.Name != "beta" || right.Pane.Meta.Name != "alpha" {
		t.Errorf("metadata: left=%q right=%q, want beta,alpha",
			left.Pane.Meta.Name, right.Pane.Meta.Name)
	}

	// ActivePane follows the pane object, not the cell position
	if w.ActivePane.ID != 1 {
		t.Errorf("ActivePane.ID = %d, want 1 (follows pane)", w.ActivePane.ID)
	}
}

func TestSwapPanesSelf(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	// Swapping a pane with itself is a no-op
	if err := w.SwapPanes(1, 1); err != nil {
		t.Errorf("SwapPanes(self): unexpected error: %v", err)
	}
}

func TestSwapPanesNotFound(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	if err := w.SwapPanes(1, 99); err == nil {
		t.Error("expected error for non-existent pane")
	}
	if err := w.SwapPanes(99, 1); err == nil {
		t.Error("expected error for non-existent source pane")
	}
}

func TestSwapPaneForward(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	root := NewLeaf(p1, 0, 0, 120, 24)
	root.Split(SplitHorizontal, p2)
	root.Children[1].Split(SplitHorizontal, p3)

	// Active is pane-3 (last in walk order)
	w := &Window{Root: root, ActivePane: p3, Width: 120, Height: 24}

	if err := w.SwapPaneForward(); err != nil {
		t.Fatalf("SwapPaneForward: %v", err)
	}

	// Forward: pane-3 swaps with next (wraps to pane-1)
	// Before: [1, 2, 3], After: [3, 2, 1]
	ids := collectPaneIDs(w)
	if ids[0] != 3 || ids[1] != 2 || ids[2] != 1 {
		t.Errorf("after forward swap: %v, want [3,2,1]", ids)
	}
}

func TestSwapPaneBackward(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	root := NewLeaf(p1, 0, 0, 120, 24)
	root.Split(SplitHorizontal, p2)
	root.Children[1].Split(SplitHorizontal, p3)

	// Active is pane-3 (last in walk order)
	w := &Window{Root: root, ActivePane: p3, Width: 120, Height: 24}

	if err := w.SwapPaneBackward(); err != nil {
		t.Fatalf("SwapPaneBackward: %v", err)
	}

	// Backward: pane-3 swaps with previous (pane-2)
	// Before: [1, 2, 3], After: [1, 3, 2]
	ids := collectPaneIDs(w)
	if ids[0] != 1 || ids[1] != 3 || ids[2] != 2 {
		t.Errorf("after backward swap: %v, want [1,3,2]", ids)
	}
}

func TestRotatePanesForward(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	root := NewLeaf(p1, 0, 0, 120, 24)
	root.Split(SplitHorizontal, p2)
	root.Children[1].Split(SplitHorizontal, p3)

	w := &Window{Root: root, ActivePane: p1, Width: 120, Height: 24}

	w.RotatePanes(true)

	// Forward: each cell gets the pane from the previous cell (last wraps to first)
	// Before: [1, 2, 3], After: [3, 1, 2]
	ids := collectPaneIDs(w)
	if ids[0] != 3 || ids[1] != 1 || ids[2] != 2 {
		t.Errorf("after forward rotate: %v, want [3,1,2]", ids)
	}
}

func TestRotatePanesBackward(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	root := NewLeaf(p1, 0, 0, 120, 24)
	root.Split(SplitHorizontal, p2)
	root.Children[1].Split(SplitHorizontal, p3)

	w := &Window{Root: root, ActivePane: p1, Width: 120, Height: 24}

	w.RotatePanes(false)

	// Backward: each cell gets the pane from the next cell (first wraps to last)
	// Before: [1, 2, 3], After: [2, 3, 1]
	ids := collectPaneIDs(w)
	if ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Errorf("after backward rotate: %v, want [2,3,1]", ids)
	}
}

func TestRotateSinglePane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	// Single pane — should be a no-op
	w.RotatePanes(true)

	ids := collectPaneIDs(w)
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("rotate single pane: %v, want [1]", ids)
	}
}
