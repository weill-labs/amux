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

// ---------------------------------------------------------------------------
// Directional Focus (LAB-147): tmux-style adjacency + overlap + wrapping
// ---------------------------------------------------------------------------

func TestFocusUpAdjacent(t *testing.T) {
	t.Parallel()
	// Two panes stacked vertically, 1-cell border between them.
	//   pane 1: (0,0)  40x12
	//   border: y=12
	//   pane 2: (0,13) 40x12  <- active
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("up")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(up) = pane %d, want pane 1", w.ActivePane.ID)
	}
}

func TestFocusDownAdjacent(t *testing.T) {
	t.Parallel()
	// Two panes stacked vertically. Active is top — down goes to bottom.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("down")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(down) = pane %d, want pane 2", w.ActivePane.ID)
	}
}

func TestFocusLeftAdjacent(t *testing.T) {
	t.Parallel()
	// Two panes side by side, active is right.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79
	w.Height = 24

	w.Focus("left")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(left) = pane %d, want pane 1", w.ActivePane.ID)
	}
}

func TestFocusRightAdjacent(t *testing.T) {
	t.Parallel()
	// Two panes side by side, active is left.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79
	w.Height = 24

	w.Focus("right")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(right) = pane %d, want pane 2", w.ActivePane.ID)
	}
}

func TestFocusUpWraps(t *testing.T) {
	t.Parallel()
	// Active pane is at top — up should wrap to bottom.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) from top = pane %d, want pane 2 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusDownWraps(t *testing.T) {
	t.Parallel()
	// Active pane is at bottom — down should wrap to top.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("down")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(down) from bottom = pane %d, want pane 1 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusLeftWraps(t *testing.T) {
	t.Parallel()
	// Active is leftmost — left should wrap to rightmost.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79
	w.Height = 24

	w.Focus("left")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(left) from leftmost = pane %d, want pane 2 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusRightWraps(t *testing.T) {
	t.Parallel()
	// Active is rightmost — right should wrap to leftmost.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79
	w.Height = 24

	w.Focus("right")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(right) from rightmost = pane %d, want pane 1 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusRecencyTiebreaker(t *testing.T) {
	t.Parallel()
	// Three panes in a row. Two are adjacent above the active pane,
	// both with X overlap. The one with higher ActivePoint wins.
	//
	//   pane 1: (0,0)   20x10
	//   pane 2: (21,0)  19x10   ← higher ActivePoint
	//   border: y=10
	//   pane 3: (0,11)  40x10   ← active
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p1.ActivePoint = 5
	p2.ActivePoint = 10

	leaves := []*LayoutCell{
		NewLeaf(p1, 0, 0, 20, 10),
		NewLeaf(p2, 21, 0, 19, 10),
		NewLeaf(p3, 0, 11, 40, 10),
	}
	root := &LayoutCell{
		X: 0, Y: 0, W: 40, H: 21,
		Dir:      SplitVertical,
		Children: leaves,
	}
	for _, l := range leaves {
		l.Parent = root
	}
	w := &Window{Root: root, ActivePane: p3, Width: 40, Height: 21}

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) recency = pane %d, want pane 2 (higher ActivePoint)", w.ActivePane.ID)
	}
}

func TestFocusActivePointIncremented(t *testing.T) {
	t.Parallel()
	// Verify that Focus() increments ActivePoint on the new active pane.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("down")

	if w.ActivePane.ID != 2 {
		t.Fatalf("Focus(down) = pane %d, want pane 2", w.ActivePane.ID)
	}
	if w.ActivePane.ActivePoint == 0 {
		t.Error("ActivePoint not incremented after Focus()")
	}
}

func TestFocusNoOverlapNoOp(t *testing.T) {
	t.Parallel()
	// Two panes that are NOT adjacent (gap between them) and have no
	// perpendicular overlap. Focus should be a no-op.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 50, 0, 30, 10},
		{2, 0, 20, 40, 10},
	})
	w.Width = 80
	w.Height = 30

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) with no adjacent pane = pane %d, want pane 2 (no-op)", w.ActivePane.ID)
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
