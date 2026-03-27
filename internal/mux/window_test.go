package mux

import (
	"fmt"
	"strings"
	"testing"
)

// buildLayout creates a Window with manually positioned leaf cells for
// testing directional focus. Each pane is placed at explicit (x,y,w,h)
// coordinates — no splits needed. Window dimensions are computed as the
// bounding box of all panes (accounting for 1-cell borders between them).
func buildLayout(active uint32, panes []struct {
	id         uint32
	x, y, w, h int
}) *Window {
	if len(panes) == 0 {
		panic("buildLayout: need at least one pane")
	}

	// Create leaves and compute bounding box for window dimensions.
	leaves := make([]*LayoutCell, len(panes))
	var activePane *Pane
	var maxX, maxY int
	for i, p := range panes {
		pane := fakePaneID(p.id)
		leaves[i] = NewLeaf(pane, p.x, p.y, p.w, p.h)
		if p.id == active {
			activePane = pane
		}
		if right := p.x + p.w; right > maxX {
			maxX = right
		}
		if bottom := p.y + p.h; bottom > maxY {
			maxY = bottom
		}
	}

	// Wrap all leaves in a single horizontal parent (the exact tree
	// structure doesn't matter — Focus() only uses Walk and cell geometry).
	root := &LayoutCell{
		X: 0, Y: 0, W: maxX, H: maxY,
		Dir:      SplitVertical,
		Children: leaves,
	}
	for _, l := range leaves {
		l.Parent = root
	}

	return &Window{
		Root:       root,
		ActivePane: activePane,
		Width:      maxX,
		Height:     maxY,
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
		Dir:      SplitHorizontal,
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

func TestResizeActiveLastChild(t *testing.T) {
	t.Parallel()
	// Regression: ResizeActive panicked with index out of range when
	// the active pane was the last child in its parent's children slice.
	// The bug accessed siblings[idx+1] before checking whether idx was
	// the last element.
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)
	root.FixOffsets()

	// Active pane is p2 — the LAST child (idx=1, len=2)
	w := &Window{Root: root, ActivePane: p2, Width: 80, Height: 24}

	// This should not panic. Resize left on a vertical split
	// moves the border between p1 and p2.
	result := w.ResizeActive("left", 2)
	if !result {
		t.Error("ResizeActive(left, 2) on last child should succeed")
	}
}

func TestResizeActiveFirstChild(t *testing.T) {
	t.Parallel()
	// Complementary test: active pane is the first child.
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)
	root.FixOffsets()

	// Active pane is p1 — the FIRST child (idx=0)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	result := w.ResizeActive("right", 2)
	if !result {
		t.Error("ResizeActive(right, 2) on first child should succeed")
	}
}

func TestResizePane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		splitDir  SplitDir
		paneID    uint32
		direction string
		delta     int
		wantOK    bool
	}{
		{
			name:      "grow right on vertical split",
			splitDir:  SplitVertical,
			paneID:    1,
			direction: "right",
			delta:     5,
			wantOK:    true,
		},
		{
			name:      "grow down on horizontal split",
			splitDir:  SplitHorizontal,
			paneID:    1,
			direction: "down",
			delta:     3,
			wantOK:    true,
		},
		{
			name:      "non-active pane resized",
			splitDir:  SplitVertical,
			paneID:    1, // active is p2
			direction: "right",
			delta:     2,
			wantOK:    true,
		},
		{
			name:      "invalid direction",
			splitDir:  SplitVertical,
			paneID:    1,
			direction: "diagonal",
			delta:     1,
			wantOK:    false,
		},
		{
			name:      "zero delta",
			splitDir:  SplitVertical,
			paneID:    1,
			direction: "right",
			delta:     0,
			wantOK:    false,
		},
		{
			name:      "nonexistent pane",
			splitDir:  SplitVertical,
			paneID:    99,
			direction: "right",
			delta:     1,
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p1 := fakePaneID(1)
			p2 := fakePaneID(2)

			root := NewLeaf(p1, 0, 0, 80, 24)
			root.Split(tt.splitDir, p2)
			root.FixOffsets()

			w := &Window{Root: root, ActivePane: p2, Width: 80, Height: 24}

			got := w.ResizePane(tt.paneID, tt.direction, tt.delta)
			if got != tt.wantOK {
				t.Errorf("ResizePane(%d, %q, %d) = %v, want %v",
					tt.paneID, tt.direction, tt.delta, got, tt.wantOK)
			}
		})
	}
}

