package mux

import (
	"fmt"
	"reflect"
	"testing"
)

// fakePaneID creates a minimal Pane with just an ID for testing layout.
func fakePaneID(id uint32) *Pane {
	return &Pane{ID: id}
}

// assertApproxEqual checks that all sizes are approximately equal, allowing
// for integer rounding. The last child receives the remainder, so the maximum
// difference is len(sizes)-1.
func assertApproxEqual(t *testing.T, sizes []int) {
	t.Helper()
	minS, maxS := sizes[0], sizes[0]
	for _, s := range sizes[1:] {
		if s < minS {
			minS = s
		}
		if s > maxS {
			maxS = s
		}
	}
	limit := len(sizes) - 1
	if maxS-minS > limit {
		t.Errorf("sizes not approximately equal: %v (max-min=%d, limit=%d)", sizes, maxS-minS, limit)
	}
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

func TestSplitVertical(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	newCell, err := root.Split(SplitVertical, p2)
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

func TestSplitHorizontal(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	_, err := root.Split(SplitHorizontal, p2)
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
	_, err := root.Split(SplitVertical, p2)
	if err == nil {
		t.Error("expected error for too-small split")
	}
}

func TestSplitSiblingInsertion(t *testing.T) {
	t.Parallel()
	// Split once vertically, then split the left child vertically again.
	// The second split should add a sibling (Case A) rather than nesting.
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)

	// Now split the left child (which has W=39) vertically
	left := root.Children[0]
	p3 := fakePaneID(3)
	_, err := left.Split(SplitVertical, p3)
	if err != nil {
		t.Fatalf("second split: %v", err)
	}

	// Root should now have 3 children (sibling insertion), not nested
	if len(root.Children) != 3 {
		t.Errorf("root children = %d, want 3 (sibling insertion)", len(root.Children))
	}
}

func TestSplitSiblingEqualRedistribution(t *testing.T) {
	t.Parallel()
	// Case A split should redistribute space equally among all siblings.
	tests := []struct {
		name      string
		dir       SplitDir
		w, h      int
		wantTotal int // expected total (width for V, height for H)
	}{
		{"vertical", SplitVertical, 80, 24, 80},
		{"horizontal", SplitHorizontal, 80, 25, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := NewLeaf(fakePaneID(1), 0, 0, tt.w, tt.h)
			root.Split(tt.dir, fakePaneID(2))

			size := func(c *LayoutCell) int {
				if tt.dir == SplitVertical {
					return c.W
				}
				return c.H
			}

			// Split child0 again in the same direction (Case A)
			root.Children[0].Split(tt.dir, fakePaneID(3))

			// All 3 siblings should have approximately equal sizes
			if len(root.Children) != 3 {
				t.Fatalf("expected 3 children, got %d", len(root.Children))
			}
			sizes := make([]int, len(root.Children))
			for i, c := range root.Children {
				sizes[i] = size(c)
			}
			assertApproxEqual(t, sizes)

			// Total across all children + separators should be preserved
			total := 0
			for i, c := range root.Children {
				total += size(c)
				if i < len(root.Children)-1 {
					total++ // separator
				}
			}
			if total != tt.wantTotal {
				t.Errorf("total = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}

func TestClosePane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)

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
	root.Split(SplitVertical, p2)

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
	root.Split(SplitVertical, p2)

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
	root.Split(SplitVertical, p2)

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

func TestResizeAll_RoundTripKeepsEqualGridStable(t *testing.T) {
	t.Parallel()

	root := NewLeaf(fakePaneID(1), 0, 0, 80, 23)
	if _, err := root.Split(SplitVertical, fakePaneID(2)); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}
	if _, err := root.Children[1].Split(SplitVertical, fakePaneID(3)); err != nil {
		t.Fatalf("split root vertical again: %v", err)
	}

	nextID := uint32(4)
	for _, col := range root.Children {
		if _, err := col.Split(SplitHorizontal, fakePaneID(nextID)); err != nil {
			t.Fatalf("split column horizontal: %v", err)
		}
		nextID++
		if _, err := col.Children[1].Split(SplitHorizontal, fakePaneID(nextID)); err != nil {
			t.Fatalf("split column horizontal again: %v", err)
		}
		nextID++
	}
	root.FixOffsets()

	initial := snapshotLeafGeometry(root)

	root.ResizeAll(120, 39)
	root.ResizeAll(80, 23)

	if diff := diffLeafGeometry(initial, snapshotLeafGeometry(root)); diff != "" {
		t.Fatalf("equal 3x3 grid drifted after resize round-trip:\n%s", diff)
	}
}

func TestResizeAllPreservesUnevenSiblingProportions(t *testing.T) {
	t.Parallel()

	root := &LayoutCell{
		X:   0,
		Y:   0,
		W:   72,
		H:   24,
		Dir: SplitVertical,
	}
	root.Children = []*LayoutCell{
		NewLeaf(fakePaneID(1), 0, 0, 10, 24),
		NewLeaf(fakePaneID(2), 0, 0, 20, 24),
		NewLeaf(fakePaneID(3), 0, 0, 40, 24),
	}
	for _, child := range root.Children {
		child.Parent = root
	}
	root.FixOffsets()

	root.ResizeAll(142, 24)

	got := []int{root.Children[0].W, root.Children[1].W, root.Children[2].W}
	want := []int{19, 40, 81}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resized widths = %v, want %v", got, want)
	}

	root.ResizeAll(72, 24)

	got = []int{root.Children[0].W, root.Children[1].W, root.Children[2].W}
	want = []int{10, 20, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip widths = %v, want %v", got, want)
	}
}

func TestResizeSubtreeLeaf(t *testing.T) {
	t.Parallel()

	leaf := NewLeaf(fakePaneID(1), 0, 0, 20, 10)
	leaf.ResizeSubtree(30, 12)

	if leaf.W != 30 || leaf.H != 12 {
		t.Fatalf("leaf size after ResizeSubtree = %dx%d, want 30x12", leaf.W, leaf.H)
	}
}

func TestResizeSubtreeEmptyInternal(t *testing.T) {
	t.Parallel()

	cell := &LayoutCell{W: 20, H: 10, Dir: SplitVertical}
	cell.ResizeSubtree(30, 12)

	if cell.W != 20 || cell.H != 10 {
		t.Fatalf("empty internal cell changed to %dx%d, want 20x10", cell.W, cell.H)
	}
}

func TestResizeSubtreePreservesChildProportions(t *testing.T) {
	t.Parallel()

	root := &LayoutCell{
		X:   0,
		Y:   0,
		W:   344,
		H:   23,
		Dir: SplitVertical,
		Children: []*LayoutCell{
			NewLeaf(fakePaneID(1), 0, 0, 97, 23),
			NewLeaf(fakePaneID(2), 0, 0, 84, 23),
			NewLeaf(fakePaneID(3), 0, 0, 89, 23),
			NewLeaf(fakePaneID(4), 0, 0, 71, 23),
		},
	}
	for _, child := range root.Children {
		child.Parent = root
	}
	root.FixOffsets()

	root.ResizeSubtree(200, 23)

	got := leafAxisSizes(root, SplitVertical)
	want := []int{56, 49, 51, 41}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("child widths after proportional resize = %v, want %v", got, want)
	}
}

func TestResizeSubtreeMatchesResizeAllRemainderRounding(t *testing.T) {
	t.Parallel()

	original := &LayoutCell{
		X:   0,
		Y:   0,
		W:   40,
		H:   7,
		Dir: SplitHorizontal,
		Children: []*LayoutCell{
			NewLeaf(fakePaneID(1), 0, 0, 40, 3),
			NewLeaf(fakePaneID(2), 0, 0, 40, 3),
		},
	}
	for _, child := range original.Children {
		child.Parent = original
	}
	original.FixOffsets()

	got := CloneLayout(original)
	got.H = 6
	got.ResizeSubtree(40, 6)

	want := CloneLayout(original)
	want.ResizeAll(40, 6)

	if diff := diffLeafGeometry(snapshotLeafGeometry(want), snapshotLeafGeometry(got)); diff != "" {
		t.Fatalf("ResizeSubtree left geometry that ResizeAll would normalize:\n%s", diff)
	}
}

func TestResizeSubtreePinsMinChildrenBeforeRedistributing(t *testing.T) {
	t.Parallel()

	root := &LayoutCell{
		X:   0,
		Y:   0,
		W:   100,
		H:   23,
		Dir: SplitVertical,
		Children: []*LayoutCell{
			NewLeaf(fakePaneID(1), 0, 0, 40, 23),
			NewLeaf(fakePaneID(2), 0, 0, 5, 23),
			NewLeaf(fakePaneID(3), 0, 0, 30, 23),
			NewLeaf(fakePaneID(4), 0, 0, 22, 23),
		},
	}
	for _, child := range root.Children {
		child.Parent = root
	}
	root.FixOffsets()

	root.ResizeSubtree(23, 23)

	got := leafAxisSizes(root, SplitVertical)
	want := []int{7, 2, 6, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("child widths after min-clamped proportional resize = %v, want %v", got, want)
	}
}

func TestProportionalSubtreeChildSizesClampsTargetToMinimumTotal(t *testing.T) {
	t.Parallel()

	children := []*LayoutCell{
		NewLeaf(fakePaneID(1), 0, 0, 5, 5),
		NewLeaf(fakePaneID(2), 0, 0, 9, 5),
	}

	got := proportionalSubtreeChildSizes(children, SplitVertical, 3)
	want := []int{PaneMinSize, PaneMinSize}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sizes after clamping target = %v, want %v", got, want)
	}
}

func TestProportionalSubtreeChildSizesFallsBackToEqualSplitWhenWeightsAreZero(t *testing.T) {
	t.Parallel()

	children := []*LayoutCell{
		NewLeaf(fakePaneID(1), 0, 0, PaneMinSize, 5),
		NewLeaf(fakePaneID(2), 0, 0, PaneMinSize, 5),
		NewLeaf(fakePaneID(3), 0, 0, PaneMinSize, 5),
	}

	got := proportionalSubtreeChildSizes(children, SplitVertical, 8)
	want := []int{PaneMinSize, PaneMinSize + 1, PaneMinSize + 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sizes with zero weights = %v, want %v", got, want)
	}
}

func snapshotLeafGeometry(root *LayoutCell) map[uint32][4]int {
	out := map[uint32][4]int{}
	root.Walk(func(c *LayoutCell) {
		out[c.Pane.ID] = [4]int{c.X, c.Y, c.W, c.H}
	})
	return out
}

func diffLeafGeometry(want, got map[uint32][4]int) string {
	var out string
	for id := range want {
		if want[id] != got[id] {
			out += fmt.Sprintf("pane-%d: want=%v got=%v\n", id, want[id], got[id])
		}
	}
	return out
}

func leafAxisSizes(root *LayoutCell, axis SplitDir) []int {
	var out []int
	root.Walk(func(c *LayoutCell) {
		if axis == SplitVertical {
			out = append(out, c.W)
			return
		}
		out = append(out, c.H)
	})
	return out
}

func TestWalkAndFindPane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)

	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)

	p3 := fakePaneID(3)
	root.Children[1].Split(SplitHorizontal, p3)

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

