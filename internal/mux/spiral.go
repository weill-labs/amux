package mux

import "fmt"

// SpiralAddPlan describes the next clockwise add-pane mutation for a window.
// In lead mode the plan targets only the right subtree.
type SpiralAddPlan struct {
	InheritPaneID     uint32
	SplitTargetPaneID uint32
	CanvasRootSplit   bool
	InsertNewFirst    bool
	SwapWithTarget    bool
	LeadMode          bool
}

type spiralCanvas struct {
	root     *LayoutCell
	leadMode bool
	columns  [][]*LayoutCell
	count    int
}

// PlanSpiralAdd validates the current layout prefix and returns the next
// add-pane mutation for either the whole window or the lead window's right
// subtree.
func (w *Window) PlanSpiralAdd() (SpiralAddPlan, error) {
	w.assertOwner("PlanSpiralAdd")

	canvas, err := w.spiralAddCanvas()
	if err != nil {
		return SpiralAddPlan{}, err
	}

	expected := spiralExpectedColumnHeights(canvas.count)
	if len(canvas.columns) != len(expected) {
		return SpiralAddPlan{}, spiralLayoutError(canvas.count)
	}
	for i := range expected {
		if len(canvas.columns[i]) != expected[i] {
			return SpiralAddPlan{}, spiralLayoutError(canvas.count)
		}
	}

	nextGrid := spiralCeilSqrt(canvas.count + 1)
	plan := SpiralAddPlan{LeadMode: canvas.leadMode}

	if spiralIsPerfectSquare(canvas.count) {
		plan.CanvasRootSplit = true
		plan.InsertNewFirst = nextGrid%2 == 1
		if plan.InsertNewFirst {
			plan.InheritPaneID = canvas.columns[0][0].Pane.ID
		} else {
			lastCol := canvas.columns[len(canvas.columns)-1]
			plan.InheritPaneID = lastCol[0].Pane.ID
		}
		return plan, nil
	}

	if nextGrid%2 == 1 {
		for _, col := range canvas.columns {
			if len(col) >= nextGrid {
				continue
			}
			plan.InheritPaneID = col[0].Pane.ID
			plan.SplitTargetPaneID = plan.InheritPaneID
			plan.SwapWithTarget = true
			return plan, nil
		}
	} else {
		for i := len(canvas.columns) - 1; i >= 0; i-- {
			col := canvas.columns[i]
			if len(col) >= nextGrid {
				continue
			}
			plan.InheritPaneID = col[len(col)-1].Pane.ID
			plan.SplitTargetPaneID = plan.InheritPaneID
			return plan, nil
		}
	}

	return SpiralAddPlan{}, spiralLayoutError(canvas.count)
}

// ApplySpiralAddPlan mutates the layout according to a previously computed
// spiral plan.
func (w *Window) ApplySpiralAddPlan(plan SpiralAddPlan, newPane *Pane, opts SplitOptions) (*Pane, error) {
	w.assertOwner("ApplySpiralAddPlan")

	if plan.CanvasRootSplit {
		canvas, err := w.spiralAddCanvas()
		if err != nil {
			return nil, err
		}
		return w.splitSubtreeRootWithOptions(canvas.root, SplitVertical, newPane, plan.InsertNewFirst, opts)
	}

	if _, err := w.SplitPaneWithOptions(plan.SplitTargetPaneID, SplitHorizontal, newPane, opts); err != nil {
		return nil, err
	}
	if plan.SwapWithTarget {
		if err := w.SwapPanes(plan.SplitTargetPaneID, newPane.ID); err != nil {
			return nil, err
		}
	}
	return newPane, nil
}

func (w *Window) spiralAddCanvas() (spiralCanvas, error) {
	if w.Root == nil {
		return spiralCanvas{}, fmt.Errorf("no layout")
	}

	root := w.Root
	leadMode := false
	if w.hasAnchoredLead() {
		if len(w.Root.Children) < 2 || w.Root.Children[1] == nil {
			return spiralCanvas{}, fmt.Errorf("lead pane has no right subtree")
		}
		root = w.Root.Children[1]
		leadMode = true
	}

	cols, err := spiralColumns(root)
	if err != nil {
		return spiralCanvas{}, err
	}

	count := 0
	for _, col := range cols {
		count += len(col)
	}
	if count == 0 {
		return spiralCanvas{}, fmt.Errorf("no panes in spiral canvas")
	}

	return spiralCanvas{
		root:     root,
		leadMode: leadMode,
		columns:  cols,
		count:    count,
	}, nil
}

func spiralColumns(root *LayoutCell) ([][]*LayoutCell, error) {
	if root == nil {
		return nil, fmt.Errorf("spiral layout requires a canvas")
	}
	if root.IsLeaf() {
		return [][]*LayoutCell{{root}}, nil
	}
	if root.Dir != SplitVertical {
		return nil, spiralLayoutError(rootLeafCount(root))
	}

	cols := make([][]*LayoutCell, 0, len(root.Children))
	for _, child := range root.Children {
		leaves, ok := spiralColumnLeaves(child)
		if !ok {
			return nil, spiralLayoutError(rootLeafCount(root))
		}
		cols = append(cols, leaves)
	}
	return cols, nil
}