func TestResizePaneWalksSiblingDonors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		active    uint32
		paneID    uint32
		direction string
		delta     int
		panes     []struct {
			id         uint32
			x, y, w, h int
		}
		wantWidths map[uint32]int
	}{
		{
			name:      "grow right skips an immediate sibling at minimum width",
			active:    1,
			paneID:    1,
			direction: "right",
			delta:     5,
			panes: []struct {
				id         uint32
				x, y, w, h int
			}{
				{1, 0, 0, 10, 24},
				{2, 11, 0, PaneMinSize, 24},
				{3, 14, 0, 20, 24},
			},
			wantWidths: map[uint32]int{
				1: 15,
				2: PaneMinSize,
				3: 15,
			},
		},
		{
			name:      "grow left walks headward when the adjacent sibling is pinned",
			active:    3,
			paneID:    3,
			direction: "left",
			delta:     5,
			panes: []struct {
				id         uint32
				x, y, w, h int
			}{
				{1, 0, 0, 20, 24},
				{2, 21, 0, PaneMinSize, 24},
				{3, 24, 0, 10, 24},
			},
			wantWidths: map[uint32]int{
				1: 15,
				2: PaneMinSize,
				3: 15,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := buildLayout(tt.active, tt.panes)
			if !w.ResizePane(tt.paneID, tt.direction, tt.delta) {
				t.Fatalf("ResizePane(%d, %q, %d) = false, want true", tt.paneID, tt.direction, tt.delta)
			}

			for paneID, wantWidth := range tt.wantWidths {
				cell := w.Root.FindPane(paneID)
				if cell == nil {
					t.Fatalf("pane-%d not found", paneID)
				}
				if cell.W != wantWidth {
					t.Errorf("pane-%d width = %d, want %d", paneID, cell.W, wantWidth)
				}
			}
		})
	}
}

func TestClosePaneRedistributesNestedSubtreeSizes(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 23)

	p2 := fakePaneID(2)
	if _, err := w.Split(SplitHorizontal, p2); err != nil {
		t.Fatalf("split horizontal: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split horizontal again: %v", err)
	}

	w.FocusPane(p1)
	p4 := fakePaneID(4)
	if _, err := w.Split(SplitVertical, p4); err != nil {
		t.Fatalf("split top row vertical: %v", err)
	}

	topSubtree := w.Root.Children[0]
	if topSubtree.IsLeaf() {
		t.Fatal("expected top child to be a subtree")
	}

	if err := w.ClosePane(p3.ID); err != nil {
		t.Fatalf("close pane-3: %v", err)
	}

	topSubtree = w.Root.Children[0]
	left := topSubtree.Children[0]
	right := topSubtree.Children[1]

	if left.H != topSubtree.H {
		t.Fatalf("left child height = %d, want subtree height %d", left.H, topSubtree.H)
	}
	if right.H != topSubtree.H {
		t.Fatalf("right child height = %d, want subtree height %d", right.H, topSubtree.H)
	}
}