func TestFindLeafAt(t *testing.T) {
	t.Parallel()
	// Vertical split: pane-1 (0..38) | border (39) | pane-2 (40..79)
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)
	root.FixOffsets()

	tests := []struct {
		name   string
		x, y   int
		wantID uint32 // 0 means nil (border/outside)
	}{
		{"left pane center", 10, 12, 1},
		{"right pane center", 50, 12, 2},
		{"left pane origin", 0, 0, 1},
		{"left pane edge", root.Children[0].W - 1, 0, 1},
		{"border column", root.Children[0].W, 12, 0},
		{"right pane origin", root.Children[1].X, 0, 2},
		{"outside right", 80, 12, 0},
		{"outside bottom", 10, 24, 0},
		{"negative coords", -1, -1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaf := root.FindLeafAt(tt.x, tt.y)
			if tt.wantID == 0 {
				if leaf != nil {
					t.Errorf("expected nil, got pane %d", leaf.Pane.ID)
				}
			} else {
				if leaf == nil {
					t.Fatalf("expected pane %d, got nil", tt.wantID)
				}
				if leaf.Pane.ID != tt.wantID {
					t.Errorf("pane ID = %d, want %d", leaf.Pane.ID, tt.wantID)
				}
			}
		})
	}
}

func TestFindLeafAtVerticalSplit(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 25)
	p2 := fakePaneID(2)
	root.Split(SplitHorizontal, p2)
	root.FixOffsets()

	top := root.Children[0]
	bottom := root.Children[1]

	// Top pane
	leaf := root.FindLeafAt(10, 5)
	if leaf == nil || leaf.Pane.ID != 1 {
		t.Errorf("expected pane 1 at (10,5)")
	}

	// Bottom pane
	leaf = root.FindLeafAt(10, bottom.Y+1)
	if leaf == nil || leaf.Pane.ID != 2 {
		t.Errorf("expected pane 2 at (10,%d)", bottom.Y+1)
	}

	// Border row
	borderY := top.Y + top.H
	leaf = root.FindLeafAt(10, borderY)
	if leaf != nil {
		t.Errorf("expected nil at border row %d, got pane %d", borderY, leaf.Pane.ID)
	}
}

