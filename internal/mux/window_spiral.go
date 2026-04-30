package mux

import "fmt"

func (w *Window) rootChildForPaneID(paneID uint32) (*LayoutCell, int, error) {
	if w.IsLeadPane(paneID) {
		return nil, -1, fmt.Errorf("cannot operate on lead pane")
	}

	root := w.logicalRoot()
	if root == nil || root.IsLeaf() {
		return nil, -1, fmt.Errorf("window has no root-level split")
	}

	leaf, err := w.mustFindPane(paneID)
	if err != nil {
		return nil, -1, err
	}

	cell := leaf
	for cell.Parent != root {
		cell = cell.Parent
	}
	return cell, cell.IndexInParent(), nil
}

// ColumnIndexForPaneID reports which top-level vertical split column contains
// paneID. When a lead pane is anchored, it is always column 0 and the logical
// root columns are offset to 1, 2, ...
func (w *Window) ColumnIndexForPaneID(paneID uint32) (int, error) {
	if w == nil || w.Root == nil {
		return 0, fmt.Errorf("window has no layout")
	}
	if _, err := w.mustFindPane(paneID); err != nil {
		return 0, err
	}
	if w.IsLeadPane(paneID) {
		return 0, nil
	}

	columnBase := 0
	root := w.logicalRoot()
	if w.hasAnchoredLead() {
		columnBase = 1
	}
	if root == nil || root.IsLeaf() || root.Dir != SplitVertical {
		return columnBase, nil
	}

	_, idx, err := w.rootChildForPaneID(paneID)
	if err != nil {
		return 0, err
	}
	return columnBase + idx, nil
}

func (w *Window) columnContainerForPaneID(paneID uint32) (*LayoutCell, error) {
	if w.Root == nil {
		return nil, fmt.Errorf("window has no layout")
	}
	if _, err := w.mustFindPane(paneID); err != nil {
		return nil, err
	}
	if w.Root.IsLeaf() || w.Root.Dir != SplitVertical {
		return w.Root, nil
	}
	cell, _, err := w.rootChildForPaneID(paneID)
	if err != nil {
		return nil, err
	}
	return cell, nil
}

func firstOtherPaneID(cell *LayoutCell, exclude uint32) (uint32, bool) {
	other := uint32(0)
	cell.Walk(func(leaf *LayoutCell) {
		if other != 0 || leaf == nil || leaf.Pane == nil || leaf.Pane.ID == exclude {
			return
		}
		other = leaf.Pane.ID
	})
	return other, other != 0
}

func (w *Window) wrapColumnWithBottomPane(column *LayoutCell, pane *Pane) {
	oldParent := column.Parent
	oldIdx := column.IndexInParent()
	oldX, oldY, oldW, oldH := column.X, column.Y, column.W, column.H
	size2 := (oldH - 1) / 2
	size1 := oldH - 1 - size2

	column.ResizeSubtree(oldW, size1)
	newLeaf := NewLeaf(pane, oldX, oldY+size1+1, oldW, size2)
	newRoot := &LayoutCell{
		X:        oldX,
		Y:        oldY,
		W:        oldW,
		H:        oldH,
		Dir:      SplitHorizontal,
		Parent:   oldParent,
		Children: []*LayoutCell{column, newLeaf},
	}
	column.Parent = newRoot
	newLeaf.Parent = newRoot

	if oldParent == nil {
		w.Root = newRoot
		return
	}
	oldParent.Children[oldIdx] = newRoot
}

func (w *Window) appendPaneToColumn(column *LayoutCell, pane *Pane) {
	switch {
	case column.IsLeaf():
		w.wrapColumnWithBottomPane(column, pane)
	case column.Dir == SplitHorizontal:
		newLeaf := NewLeaf(pane, 0, 0, 0, 0)
		newLeaf.Parent = column
		column.Children = append(column.Children, newLeaf)
		column.distributeEqual()
	default:
		w.wrapColumnWithBottomPane(column, pane)
	}
}