func TestResizePanePreservesAdjacencyForFullWidthBottomPane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  string
	}{
		{name: "up", dir: "up"},
		{name: "down", dir: "down"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p1 := fakePaneID(1)
			w := NewWindow(p1, 80, 24)

			panes := map[uint32]*Pane{1: p1}
			mustPane := func(id uint32) *Pane {
				p, ok := panes[id]
				if !ok {
					p = fakePaneID(id)
					panes[id] = p
				}
				return p
			}

			if _, err := w.SplitRoot(SplitVertical, mustPane(2)); err != nil {
				t.Fatalf("split root vertical: %v", err)
			}
			if _, err := w.SplitRoot(SplitVertical, mustPane(3)); err != nil {
				t.Fatalf("split root vertical again: %v", err)
			}

			for _, id := range []uint32{1, 2, 3} {
				w.FocusPane(mustPane(id))
				if _, err := w.Split(SplitHorizontal, mustPane(uint32(len(panes)+1))); err != nil {
					t.Fatalf("split horizontal for pane-%d: %v", id, err)
				}
				if _, err := w.Split(SplitHorizontal, mustPane(uint32(len(panes)+1))); err != nil {
					t.Fatalf("split horizontal again for pane-%d: %v", id, err)
				}
			}

			w.FocusPane(mustPane(9))
			if _, err := w.SplitRoot(SplitHorizontal, mustPane(10)); err != nil {
				t.Fatalf("split root horizontal: %v", err)
			}

			if !w.ResizePane(10, tt.dir, 2) {
				t.Fatalf("resize pane-10 %s failed", tt.dir)
			}

			bottom := w.Root.FindPane(10)
			if bottom == nil {
				t.Fatal("pane-10 not found")
			}

			foundAdjacentAbove := false
			var coords []string
			w.Root.Walk(func(cell *LayoutCell) {
				if cell.Pane == nil {
					return
				}
				coords = append(coords,
					fmt.Sprintf("pane-%d=(x=%d y=%d w=%d h=%d)", cell.Pane.ID, cell.X, cell.Y, cell.W, cell.H),
				)
				if cell.Pane.ID == 10 {
					return
				}
				if cell.Y+cell.H+1 != bottom.Y {
					return
				}
				if cell.X >= bottom.X+bottom.W || cell.X+cell.W <= bottom.X {
					return
				}
				foundAdjacentAbove = true
			})

			if !foundAdjacentAbove {
				t.Fatalf("resize %s left pane-10 with no adjacent pane above; %s", tt.dir, strings.Join(coords, "; "))
			}
		})
	}
}

func TestSplitRootSameDirectionResizesNestedChildren(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	if _, err := w.SplitRoot(SplitVertical, fakePaneID(4)); err != nil {
		t.Fatalf("split root vertical again: %v", err)
	}

	leftColumn := w.Root.Children[0]
	if leftColumn.IsLeaf() {
		t.Fatal("expected left column to remain a subtree")
	}

	for i, child := range leftColumn.Children {
		if child.W != leftColumn.W {
			t.Fatalf("left column child[%d] width = %d, want column width %d", i, child.W, leftColumn.W)
		}
	}
}

func TestSplitRootWrapResizesNestedOldRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	if _, err := w.SplitRoot(SplitHorizontal, fakePaneID(4)); err != nil {
		t.Fatalf("split root horizontal: %v", err)
	}

	top := w.Root.Children[0]
	if top.IsLeaf() {
		t.Fatal("expected wrapped old root to be a subtree")
	}

	leftColumn := top.Children[0]
	if leftColumn.IsLeaf() {
		t.Fatal("expected left column to remain a subtree")
	}

	for i, child := range top.Children {
		if child.H != top.H {
			t.Fatalf("wrapped top child[%d] height = %d, want wrapped root height %d", i, child.H, top.H)
		}
	}
}

func TestResizePaneDelegation(t *testing.T) {
	t.Parallel()
	// Verify ResizeActive delegates to ResizePane correctly.
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)
	root.FixOffsets()

	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	leaf1 := root.FindPane(1)
	initialW := leaf1.W

	result := w.ResizeActive("right", 3)
	if !result {
		t.Fatal("ResizeActive should succeed")
	}
	if leaf1.W != initialW+3 {
		t.Errorf("pane-1 width after ResizeActive: got %d, want %d", leaf1.W, initialW+3)
	}
}

func TestSwapPanes(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p1.Meta.Name = "alpha"
	p2.Meta.Name = "beta"

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)

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

func TestRootChildForPaneID(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	tests := []struct {
		name     string
		paneID   uint32
		wantIdx  int
		wantCell *LayoutCell
	}{
		{name: "top pane", paneID: 1, wantIdx: 0, wantCell: w.Root.Children[0]},
		{name: "bottom pane in same branch", paneID: 3, wantIdx: 0, wantCell: w.Root.Children[0]},
		{name: "right pane", paneID: 2, wantIdx: 1, wantCell: w.Root.Children[1]},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cell, idx, err := w.rootChildForPaneID(tt.paneID)
			if err != nil {
				t.Fatalf("rootChildForPaneID(%d): %v", tt.paneID, err)
			}
			if idx != tt.wantIdx {
				t.Fatalf("rootChildForPaneID(%d) index = %d, want %d", tt.paneID, idx, tt.wantIdx)
			}
			if cell != tt.wantCell {
				t.Fatalf("rootChildForPaneID(%d) returned unexpected root child", tt.paneID)
			}
		})
	}
}