func TestFindBorderAt(t *testing.T) {
	t.Parallel()
	// Vertical split: pane-1 | border | pane-2
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)
	root.FixOffsets()

	borderX := root.Children[0].W

	// Border position
	hit := root.FindBorderAt(borderX, 12)
	if hit == nil {
		t.Fatal("expected border hit at border column")
	}
	if hit.Dir != SplitVertical {
		t.Errorf("dir = %d, want SplitVertical", hit.Dir)
	}

	// Non-border position (inside pane)
	hit = root.FindBorderAt(10, 12)
	if hit != nil {
		t.Error("expected no border hit inside pane")
	}
}

func TestFindBorderAtNested(t *testing.T) {
	t.Parallel()
	// 2x2 grid: V split at root, then each child H split
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 81, 25)
	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)
	p3 := fakePaneID(3)
	root.Children[0].Split(SplitHorizontal, p3)
	p4 := fakePaneID(4)
	root.Children[1].Split(SplitHorizontal, p4)
	root.FixOffsets()

	// Vertical border between left and right halves
	vBorderX := root.Children[0].X + root.Children[0].W
	hit := root.FindBorderAt(vBorderX, 5)
	if hit == nil {
		t.Fatal("expected vertical border hit")
	}
	if hit.Dir != SplitVertical {
		t.Errorf("dir = %d, want SplitVertical", hit.Dir)
	}

	// Horizontal border in left half
	leftChild := root.Children[0]
	if !leftChild.IsLeaf() {
		hBorderY := leftChild.Children[0].Y + leftChild.Children[0].H
		hit = root.FindBorderAt(5, hBorderY)
		if hit == nil {
			t.Fatal("expected horizontal border hit in left half")
		}
		if hit.Dir != SplitHorizontal {
			t.Errorf("dir = %d, want SplitHorizontal", hit.Dir)
		}
	}
}

func TestFindBorderNear(t *testing.T) {
	t.Parallel()

	root := NewLeaf(fakePaneID(1), 0, 0, 80, 24)
	if _, err := root.Split(SplitHorizontal, fakePaneID(2)); err != nil {
		t.Fatalf("Split: %v", err)
	}

	borderY := root.Children[0].Y + root.Children[0].H

	if hit := root.FindBorderNear(10, borderY); hit == nil {
		t.Fatal("expected exact horizontal border hit")
	}
	if hit := root.FindBorderNear(10, borderY+1); hit == nil {
		t.Fatal("expected nearby horizontal border hit")
	}
	if hit := root.FindBorderNear(10, borderY-1); hit == nil {
		t.Fatal("expected nearby horizontal border hit")
	}
}

func TestNestedSplits(t *testing.T) {
	t.Parallel()
	// Create a 2x2 grid: split V, then split each half H
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 81, 25)

	p2 := fakePaneID(2)
	root.Split(SplitVertical, p2)

	p3 := fakePaneID(3)
	root.Children[0].Split(SplitHorizontal, p3)

	p4 := fakePaneID(4)
	root.Children[1].Split(SplitHorizontal, p4)

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
