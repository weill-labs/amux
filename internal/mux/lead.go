package mux

import "fmt"

// SetLead designates a pane as the lead pane for the window.
// The lead pane is pinned to the left side at full height. When lead is set,
// the window uses an "absolute root" shape:
//
//	Root
//	├── Children[0] = lead pane
//	└── Children[1] = logical root for root-targeted layout operations
//
// The layout otherwise remains structural — no synthetic render tree.
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

	// Fast path: already in the anchored lead slot.
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

// hasAnchoredLead reports whether the current root shape is the anchored lead
// form: a vertical root with exactly two children and the lead pane in child 0.
func (w *Window) hasAnchoredLead() bool {
	if w.LeadPaneID == 0 || w.Root == nil || w.Root.IsLeaf() {
		return false
	}
	if w.Root.Dir != SplitVertical || len(w.Root.Children) != 2 {
		return false
	}
	lead := w.Root.Children[0]
	return lead != nil &&
		lead.IsLeaf() &&
		lead.Pane != nil &&
		lead.Pane.ID == w.LeadPaneID
}

func containsPane(cell *LayoutCell, paneID uint32) bool {
	return cell != nil && cell.FindPane(paneID) != nil
}

// logicalRoot returns the subtree that root-targeted operations should mutate.
// When lead is active, this is Root.Children[1]; otherwise it is Root.
func (w *Window) logicalRoot() *LayoutCell {
	if !w.hasAnchoredLead() {
		return w.Root
	}
	return w.Root.Children[1]
}

// logicalRootTarget returns the subtree root that "root" layout operations
// should mutate plus its parent/index in the absolute root.
func (w *Window) logicalRootTarget() (root, parent *LayoutCell, index int) {
	if !w.hasAnchoredLead() {
		return w.Root, nil, -1
	}
	return w.Root.Children[1], w.Root, 1
}

func (w *Window) leadColumn() *LayoutCell {
	if !w.hasAnchoredLead() {
		return nil
	}
	return w.Root.Children[0]
}

func (w *Window) hasPendingLead() bool {
	return w.LeadPaneID != 0 &&
		w.Root != nil &&
		w.Root.IsLeaf() &&
		w.Root.Pane != nil &&
		w.Root.Pane.ID == w.LeadPaneID
}

func (w *Window) materializePendingLead(newPane *Pane, opts SplitOptions) (*Pane, error) {
	// A lead role on a single-pane window becomes the anchored left column on the
	// first growth operation, regardless of the requested split direction.
	return w.splitRootTargetWithOptions(w.Root, nil, -1, SplitVertical, newPane, opts)
}