func TestRootChildForPaneIDErrors(t *testing.T) {
	t.Parallel()

	w := NewWindow(fakePaneID(1), 120, 24)
	if _, _, err := w.rootChildForPaneID(1); err == nil || !strings.Contains(err.Error(), "window has no root-level split") {
		t.Fatalf("rootChildForPaneID without root split = %v, want root-level split error", err)
	}

	if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}
	if _, _, err := w.rootChildForPaneID(99); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
		t.Fatalf("rootChildForPaneID missing pane = %v, want pane not found", err)
	}
}

func TestSwapTree(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	if !w.ResizePane(1, "right", 7) {
		t.Fatal("resize pane-1 right should succeed")
	}

	leftW := w.Root.Children[0].W
	rightW := w.Root.Children[1].W
	w.FocusPane(p1)

	if err := w.SwapTree(1, 2); err != nil {
		t.Fatalf("SwapTree: %v", err)
	}

	if got := w.Root.Children[0].FindPane(2); got == nil {
		t.Fatal("left root child should now contain pane-2")
	}
	if got := w.Root.Children[1].FindPane(1); got == nil {
		t.Fatal("right root child should now contain pane-1 subtree")
	}
	if got := w.Root.Children[1].FindPane(3); got == nil {
		t.Fatal("right root child should preserve pane-3 subtree member")
	}
	if got := w.Root.Children[0].W; got != rightW {
		t.Fatalf("moved pane-2 branch width = %d, want %d", got, rightW)
	}
	if got := w.Root.Children[1].W; got != leftW {
		t.Fatalf("moved pane-1 subtree width = %d, want %d", got, leftW)
	}
	if w.ActivePane != p1 {
		t.Fatalf("active pane = %v, want pane-1 pointer", w.ActivePane)
	}
}

func TestSwapTreeErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("no root split", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if err := w.SwapTree(1, 2); err == nil || !strings.Contains(err.Error(), "window has no root-level split") {
			t.Fatalf("SwapTree without root split = %v, want root-level split error", err)
		}
	})

	t.Run("missing pane", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.SwapTree(1, 99); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
			t.Fatalf("SwapTree missing pane = %v, want pane not found", err)
		}
	})
}

func TestSwapTreeRejectsSameRootBranch(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	err := w.SwapTree(1, 3)
	if err == nil {
		t.Fatal("expected same-branch swap-tree error")
	}
	if !strings.Contains(err.Error(), "same root-level group") {
		t.Fatalf("SwapTree same-branch error = %q, want same root-level group", err.Error())
	}
}

func TestSwapTreeAutoUnzooms(t *testing.T) {
	t.Parallel()

	w := NewWindow(fakePaneID(1), 80, 24)
	if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	if err := w.Zoom(2); err != nil {
		t.Fatalf("zoom pane-2: %v", err)
	}
	if err := w.SwapTree(1, 2); err != nil {
		t.Fatalf("SwapTree while zoomed: %v", err)
	}
	if w.ZoomedPaneID != 0 {
		t.Fatalf("swap should auto-unzoom, got ZoomedPaneID=%d", w.ZoomedPaneID)
	}
}