func spiralColumnLeaves(cell *LayoutCell) ([]*LayoutCell, bool) {
	if cell == nil {
		return nil, false
	}
	if cell.IsLeaf() {
		if cell.Pane == nil {
			return nil, false
		}
		return []*LayoutCell{cell}, true
	}
	if cell.Dir != SplitHorizontal {
		return nil, false
	}

	leaves := make([]*LayoutCell, 0)
	for _, child := range cell.Children {
		childLeaves, ok := spiralColumnLeaves(child)
		if !ok {
			return nil, false
		}
		leaves = append(leaves, childLeaves...)
	}
	return leaves, true
}

func spiralExpectedColumnHeights(count int) []int {
	grid := spiralCeilSqrt(count)
	if grid <= 0 {
		return nil
	}
	if grid*grid == count {
		return spiralRepeatHeight(grid, grid)
	}

	inner := grid - 1
	added := count - inner*inner
	heights := spiralRepeatHeight(grid, inner)

	if grid%2 == 1 {
		if added <= grid {
			heights[0] = added
			return heights
		}
		fullCols := added - grid + 1
		for i := 0; i < fullCols; i++ {
			heights[i] = grid
		}
		return heights
	}

	if added <= grid {
		heights[grid-1] = added
		return heights
	}
	fullCols := added - grid + 1
	for i := grid - fullCols; i < grid; i++ {
		heights[i] = grid
	}
	return heights
}

func spiralRepeatHeight(n, height int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = height
	}
	return out
}

func spiralCeilSqrt(n int) int {
	if n <= 0 {
		return 0
	}
	root := 1
	for root*root < n {
		root++
	}
	return root
}

func spiralIsPerfectSquare(n int) bool {
	if n < 0 {
		return false
	}
	root := spiralCeilSqrt(n)
	return root*root == n
}

func rootLeafCount(cell *LayoutCell) int {
	count := 0
	cell.Walk(func(*LayoutCell) { count++ })
	return count
}

func spiralLayoutError(count int) error {
	return fmt.Errorf("add-pane requires a canonical spiral layout prefix for %d panes", count)
}

func (w *Window) splitSubtreeRootWithOptions(root *LayoutCell, dir SplitDir, newPane *Pane, insertFirst bool, opts SplitOptions) (*Pane, error) {
	if root == nil {
		return nil, fmt.Errorf("no layout")
	}
	if w.ZoomedPaneID != 0 && !opts.KeepFocus {
		w.Unzoom()
	}

	newLeaf := NewLeaf(newPane, 0, 0, 0, 0)

	if !root.IsLeaf() && root.Dir == dir {
		newLeaf.Parent = root
		if insertFirst {
			root.Children = append([]*LayoutCell{newLeaf}, root.Children...)
		} else {
			root.Children = append(root.Children, newLeaf)
		}
		root.distributeEqual()
	} else {
		oldRoot := root
		parent := oldRoot.Parent
		parentIdx := oldRoot.IndexInParent()

		newRoot := &LayoutCell{
			X: oldRoot.X, Y: oldRoot.Y, W: oldRoot.W, H: oldRoot.H,
			Dir: dir,
		}
		if insertFirst {
			newRoot.Children = []*LayoutCell{newLeaf, oldRoot}
		} else {
			newRoot.Children = []*LayoutCell{oldRoot, newLeaf}
		}
		newLeaf.Parent = newRoot
		oldRoot.Parent = newRoot

		if dir == SplitVertical {
			secondW := (oldRoot.W - 1) / 2
			firstW := oldRoot.W - 1 - secondW
			if insertFirst {
				newLeaf.W = firstW
				newLeaf.H = oldRoot.H
				oldRoot.ResizeSubtree(secondW, oldRoot.H)
			} else {
				newLeaf.W = secondW
				newLeaf.H = oldRoot.H
				oldRoot.ResizeSubtree(firstW, oldRoot.H)
			}
		} else {
			secondH := (oldRoot.H - 1) / 2
			firstH := oldRoot.H - 1 - secondH
			if insertFirst {
				newLeaf.W = oldRoot.W
				newLeaf.H = firstH
				oldRoot.ResizeSubtree(oldRoot.W, secondH)
			} else {
				newLeaf.W = oldRoot.W
				newLeaf.H = secondH
				oldRoot.ResizeSubtree(oldRoot.W, firstH)
			}
		}

		newRoot.Parent = parent
		if parent == nil {
			w.Root = newRoot
		} else {
			parent.Children[parentIdx] = newRoot
		}
		root = newRoot
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	w.restoreZoomedPaneSize()
	if !opts.KeepFocus {
		w.setActive(newPane)
	}
	return newPane, nil
}
