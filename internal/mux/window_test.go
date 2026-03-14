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