func TestMovePaneBefore(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("split root vertical again: %v", err)
	}

	w.FocusPane(p1)
	p4 := fakePaneID(4)
	if _, err := w.Split(SplitHorizontal, p4); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	if !w.ResizePane(1, "right", 6) {
		t.Fatal("resize pane-1 right should succeed")
	}

	leftW := w.Root.Children[0].W
	middleW := w.Root.Children[1].W
	rightW := w.Root.Children[2].W

	w.FocusPane(p1)
	if err := w.MovePane(3, 1, true); err != nil {
		t.Fatalf("MovePane before: %v", err)
	}

	if got := w.Root.Children[0].FindPane(3); got == nil {
		t.Fatal("first root child should now contain pane-3")
	}
	if got := w.Root.Children[1].FindPane(1); got == nil {
		t.Fatal("second root child should now contain pane-1 subtree")
	}
	if got := w.Root.Children[1].FindPane(4); got == nil {
		t.Fatal("second root child should preserve pane-4 subtree member")
	}
	if got := w.Root.Children[2].FindPane(2); got == nil {
		t.Fatal("third root child should now contain pane-2")
	}
	if got := w.Root.Children[0].W; got != rightW {
		t.Fatalf("pane-3 branch width after move = %d, want %d", got, rightW)
	}
	if got := w.Root.Children[1].W; got != leftW {
		t.Fatalf("pane-1 subtree width after move = %d, want %d", got, leftW)
	}
	if got := w.Root.Children[2].W; got != middleW {
		t.Fatalf("pane-2 branch width after move = %d, want %d", got, middleW)
	}
	if w.ActivePane != p1 {
		t.Fatalf("active pane = %v, want pane-1 pointer", w.ActivePane)
	}
}

func TestMovePaneErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("missing source pane", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.MovePane(99, 1, true); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
			t.Fatalf("MovePane missing source = %v, want pane not found", err)
		}
	})

	t.Run("missing target pane", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.MovePane(1, 99, true); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
			t.Fatalf("MovePane missing target = %v, want pane not found", err)
		}
	})

	t.Run("same root branch", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 120, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		w.FocusPane(w.Root.Children[0].Pane)
		if _, err := w.Split(SplitHorizontal, fakePaneID(3)); err != nil {
			t.Fatalf("split left column horizontal: %v", err)
		}

		if err := w.MovePane(3, 1, true); err != nil {
			t.Fatalf("MovePane within same split group: %v", err)
		}
		if got := []uint32{
			w.Root.Children[0].Children[0].Pane.ID,
			w.Root.Children[0].Children[1].Pane.ID,
		}; got[0] != 3 || got[1] != 1 {
			t.Fatalf("left split group order = %v, want [3 1]", got)
		}
	})

	t.Run("same root branch different split group", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 120, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		w.FocusPane(w.Root.Children[0].Pane)
		if _, err := w.Split(SplitHorizontal, fakePaneID(3)); err != nil {
			t.Fatalf("split left column horizontal: %v", err)
		}
		w.FocusPane(w.Root.Children[0].Children[1].Pane)
		if _, err := w.Split(SplitVertical, fakePaneID(4)); err != nil {
			t.Fatalf("split nested vertical: %v", err)
		}

		if err := w.MovePane(1, 4, true); err == nil || !strings.Contains(err.Error(), "same root-level group") {
			t.Fatalf("MovePane same root branch different split group = %v, want same root-level group", err)
		}
	})
}

func TestMovePaneDownWithinSplitGroup(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.Split(SplitHorizontal, p2); err != nil {
		t.Fatalf("split horizontal: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split horizontal again: %v", err)
	}

	if err := w.MovePaneDown(1); err != nil {
		t.Fatalf("MovePaneDown: %v", err)
	}

	if ids := collectPaneIDs(w); ids[0] != 2 || ids[1] != 1 || ids[2] != 3 {
		t.Fatalf("after move-down: %v, want [2 1 3]", ids)
	}
}

func TestMovePaneAutoUnzooms(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	if err := w.Zoom(2); err != nil {
		t.Fatalf("zoom pane-2: %v", err)
	}
	if err := w.MovePane(2, 1, true); err != nil {
		t.Fatalf("MovePane while zoomed: %v", err)
	}
	if w.ZoomedPaneID != 0 {
		t.Fatalf("move should auto-unzoom, got ZoomedPaneID=%d", w.ZoomedPaneID)
	}
}

func TestMovePaneAfter(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("split root vertical again: %v", err)
	}

	if err := w.MovePane(1, 3, false); err != nil {
		t.Fatalf("MovePane after: %v", err)
	}

	ids := collectPaneIDs(w)
	if ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Fatalf("after move after: %v, want [2 3 1]", ids)
	}
}

func TestMovePaneToColumnAcrossRootColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 120, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("split root vertical again: %v", err)
	}

	w.FocusPane(p1)
	p4 := fakePaneID(4)
	if _, err := w.Split(SplitHorizontal, p4); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}

	if err := w.MovePaneToColumn(4, 2); err != nil {
		t.Fatalf("MovePaneToColumn: %v", err)
	}

	if got := w.Root.Children[0].FindPane(1); got == nil {
		t.Fatal("left root child should contain pane-1 after extraction")
	}
	if got := w.Root.Children[1].FindPane(2); got == nil {
		t.Fatal("middle root child should contain pane-2 after insertion")
	}
	if got := w.Root.Children[1].FindPane(4); got == nil {
		t.Fatal("middle root child should contain moved pane-4")
	}
	if got := w.Root.Children[2].FindPane(3); got == nil {
		t.Fatal("right root child should contain pane-3")
	}

	middle := w.Root.Children[1]
	if middle.IsLeaf() || middle.Dir != SplitHorizontal {
		t.Fatalf("target root child = %+v, want horizontal stack", middle)
	}
	if middle.Children[0].Pane != p2 || middle.Children[1].Pane != p4 {
		t.Fatalf("target column child order = [%d %d], want [2 4]", middle.Children[0].Pane.ID, middle.Children[1].Pane.ID)
	}
	if w.ActivePane != p4 {
		t.Fatalf("active pane = %v, want pane-4 pointer", w.ActivePane)
	}
}

func TestMovePaneToColumnSameColumnMovesSourceToBottom(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	w.FocusPane(p1)
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split left column horizontal: %v", err)
	}
	p4 := fakePaneID(4)
	if _, err := w.Split(SplitHorizontal, p4); err != nil {
		t.Fatalf("split left column horizontal again: %v", err)
	}

	w.FocusPane(p2)
	if err := w.MovePaneToColumn(1, 1); err != nil {
		t.Fatalf("MovePaneToColumn same column: %v", err)
	}

	left := w.Root.Children[0]
	if left.IsLeaf() || left.Dir != SplitHorizontal {
		t.Fatalf("left root child = %+v, want horizontal stack", left)
	}
	if got := []uint32{left.Children[0].Pane.ID, left.Children[1].Pane.ID, left.Children[2].Pane.ID}; got[0] != 3 || got[1] != 4 || got[2] != 1 {
		t.Fatalf("left column order = %v, want [3 4 1]", got)
	}
	if w.ActivePane != p2 {
		t.Fatalf("active pane = %v, want pane-2 pointer", w.ActivePane)
	}
}

func TestMovePaneToColumnSingleColumnReordersToBottom(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.Split(SplitHorizontal, p2); err != nil {
		t.Fatalf("split horizontal: %v", err)
	}
	p3 := fakePaneID(3)
	if _, err := w.Split(SplitHorizontal, p3); err != nil {
		t.Fatalf("split horizontal again: %v", err)
	}

	if err := w.MovePaneToColumn(1, 1); err != nil {
		t.Fatalf("MovePaneToColumn single column: %v", err)
	}

	if ids := collectPaneIDs(w); ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Fatalf("single-column move order = %v, want [2 3 1]", ids)
	}
}

func TestMovePaneToColumnAutoUnzooms(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	p2 := fakePaneID(2)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("split root vertical: %v", err)
	}

	if err := w.Zoom(2); err != nil {
		t.Fatalf("zoom pane-2: %v", err)
	}
	if err := w.MovePaneToColumn(1, 2); err != nil {
		t.Fatalf("MovePaneToColumn while zoomed: %v", err)
	}
	if w.ZoomedPaneID != 0 {
		t.Fatalf("move-to should auto-unzoom, got ZoomedPaneID=%d", w.ZoomedPaneID)
	}
}

func TestMovePaneToColumnErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("missing source pane", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.MovePaneToColumn(99, 1); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
			t.Fatalf("MovePaneToColumn missing source = %v, want pane not found", err)
		}
	})

	t.Run("missing target pane", func(t *testing.T) {
		t.Parallel()

		w := NewWindow(fakePaneID(1), 80, 24)
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(2)); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.MovePaneToColumn(1, 99); err == nil || !strings.Contains(err.Error(), "pane 99 not found") {
			t.Fatalf("MovePaneToColumn missing target = %v, want pane not found", err)
		}
	})

	t.Run("lead column boundary", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		p2 := fakePaneID(2)
		w := NewWindow(p1, 80, 24)
		if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.SetLead(1); err != nil {
			t.Fatalf("SetLead: %v", err)
		}
		if err := w.MovePaneToColumn(1, 2); err == nil || !strings.Contains(err.Error(), "lead column") {
			t.Fatalf("MovePaneToColumn across lead boundary = %v, want lead-column error", err)
		}
	})

	t.Run("lead column boundary into lead pane", func(t *testing.T) {
		t.Parallel()

		p1 := fakePaneID(1)
		p2 := fakePaneID(2)
		w := NewWindow(p1, 80, 24)
		if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
			t.Fatalf("split root vertical: %v", err)
		}
		if err := w.SetLead(1); err != nil {
			t.Fatalf("SetLead: %v", err)
		}
		if err := w.MovePaneToColumn(2, 1); err == nil || !strings.Contains(err.Error(), "lead column") {
			t.Fatalf("MovePaneToColumn into lead column = %v, want lead-column error", err)
		}
	})
}

func TestSwapPaneForward(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	root := NewLeaf(p1, 0, 0, 120, 24)
	root.Split(SplitVertical, p2)
	root.Children[1].Split(SplitVertical, p3)

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
	root.Split(SplitVertical, p2)
	root.Children[1].Split(SplitVertical, p3)

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
	root.Split(SplitVertical, p2)
	root.Children[1].Split(SplitVertical, p3)

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
	root.Split(SplitVertical, p2)
	root.Children[1].Split(SplitVertical, p3)

	w := &Window{Root: root, ActivePane: p1, Width: 120, Height: 24}

	w.RotatePanes(false)

	// Backward: each cell gets the pane from the next cell (first wraps to last)
	// Before: [1, 2, 3], After: [2, 3, 1]
	ids := collectPaneIDs(w)
	if ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Errorf("after backward rotate: %v, want [2,3,1]", ids)
	}
}

// ---------------------------------------------------------------------------
// ResizeActive (regression: index out of bounds when active pane is last child)
// ---------------------------------------------------------------------------

func TestResizeActiveFromLastChild(t *testing.T) {
	t.Parallel()
	// Two panes side by side: [pane-1 | pane-2], pane-2 active (last child).
	// ResizeActive("left", 2) should move the border left without panicking.
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)

	w := &Window{Root: root, ActivePane: p2, Width: 80, Height: 24}
	w.Root.FixOffsets()

	initialP1W := root.Children[0].W

	ok := w.ResizeActive("left", 2)
	if !ok {
		t.Fatal("ResizeActive returned false, expected true")
	}

	newP1W := root.Children[0].W
	if newP1W >= initialP1W {
		t.Errorf("pane-1 width should shrink: was %d, now %d", initialP1W, newP1W)
	}
}

