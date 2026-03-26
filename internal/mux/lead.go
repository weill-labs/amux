package mux

import "fmt"

// SetLead designates a pane as the lead pane for the window.
// The lead pane is pinned to the left side at full height.
// The root is normalized into a binary SplitVertical with the lead
// in Children[0] and everything else in Children[1].
func (w *Window) SetLead(paneID uint32) error {
	w.assertOwner("SetLead")

	if w.LeadPaneID == paneID {
		return nil // idempotent
	}

	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}

	if w.Root.IsLeaf() {
		return fmt.Errorf("cannot set lead with only one pane")
	}

	// Clear any existing lead before restructuring.
	w.LeadPaneID = 0

	// Fast path: already in position (root is vertical, pane is Children[0] leaf, exactly 2 children).
	if !w.Root.IsLeaf() && w.Root.Dir == SplitVertical &&
		len(w.Root.Children) == 2 &&
		w.Root.Children[0].IsLeaf() && w.Root.Children[0].Pane != nil &&
		w.Root.Children[0].Pane.ID == paneID {
		w.LeadPaneID = paneID
		return nil
	}

	// Save the pane's current width for sizing the lead column.
	savedW := cell.W

	// Extract the pane from the tree.
	result := cell.Close()

	// Handle collapse-to-root (same as ClosePane).
	if result != nil && result.Parent == nil {
		result.X = 0
		result.Y = 0
		result.W = w.Width
		result.H = w.Height
		w.Root = result
	}

	// Redistribute space in the remaining tree.
	w.Root.ResizeAll(w.Width, w.Height)

	// Clamp lead width to [20%, 80%] of window.
	minW := max(20, w.Width/5)
	maxW := w.Width * 4 / 5
	leadW := max(min(savedW, maxW), minW)
	rightW := w.Width - leadW - 1 // -1 for separator
	if rightW < 1 {
		rightW = 1
		leadW = w.Width - 1 - rightW
	}

	// Create the lead leaf.
	leadLeaf := NewLeaf(cell.Pane, 0, 0, leadW, w.Height)

	// Resize the remaining tree to fit the right side.
	w.Root.ResizeSubtree(rightW, w.Height)

	// Build new binary root.
	newRoot := &LayoutCell{
		X: 0, Y: 0, W: w.Width, H: w.Height,
		Dir:      SplitVertical,
		Children: []*LayoutCell{leadLeaf, w.Root},
	}
	leadLeaf.Parent = newRoot
	w.Root.Parent = newRoot
	w.Root = newRoot

	w.Root.FixOffsets()
	w.resizePTYs()

	w.LeadPaneID = paneID
	return nil
}

// UnsetLead removes the lead pane designation. The layout stays as-is.
func (w *Window) UnsetLead() error {
	w.assertOwner("UnsetLead")
	if w.LeadPaneID == 0 {
		return fmt.Errorf("no lead pane set")
	}
	w.LeadPaneID = 0
	return nil
}

// IsLeadPane reports whether the given pane is the current lead pane.
func (w *Window) IsLeadPane(paneID uint32) bool {
	return w.LeadPaneID != 0 && w.LeadPaneID == paneID
}

// leadColumn returns the root-level cell that contains the lead pane
// (Root.Children[0] when the lead invariant holds). Returns nil if no lead.
func (w *Window) leadColumn() *LayoutCell {
	if w.LeadPaneID == 0 || w.Root == nil || w.Root.IsLeaf() {
		return nil
	}
	if w.Root.Dir != SplitVertical || len(w.Root.Children) < 2 {
		return nil
	}
	return w.Root.Children[0]
}

// containsPane reports whether cell's subtree contains the given pane ID.
func containsPane(cell *LayoutCell, paneID uint32) bool {
	if cell == nil {
		return false
	}
	return cell.FindPane(paneID) != nil
}

// firstLeafCell returns the first leaf LayoutCell (with a non-nil Pane) in the subtree.
func firstLeafCell(cell *LayoutCell) *LayoutCell {
	found := (*LayoutCell)(nil)
	cell.Walk(func(c *LayoutCell) {
		if found == nil && c.Pane != nil {
			found = c
		}
	})
	return found
}

// LeadAwareSplitTarget returns a redirect target when the active pane is
// in the lead column. Spawn/split operations should use this target instead
// of the active pane. Returns nil if no redirect is needed.
func (w *Window) LeadAwareSplitTarget() *Pane {
	col := w.leadColumn()
	if col == nil || w.ActivePane == nil {
		return nil
	}
	if !containsPane(col, w.ActivePane.ID) {
		return nil // active pane is not in the lead column
	}
	// Find first leaf in the right subtree.
	if cell := firstLeafCell(w.Root.Children[1]); cell != nil {
		return cell.Pane
	}
	return nil
}