func (w *Window) finishTreeMutation() {
	w.Root.FixOffsets()
	w.resizePTYs()
}

// MovePaneToColumn reparents paneID into the logical column selected by
// targetPaneID, appending the moved pane to the bottom of that column.
func (w *Window) MovePaneToColumn(paneID, targetPaneID uint32) error {
	w.assertOwner("MovePaneToColumn")
	if col := w.leadColumn(); col != nil {
		if (col.FindPane(paneID) != nil) != (col.FindPane(targetPaneID) != nil) {
			return fmt.Errorf("cannot move panes across lead column")
		}
	}

	sourceCell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
	}

	sourcePane := sourceCell.Pane
	sourceColumn, err := w.columnContainerForPaneID(paneID)
	if err != nil {
		return err
	}
	destColumn, err := w.columnContainerForPaneID(targetPaneID)
	if err != nil {
		return err
	}

	sameColumn := sourceColumn == destColumn
	destWasRoot := destColumn == w.Root
	anchorPaneID := targetPaneID
	if sameColumn && paneID == targetPaneID {
		ok := false
		anchorPaneID, ok = firstOtherPaneID(destColumn, paneID)
		if !ok {
			return nil
		}
	}

	if sameColumn || destColumn.IsLeaf() || destColumn.Dir != SplitHorizontal {
		if destColumn.H < 2*PaneMinSize+1 {
			return fmt.Errorf("not enough space to move pane into destination column (%d < %d)", destColumn.H, 2*PaneMinSize+1)
		}
	}

	if w.ZoomedPaneID != 0 {
		if err := w.Unzoom(); err != nil {
			return err
		}
	}

	sourceWasActive := w.ActivePane != nil && w.ActivePane.ID == paneID
	if err := w.ClosePane(paneID); err != nil {
		return err
	}

	postDestColumn := destColumn
	if sameColumn {
		if destWasRoot {
			postDestColumn = w.Root
		} else {
			postDestColumn, err = w.columnContainerForPaneID(anchorPaneID)
			if err != nil {
				return err
			}
		}
	}

	w.appendPaneToColumn(postDestColumn, sourcePane)
	w.finishTreeMutation()
	if sourceWasActive {
		w.setActive(sourcePane)
	}
	return nil
}

func (w *Window) splitSubtreeRootWithOptions(root *LayoutCell, dir SplitDir, newPane *Pane, insertFirst bool, opts SplitOptions) (*Pane, error) {
	if root == nil {
		return nil, fmt.Errorf("no layout")
	}
	if w.ZoomedPaneID != 0 && !opts.KeepFocus {
		if err := w.Unzoom(); err != nil {
			return nil, err
		}
	}

	newLeaf := NewLeaf(newPane, 0, 0, 0, 0)
	parent := root.Parent
	parentIdx := root.IndexInParent()
	equalizeAnchoredLeadAfterSplit := false

	if !root.IsLeaf() && root.Dir == dir {
		equalizeAnchoredLeadAfterSplit = w.shouldEqualizeAnchoredLeadAfterRootSplit(parent, parentIdx, dir)
		newLeaf.Parent = root
		if insertFirst {
			root.Children = append([]*LayoutCell{newLeaf}, root.Children...)
		} else {
			root.Children = append(root.Children, newLeaf)
		}
		root.distributeEqual()
	} else {
		oldRoot := root

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
		equalizeAnchoredLeadAfterSplit = w.shouldEqualizeAnchoredLeadAfterRootSplit(parent, parentIdx, dir)
	}

	if equalizeAnchoredLeadAfterSplit {
		w.equalizeAnchoredLeadColumns()
	}
	w.Root.FixOffsets()
	w.resizePTYs()
	w.restoreZoomedPaneSize()
	if !opts.KeepFocus {
		w.setActive(newPane)
	}
	return newPane, nil
}
