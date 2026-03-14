package mux

import "testing"

// fakePaneID creates a minimal Pane with just an ID for testing layout.
func fakePaneID(id uint32) *Pane {
	return &Pane{ID: id}
}

func TestNewLeaf(t *testing.T) {
	t.Parallel()
	p := fakePaneID(1)
	leaf := NewLeaf(p, 0, 0, 80, 24)

	if !leaf.IsLeaf() {
		t.Error("expected leaf")
	}
	if leaf.Pane != p {
		t.Error("wrong pane")
	}
	if leaf.W != 80 || leaf.H != 24 {
		t.Errorf("size = %dx%d, want 80x24", leaf.W, leaf.H)
	}
}

func TestSplitHorizontal(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	newCell, err := root.Split(SplitHorizontal, p2)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	// Root should now be internal with 2 children
	if root.IsLeaf() {
		t.Error("root should be internal after split")
	}
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(root.Children))
	}

	left := root.Children[0]
	right := root.Children[1]

	// Both children should be leaves
	if !left.IsLeaf() || !right.IsLeaf() {
		t.Error("children should be leaves")
	}

	// Pane assignments
	if left.Pane.ID != 1 {
		t.Errorf("left pane ID = %d, want 1", left.Pane.ID)
	}
	if right.Pane.ID != 2 {
		t.Errorf("right pane ID = %d, want 2", right.Pane.ID)
	}
	if newCell.Pane.ID != 2 {
		t.Errorf("returned cell pane ID = %d, want 2", newCell.Pane.ID)
	}

	// Sizes should add up: left.W + 1 (separator) + right.W = 80
	if left.W+1+right.W != 80 {
		t.Errorf("widths %d + 1 + %d != 80", left.W, right.W)
	}
	// Heights preserved
	if left.H != 24 || right.H != 24 {
		t.Errorf("heights = %d, %d, want 24, 24", left.H, right.H)
	}
}

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	_, err := root.Split(SplitVertical, p2)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	top := root.Children[0]
	bottom := root.Children[1]

	// Heights should add up: top.H + 1 + bottom.H = 24
	if top.H+1+bottom.H != 24 {
		t.Errorf("heights %d + 1 + %d != 24", top.H, bottom.H)
	}
	// Widths preserved
	if top.W != 80 || bottom.W != 80 {
		t.Errorf("widths = %d, %d, want 80, 80", top.W, bottom.W)
	}
}

func TestSplitTooSmall(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 4, 24) // Width 4 < 2*2+1=5

	p2 := fakePaneID(2)
	_, err := root.Split(SplitHorizontal, p2)
	if err == nil {
		t.Error("expected error for too-small split")
	}
}

func TestSplitSiblingInsertion(t *testing.T) {
	t.Parallel()
	// Split once horizontally, then split the left child horizontally again.
	// The second split should add a sibling (Case A) rather than nesting.
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	// Now split the left child (which has W=39) horizontally
	left := root.Children[0]
	p3 := fakePaneID(3)
	_, err := left.Split(SplitHorizontal, p3)
	if err != nil {
		t.Fatalf("second split: %v", err)
	}

	// Root should now have 3 children (sibling insertion), not nested
	if len(root.Children) != 3 {
		t.Errorf("root children = %d, want 3 (sibling insertion)", len(root.Children))
	}
}

func TestClosePane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	right := root.Children[1]
	rightW := right.W
	leftW := root.Children[0].W

	// Close the left pane
	recipient := root.Children[0].Close()

	// Right pane should receive the space
	if recipient.Pane.ID != 2 {
		t.Errorf("recipient pane ID = %d, want 2", recipient.Pane.ID)
	}
	// Right pane width should be original left + 1 separator + original right
	if recipient.W != leftW+1+rightW {
		t.Errorf("recipient width = %d, want %d", recipient.W, leftW+1+rightW)
	}
}

func TestCloseCollapsesSingleChild(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	// Close right pane — parent should collapse, left becomes new root-level cell
	right := root.Children[1]
	result := right.Close()

	// Result should be the remaining left child
	if result.Pane.ID != 1 {
		t.Errorf("remaining pane ID = %d, want 1", result.Pane.ID)
	}
	// Should now be a leaf with full dimensions
	if !result.IsLeaf() {
		t.Error("remaining cell should be a leaf")
	}
	if result.W != 80 {
		t.Errorf("remaining width = %d, want 80", result.W)
	}
}

func TestFixOffsets(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	// Manually mess up offsets
	root.Children[0].X = 999
	root.Children[1].X = 999

	root.FixOffsets()

	left := root.Children[0]
	right := root.Children[1]

	if left.X != 0 {
		t.Errorf("left.X = %d, want 0", left.X)
	}
	if right.X != left.W+1 {
		t.Errorf("right.X = %d, want %d", right.X, left.W+1)
	}
	if left.Y != 0 || right.Y != 0 {
		t.Errorf("Y offsets should be 0, got %d, %d", left.Y, right.Y)
	}
}

func TestResizeAll(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	// Resize to 120x40
	root.ResizeAll(120, 40)

	if root.W != 120 || root.H != 40 {
		t.Errorf("root size = %dx%d, want 120x40", root.W, root.H)
	}

	// Children widths should add up: c0.W + 1 + c1.W = 120
	total := root.Children[0].W + 1 + root.Children[1].W
	if total != 120 {
		t.Errorf("children total width = %d, want 120", total)
	}

	// All children should have height 40
	for i, child := range root.Children {
		if child.H != 40 {
			t.Errorf("child[%d].H = %d, want 40", i, child.H)
		}
	}
}

func TestWalkAndFindPane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	p3 := fakePaneID(3)
	root.Children[1].Split(SplitVertical, p3)

	// Walk should find 3 leaves
	count := 0
	root.Walk(func(c *LayoutCell) { count++ })
	if count != 3 {
		t.Errorf("Walk found %d leaves, want 3", count)
	}

	// FindPane
	cell := root.FindPane(3)
	if cell == nil || cell.Pane.ID != 3 {
		t.Error("FindPane(3) failed")
	}

	cell = root.FindPane(99)
	if cell != nil {
		t.Error("FindPane(99) should return nil")
	}
}

func TestNestedSplits(t *testing.T) {
	t.Parallel()
	// Create a 2x2 grid: split H, then split each half V
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 81, 25)

	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)

	p3 := fakePaneID(3)
	root.Children[0].Split(SplitVertical, p3)

	p4 := fakePaneID(4)
	root.Children[1].Split(SplitVertical, p4)

	root.FixOffsets()

	// Should have 4 leaves
	count := 0
	root.Walk(func(c *LayoutCell) { count++ })
	if count != 4 {
		t.Errorf("Walk found %d leaves, want 4", count)
	}

	// All panes should have valid positions (non-negative, within bounds)
	root.Walk(func(c *LayoutCell) {
		if c.X < 0 || c.Y < 0 {
			t.Errorf("pane %d has negative offset: (%d, %d)", c.Pane.ID, c.X, c.Y)
		}
		if c.X+c.W > 81 || c.Y+c.H > 25 {
			t.Errorf("pane %d exceeds bounds: pos=(%d,%d) size=(%d,%d)",
				c.Pane.ID, c.X, c.Y, c.W, c.H)
		}
	})
}