func TestResizeActiveFromFirstChild(t *testing.T) {
	t.Parallel()
	// Two panes side by side: [pane-1 | pane-2], pane-1 active (first child).
	// ResizeActive("right", 2) should move the border right.
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	root := NewLeaf(p1, 0, 0, 80, 24)
	root.Split(SplitVertical, p2)

	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}
	w.Root.FixOffsets()

	initialP1W := root.Children[0].W

	ok := w.ResizeActive("right", 2)
	if !ok {
		t.Fatal("ResizeActive returned false, expected true")
	}

	newP1W := root.Children[0].W
	if newP1W <= initialP1W {
		t.Errorf("pane-1 width should grow: was %d, now %d", initialP1W, newP1W)
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

// ---------------------------------------------------------------------------
// SplicePane
// ---------------------------------------------------------------------------

func TestSplicePaneSingleReplacement(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	cells, err := w.SplicePane(1, []*Pane{p2})
	if err != nil {
		t.Fatalf("SplicePane: %v", err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell, got %d", len(cells))
	}
	if cells[0].Pane.ID != 2 {
		t.Errorf("spliced pane ID = %d, want 2", cells[0].Pane.ID)
	}
	// Root should still be a leaf
	if !w.Root.IsLeaf() {
		t.Error("root should still be a leaf after 1:1 splice")
	}
	if w.Root.Pane.ID != 2 {
		t.Errorf("root pane ID = %d, want 2", w.Root.Pane.ID)
	}
	// Active pane should update
	if w.ActivePane.ID != 2 {
		t.Errorf("active pane = %d, want 2", w.ActivePane.ID)
	}
}

func TestSplicePaneMultiple(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	proxyA := fakePaneID(10)
	proxyB := fakePaneID(11)
	proxyC := fakePaneID(12)

	cells, err := w.SplicePane(1, []*Pane{proxyA, proxyB, proxyC})
	if err != nil {
		t.Fatalf("SplicePane: %v", err)
	}
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d", len(cells))
	}

	// Root should now be internal with vertical split
	if w.Root.IsLeaf() {
		t.Error("root should be internal after multi-pane splice")
	}
	if w.Root.Dir != SplitVertical {
		t.Errorf("root dir = %d, want SplitVertical", w.Root.Dir)
	}
	if len(w.Root.Children) != 3 {
		t.Fatalf("root children = %d, want 3", len(w.Root.Children))
	}

	// All children should be leaves with correct panes
	for i, expected := range []uint32{10, 11, 12} {
		child := w.Root.Children[i]
		if !child.IsLeaf() {
			t.Errorf("child %d should be a leaf", i)
		}
		if child.Pane.ID != expected {
			t.Errorf("child %d pane ID = %d, want %d", i, child.Pane.ID, expected)
		}
	}

	// Widths should add up: w1 + 1 + w2 + 1 + w3 = 80
	totalW := 0
	for i, child := range w.Root.Children {
		totalW += child.W
		if i < len(w.Root.Children)-1 {
			totalW++ // separator
		}
	}
	if totalW != 80 {
		t.Errorf("total width = %d, want 80", totalW)
	}
}

func TestSplicePaneInSplitLayout(t *testing.T) {
	t.Parallel()
	// Create a window with 2 panes (vertical split), then splice pane-2
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	_, err := root.Split(SplitVertical, p2)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	w.setActive(p2)

	// Splice pane-2 with two proxy panes
	proxyA := fakePaneID(20)
	proxyB := fakePaneID(21)
	_, err = w.SplicePane(2, []*Pane{proxyA, proxyB})
	if err != nil {
		t.Fatalf("SplicePane: %v", err)
	}

	// Walk should find 3 panes: p1, proxyA, proxyB
	ids := collectPaneIDs(w)
	if len(ids) != 3 {
		t.Fatalf("expected 3 panes, got %d: %v", len(ids), ids)
	}
}

func TestSplicePaneNotFound(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	_, err := w.SplicePane(99, []*Pane{fakePaneID(2)})
	if err == nil {
		t.Error("expected error for non-existent pane")
	}
}

func TestSplicePaneEmpty(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	_, err := w.SplicePane(1, nil)
	if err == nil {
		t.Error("expected error for empty panes list")
	}
}

func TestUnsplicePane(t *testing.T) {
	t.Parallel()
	p1 := fakePaneID(1)
	root := NewLeaf(p1, 0, 0, 80, 24)
	w := &Window{Root: root, ActivePane: p1, Width: 80, Height: 24}

	// Splice in two proxy panes
	proxyA := &Pane{ID: 10, Meta: PaneMeta{Host: "gpu-box"}, writeOverride: func(b []byte) (int, error) { return len(b), nil }}
	proxyB := &Pane{ID: 11, Meta: PaneMeta{Host: "gpu-box"}, writeOverride: func(b []byte) (int, error) { return len(b), nil }}
	_, err := w.SplicePane(1, []*Pane{proxyA, proxyB})
	if err != nil {
		t.Fatalf("SplicePane: %v", err)
	}

	// Unsplice back to a single pane
	replacement := fakePaneID(99)
	err = w.UnsplicePane("gpu-box", replacement)
	if err != nil {
		t.Fatalf("UnsplicePane: %v", err)
	}

	// Should have exactly one pane
	ids := collectPaneIDs(w)
	if len(ids) != 1 || ids[0] != 99 {
		t.Errorf("after unsplice: panes = %v, want [99]", ids)
	}
	if w.ActivePane.ID != 99 {
		t.Errorf("active pane = %d, want 99", w.ActivePane.ID)
	}
}
