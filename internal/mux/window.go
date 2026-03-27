package mux

import (
	"fmt"
	"sync/atomic"

	"github.com/weill-labs/amux/internal/debugowner"
)

// StatusLineRows is the number of rows reserved for the per-pane status line.
const StatusLineRows = 1

// Window holds the layout tree and active pane for one window.
//
// Concurrency:
// Window is owned by the server session event loop. No Window methods are safe
// for concurrent use; callers must serialize all reads and writes through the
// session queue/query helpers or otherwise guarantee exclusive access.
type Window struct {
	owner        debugowner.Checker
	ID           uint32
	Name         string
	Root         *LayoutCell
	ActivePane   *Pane
	Width        int
	Height       int
	ZoomedPaneID uint32 // non-zero when a pane is zoomed to full window
	LeadPaneID   uint32 // non-zero when a pane is pinned as lead (full-height left column)
}

// SplitOptions controls whether the existing active pane keeps focus.
// KeepFocus preserves zoom state and leaves the active pane unchanged.
type SplitOptions struct {
	KeepFocus bool
}

// NewWindow creates a window with a single pane.
func NewWindow(pane *Pane, width, height int) *Window {
	root := NewLeaf(pane, 0, 0, width, height)
	return &Window{
		Root:       root,
		ActivePane: pane,
		Width:      width,
		Height:     height,
	}
}

func (w *Window) assertOwner(method string) {
	w.owner.Assert("mux.Window", method)
}

// SplitRoot splits the entire window at the root level.
// If the root already has the same split direction, the new pane is added
// as a sibling (equal distribution). Otherwise, wraps the root in a new parent.
// Auto-unzooms if a pane is zoomed.
func (w *Window) SplitRoot(dir SplitDir, newPane *Pane) (*Pane, error) {
	w.assertOwner("SplitRoot")
	return w.SplitRootWithOptions(dir, newPane, SplitOptions{})
}

// SplitRootWithOptions splits the entire window at the root level with
// explicit focus/zoom behavior control.
func (w *Window) SplitRootWithOptions(dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	w.assertOwner("SplitRootWithOptions")
	if w.ZoomedPaneID != 0 && !opts.KeepFocus {
		w.Unzoom()
	}
	targetRoot, parent, parentIdx := w.logicalRootTarget()
	return w.splitRootTargetWithOptions(targetRoot, parent, parentIdx, dir, newPane, opts)
}

func (w *Window) splitRootTargetWithOptions(targetRoot, parent *LayoutCell, parentIdx int, dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	if targetRoot == nil {
		return nil, fmt.Errorf("window has no layout root")
	}
	newLeaf := NewLeaf(newPane, 0, 0, 0, 0)

	if !targetRoot.IsLeaf() && targetRoot.Dir == dir {
		// Same direction: add as sibling, redistribute equally
		newLeaf.Parent = targetRoot
		targetRoot.Children = append(targetRoot.Children, newLeaf)
		// Give all children equal sizes so ResizeAll distributes fairly
		n := len(targetRoot.Children)
		seps := n - 1
		if dir == SplitVertical {
			each := (targetRoot.W - seps) / n
			for _, child := range targetRoot.Children {
				child.ResizeSubtree(each, targetRoot.H)
			}
			// Give remainder to last child
			targetRoot.Children[n-1].ResizeSubtree(targetRoot.W-seps-each*(n-1), targetRoot.H)
		} else {
			each := (targetRoot.H - seps) / n
			for _, child := range targetRoot.Children {
				child.ResizeSubtree(targetRoot.W, each)
			}
			targetRoot.Children[n-1].ResizeSubtree(targetRoot.W, targetRoot.H-seps-each*(n-1))
		}
	} else {
		// Different direction or root is a leaf: wrap
		oldRoot := targetRoot
		oldX, oldY := oldRoot.X, oldRoot.Y
		oldW, oldH := oldRoot.W, oldRoot.H

		if dir == SplitVertical {
			size2 := (oldW - 1) / 2
			size1 := oldW - 1 - size2
			newLeaf.W = size2
			newLeaf.H = oldH
			oldRoot.ResizeSubtree(size1, oldH)
		} else {
			size2 := (oldH - 1) / 2
			size1 := oldH - 1 - size2
			newLeaf.W = oldW
			newLeaf.H = size2
			oldRoot.ResizeSubtree(oldW, size1)
		}

		newRoot := &LayoutCell{
			X: oldX, Y: oldY, W: oldW, H: oldH,
			Dir:      dir,
			Children: []*LayoutCell{oldRoot, newLeaf},
		}
		oldRoot.Parent = newRoot
		newLeaf.Parent = newRoot
		if parent == nil {
			w.Root = newRoot
		} else {
			parent.Children[parentIdx] = newRoot
			newRoot.Parent = parent
		}
	}

	w.Root.FixOffsets()

	w.resizePTYs()
	w.restoreZoomedPaneSize()

	if !opts.KeepFocus {
		w.setActive(newPane)
	}
	return newPane, nil
}

// Split splits the active pane in the given direction, creating a new pane
// via the provided factory function. Returns the new pane.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Split(dir SplitDir, newPane *Pane) (*Pane, error) {
	w.assertOwner("Split")
	return w.SplitWithOptions(dir, newPane, SplitOptions{})
}

// SplitWithOptions splits the active pane with explicit focus/zoom behavior control.
func (w *Window) SplitWithOptions(dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	w.assertOwner("SplitWithOptions")
	return w.SplitPaneWithOptions(w.ActivePane.ID, dir, newPane, opts)
}

// SplitPaneWithOptions splits the specified pane with explicit focus/zoom
// behavior control.
func (w *Window) SplitPaneWithOptions(targetPaneID uint32, dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	w.assertOwner("SplitPaneWithOptions")
	if w.ZoomedPaneID != 0 && !opts.KeepFocus {
		w.Unzoom()
	}
	if w.IsLeadPane(targetPaneID) {
		return nil, fmt.Errorf("cannot operate on lead pane")
	}
	cell := w.Root.FindPane(targetPaneID)
	if cell == nil {
		return nil, fmt.Errorf("pane %d not found in layout", targetPaneID)
	}
	return w.splitCellWithOptions(cell, dir, newPane, opts)
}

func (w *Window) splitCellWithOptions(cell *LayoutCell, dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	newCell, err := cell.Split(dir, newPane)
	if err != nil {
		return nil, err
	}

	// Resize PTYs to match layout cells (minus status line row)
	newPane.Resize(newCell.W, PaneContentHeight(newCell.H))

	// Find the existing pane's cell without a second tree walk:
	// - Case A (sibling insertion): cell itself still holds the existing pane
	// - Case B (new parent): cell became internal; existing pane is in Children[0]
	var existingCell *LayoutCell
	if cell.IsLeaf() {
		existingCell = cell
	} else if len(cell.Children) > 0 {
		existingCell = cell.Children[0]
	}
	if existingCell != nil && existingCell.Pane != nil {
		existingCell.Pane.Resize(existingCell.W, PaneContentHeight(existingCell.H))
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	w.restoreZoomedPaneSize()
	if !opts.KeepFocus {
		w.setActive(newPane)
	}

	return newPane, nil
}

// ClosePane removes a pane from the layout and reclaims its space.
// If the closed pane was zoomed, zoom is automatically cleared.
func (w *Window) ClosePane(paneID uint32) error {
	w.assertOwner("ClosePane")
	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}

	// Count leaves to prevent closing the last pane
	count := 0
	w.Root.Walk(func(_ *LayoutCell) { count++ })
	if count <= 1 {
		return fmt.Errorf("cannot close last pane")
	}

	// Clear lead if closing the lead pane
	if w.LeadPaneID == paneID {
		w.LeadPaneID = 0
	}

	// Auto-unzoom if closing the zoomed pane
	if w.ZoomedPaneID == paneID {
		w.ZoomedPaneID = 0
	}

	result := cell.Close()

	// If Close() collapsed a single-child parent that was the root,
	// result has Parent==nil and should become the new root.
	if result != nil && result.Parent == nil {
		result.X = 0
		result.Y = 0
		result.W = w.Width
		result.H = w.Height
		w.Root = result
	}

	// Propagate sizes to all children after redistribution
	w.Root.ResizeAll(w.Width, w.Height)

	// Clear lead if only one pane remains (no column to pin against).
	if w.LeadPaneID != 0 && w.Root.IsLeaf() {
		w.LeadPaneID = 0
	}

	// Update active pane if the closed pane was active
	if w.ActivePane.ID == paneID {
		if result != nil && result.IsLeaf() && result.Pane != nil {
			w.setActive(result.Pane)
		} else {
			// Find any leaf
			w.Root.Walk(func(c *LayoutCell) {
				if w.ActivePane.ID == paneID && c.Pane != nil {
					w.setActive(c.Pane)
				}
			})
		}
	}

	w.Root.FixOffsets()
	w.resizePTYs()

	return nil
}

// Resize adjusts the layout to fit new terminal dimensions.
func (w *Window) Resize(width, height int) {
	w.assertOwner("Resize")
	w.Width = width
	w.Height = height
	w.Root.ResizeAll(width, height)

	w.resizePTYs()
	w.restoreZoomedPaneSize()
}

// activePointCounter is a package-level monotonic counter for pane focus recency.
var activePointCounter uint64

// setActive updates the active pane and increments ActivePoint for recency tracking.
func (w *Window) setActive(p *Pane) {
	w.ActivePane = p
	p.ActivePoint = atomic.AddUint64(&activePointCounter, 1)
}

// FocusPane sets the active pane directly (by pointer) and updates recency.
// Used by the server when focusing by name or ID.
// Auto-unzooms if a pane is zoomed and the target is a different pane.
func (w *Window) FocusPane(p *Pane) {
	w.assertOwner("FocusPane")
	if w.ZoomedPaneID != 0 && p.ID != w.ZoomedPaneID {
		w.Unzoom()
	}
	w.setActive(p)
}

// Focus changes the active pane. Direction is "next", "left", "right", "up", "down".
// Uses tmux-style adjacency + perpendicular overlap + wrapping + recency tiebreaker.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Focus(direction string) {
	w.assertOwner("Focus")
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	panes := w.Panes()
	if len(panes) <= 1 {
		return
	}

	if direction == "next" {
		for i, p := range panes {
			if p.ID == w.ActivePane.ID {
				w.setActive(panes[(i+1)%len(panes)])
				return
			}
		}
		return
	}

	activeCell := w.Root.FindPane(w.ActivePane.ID)
	if activeCell == nil {
		return
	}

	// Try adjacent panes, then wrap to opposite edge.
	best := w.findDirectional(activeCell, direction, false)
	if best == nil {
		best = w.findDirectional(activeCell, direction, true)
	}

	if best != nil {
		w.setActive(best.Pane)
	}
}

// findDirectional finds the best pane in the given direction from activeCell.
// If wrap is true, searches from the opposite window edge instead.
//
// The algorithm checks two things for each candidate pane:
//   - Adjacency: the candidate's edge touches the active pane's edge (with 1-cell border)
//   - Perpendicular overlap: the candidate shares some range along the other axis
//
// Among matching candidates, the most recently active pane wins (recency tiebreaker).
func (w *Window) findDirectional(activeCell *LayoutCell, direction string, wrap bool) *LayoutCell {
	// vertical means we're moving along the Y axis (up/down).
	// checkNear means adjacency is checked against the candidate's near edge
	// (Y for down, X for right) rather than its far edge (Y+H for up, X+W for left).
	vertical := direction == "up" || direction == "down"
	checkNear := direction == "down" || direction == "right"

	// Compute the edge that candidates must be adjacent to, and the
	// perpendicular range they must overlap with.
	var edge, rangeStart, rangeEnd int
	switch direction {
	case "up":
		edge = activeCell.Y
		if wrap {
			edge = w.Height + 1
		}
	case "down":
		edge = activeCell.Y + activeCell.H + 1
		if wrap {
			edge = 0
		}
	case "left":
		edge = activeCell.X
		if wrap {
			edge = w.Width + 1
		}
	case "right":
		edge = activeCell.X + activeCell.W + 1
		if wrap {
			edge = 0
		}
	}
	if vertical {
		rangeStart = activeCell.X
		rangeEnd = activeCell.X + activeCell.W
	} else {
		rangeStart = activeCell.Y
		rangeEnd = activeCell.Y + activeCell.H
	}

	var best *LayoutCell
	var bestActivePoint uint64

	w.Root.Walk(func(cell *LayoutCell) {
		if cell.Pane == nil || cell.Pane.ID == w.ActivePane.ID {
			return
		}

		// Check adjacency: candidate's edge must be exactly at our edge.
		var candEdge, candStart, candEnd int
		if vertical {
			candStart = cell.X
			candEnd = cell.X + cell.W
			if checkNear {
				candEdge = cell.Y
			} else {
				candEdge = cell.Y + cell.H + 1
			}
		} else {
			candStart = cell.Y
			candEnd = cell.Y + cell.H
			if checkNear {
				candEdge = cell.X
			} else {
				candEdge = cell.X + cell.W + 1
			}
		}
		if candEdge != edge {
			return
		}

		// Check perpendicular overlap (half-open interval intersection).
		if candStart >= rangeEnd || candEnd <= rangeStart {
			return
		}

		// Tiebreaker: most recently active pane wins.
		if best == nil || cell.Pane.ActivePoint > bestActivePoint {
			best = cell
			bestActivePoint = cell.Pane.ActivePoint
		}
	})

	return best
}

// PaneContentHeight returns the PTY height for a pane in a layout cell,
// accounting for the per-pane status line.
func PaneContentHeight(cellH int) int {
	h := cellH - StatusLineRows
	if h < 1 {
		h = 1
	}
	return h
}

// ResizeBorder moves a border at position (x, y) by delta cells.
// For vertical borders (vertical split), delta is applied horizontally.
// For horizontal borders (horizontal split), delta is applied vertically.
// Returns true if a resize was performed.
func (w *Window) ResizeBorder(x, y, delta int) bool {
	w.assertOwner("ResizeBorder")
	hit := w.Root.FindBorderNear(x, y)
	if hit == nil || delta == 0 {
		return false
	}

	var leftSize, rightSize *int
	if hit.Dir == SplitVertical {
		leftSize = &hit.Left.W
		rightSize = &hit.Right.W
	} else {
		leftSize = &hit.Left.H
		rightSize = &hit.Right.H
	}

	// Clamp delta so neither side goes below PaneMinSize
	if delta > 0 && *rightSize-delta < PaneMinSize {
		delta = *rightSize - PaneMinSize
	}
	if delta < 0 && *leftSize+delta < PaneMinSize {
		delta = -(*leftSize - PaneMinSize)
	}
	if delta == 0 {
		return false
	}

	*leftSize += delta
	*rightSize -= delta

	// Propagate size changes to subtrees
	if !hit.Left.IsLeaf() {
		hit.Left.ResizeSubtree(hit.Left.W, hit.Left.H)
	}
	if !hit.Right.IsLeaf() {
		hit.Right.ResizeSubtree(hit.Right.W, hit.Right.H)
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	return true
}

// ResizeActive moves the nearest border in the given direction by delta cells,
// following tmux's resize-pane semantics. The direction specifies which way the
// border moves, not which way the pane grows.
// direction is "left", "right", "up", or "down".
// Returns true if a resize was performed.
func (w *Window) ResizeActive(direction string, delta int) bool {
	w.assertOwner("ResizeActive")
	if w.ActivePane == nil {
		return false
	}
	return w.ResizePane(w.ActivePane.ID, direction, delta)
}

// ResizePane resizes a specific pane by moving its nearest border in the given direction.
// direction is "left", "right", "up", or "down". delta is the number of cells to move.
// Returns true if a resize was performed.
func (w *Window) ResizePane(paneID uint32, direction string, delta int) bool {
	w.assertOwner("ResizePane")
	if delta <= 0 {
		return false
	}
	if w.IsLeadPane(paneID) {
		return false
	}
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	// Map direction to split axis and change sign.
	// Positive change grows the left/top sibling (border moves right/down).
	// Negative change shrinks it (border moves left/up).
	var axis SplitDir
	var change int
	switch direction {
	case "left":
		axis, change = SplitVertical, -delta
	case "right":
		axis, change = SplitVertical, delta
	case "up":
		axis, change = SplitHorizontal, -delta
	case "down":
		axis, change = SplitHorizontal, delta
	default:
		return false
	}

	leaf := w.Root.FindPane(paneID)
	if leaf == nil {
		return false
	}

	// Walk up the tree to find the nearest ancestor with matching axis.
	cell := leaf
	for cell.Parent != nil {
		if cell.Parent.Dir == axis {
			idx := cell.IndexInParent()
			siblings := cell.Parent.Children
			if len(siblings) < 2 {
				return false
			}

			// tmux convention: resize the border adjacent to this cell.
			// If we're the last child, use the border to our left (idx-1, idx).
			// Otherwise, use the border to our right (idx, idx+1).
			if idx == len(siblings)-1 {
				idx--
			}
			if idx < 0 || idx+1 >= len(siblings) {
				return false
			}

			var moved int
			if change > 0 {
				moved = w.resizePaneGrow(siblings, idx, axis, change)
			} else {
				moved = w.resizePaneShrink(siblings, idx, axis, -change)
			}
			if moved == 0 {
				return false
			}

			w.Root.FixOffsets()
			w.resizePTYs()
			return true
		}
		cell = cell.Parent
	}

	return false
}

func (w *Window) resizePaneGrow(siblings []*LayoutCell, idx int, axis SplitDir, needed int) int {
	grower := siblings[idx]

	// Match tmux layout_resize_pane_grow: walk tail-ward first, then fall back
	// to the head if no right/bottom sibling can donate enough space.
	remaining := w.transferSiblingRange(grower, siblings, axis, needed, idx+1, len(siblings), 1)
	if remaining == 0 {
		return needed
	}
	remaining = w.transferSiblingRange(grower, siblings, axis, remaining, idx-1, -1, -1)
	return needed - remaining
}

func (w *Window) resizePaneShrink(siblings []*LayoutCell, idx int, axis SplitDir, needed int) int {
	// Match tmux layout_resize_pane_shrink: grow the sibling across the border
	// and walk left/up from the border cell looking for donors.
	return needed - w.transferSiblingRange(siblings[idx+1], siblings, axis, needed, idx, -1, -1)
}

func (w *Window) transferSiblingRange(grower *LayoutCell, siblings []*LayoutCell, axis SplitDir, remaining, start, stop, step int) int {
	for donorIdx := start; donorIdx != stop && remaining > 0; donorIdx += step {
		remaining -= transferAxisSize(grower, siblings[donorIdx], axis, remaining)
	}
	return remaining
}

func transferAxisSize(grower, donor *LayoutCell, axis SplitDir, needed int) int {
	available := donor.resizeCheck(axis)
	if available == 0 {
		return 0
	}
	if available > needed {
		available = needed
	}

	grower.resizeToAxis(axis, grower.axisSize(axis)+available)
	donor.resizeToAxis(axis, donor.axisSize(axis)-available)
	return available
}

// resizePTYs resizes all pane PTYs to match their layout cell dimensions.
func (w *Window) resizePTYs() {
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			c.Pane.Resize(c.W, PaneContentHeight(c.H))
		}
	})
}

func (w *Window) restoreZoomedPaneSize() {
	if w.ZoomedPaneID == 0 {
		return
	}
	cell := w.Root.FindPane(w.ZoomedPaneID)
	if cell != nil && cell.Pane != nil {
		cell.Pane.Resize(w.Width, PaneContentHeight(w.Height))
	}
}

// PaneCount returns the number of panes in the window's layout tree.
func (w *Window) PaneCount() int {
	count := 0
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			count++
		}
	})
	return count
}

// Panes returns all panes in the window (depth-first order).
func (w *Window) Panes() []*Pane {
	var panes []*Pane
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			panes = append(panes, c.Pane)
		}
	})
	return panes
}

// ResolvePane finds a pane by exact name or numeric ID string.
func (w *Window) ResolvePane(ref string) (*Pane, error) {
	panes := w.Panes()
	candidates := make([]PaneRefCandidate, 0, len(panes))
	byID := make(map[uint32]*Pane, len(panes))
	for _, pane := range panes {
		candidates = append(candidates, PaneRefCandidate{ID: pane.ID, Name: pane.Meta.Name})
		byID[pane.ID] = pane
	}

	paneID, err := ResolvePaneRef(ref, candidates)
	if err != nil {
		return nil, err
	}
	return byID[paneID], nil
}

func (w *Window) rootChildForPaneID(paneID uint32) (*LayoutCell, int, error) {
	if w.IsLeadPane(paneID) {
		return nil, -1, fmt.Errorf("cannot operate on lead pane")
	}

	root := w.logicalRoot()
	if root == nil || root.IsLeaf() {
		return nil, -1, fmt.Errorf("window has no root-level split")
	}

	leaf := root.FindPane(paneID)
	if leaf == nil {
		return nil, -1, fmt.Errorf("pane %d not found", paneID)
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
	if w.Root.FindPane(paneID) == nil {
		return 0, fmt.Errorf("pane %d not found", paneID)
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
	if w.Root.FindPane(paneID) == nil {
		return nil, fmt.Errorf("pane %d not found", paneID)
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

func reorderLayoutChildren(children []*LayoutCell, fromIdx, targetIdx int, before bool) []*LayoutCell {
	if fromIdx == targetIdx {
		return children
	}

	moving := children[fromIdx]
	children = append(children[:fromIdx], children[fromIdx+1:]...)

	insertIdx := targetIdx
	if !before {
		insertIdx = targetIdx + 1
	}
	if fromIdx < targetIdx {
		insertIdx--
	}

	children = append(children, nil)
	copy(children[insertIdx+1:], children[insertIdx:])
	children[insertIdx] = moving
	return children
}

func (w *Window) splitGroupForPaneID(paneID uint32) (*LayoutCell, int, error) {
	if w.IsLeadPane(paneID) {
		return nil, -1, fmt.Errorf("cannot operate on lead pane")
	}
	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return nil, -1, fmt.Errorf("pane %d not found", paneID)
	}
	if cell.Parent == nil {
		return nil, -1, fmt.Errorf("pane %d is not in a split group", paneID)
	}
	return cell.Parent, cell.IndexInParent(), nil
}

// SwapPanes exchanges the Pane pointers of two layout cells and resizes PTYs
// to match their new cell dimensions.
// Both the Pane struct and its Meta travel together (swap-with-meta semantics).
func (w *Window) SwapPanes(id1, id2 uint32) error {
	w.assertOwner("SwapPanes")
	if id1 == id2 {
		return nil
	}
	if w.IsLeadPane(id1) || w.IsLeadPane(id2) {
		return fmt.Errorf("cannot operate on lead pane")
	}
	cell1 := w.Root.FindPane(id1)
	if cell1 == nil {
		return fmt.Errorf("pane %d not found", id1)
	}
	cell2 := w.Root.FindPane(id2)
	if cell2 == nil {
		return fmt.Errorf("pane %d not found", id2)
	}
	cell1.Pane, cell2.Pane = cell2.Pane, cell1.Pane
	w.resizePTYs()
	return nil
}

// SwapTree swaps the root-level groups containing the given panes.
func (w *Window) SwapTree(id1, id2 uint32) error {
	w.assertOwner("SwapTree")
	_, idx1, err := w.rootChildForPaneID(id1)
	if err != nil {
		return err
	}
	_, idx2, err := w.rootChildForPaneID(id2)
	if err != nil {
		return err
	}
	if idx1 == idx2 {
		return fmt.Errorf("panes %d and %d are in the same root-level group", id1, id2)
	}

	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	root := w.logicalRoot()
	root.Children[idx1], root.Children[idx2] = root.Children[idx2], root.Children[idx1]
	w.finishTreeMutation()
	return nil
}

// MovePane moves the root-level group containing paneID before or after the
// root-level group containing targetPaneID.
func (w *Window) MovePane(paneID, targetPaneID uint32, before bool) error {
	w.assertOwner("MovePane")
	if paneID == targetPaneID {
		return nil
	}

	if w.IsLeadPane(paneID) || w.IsLeadPane(targetPaneID) {
		return fmt.Errorf("cannot operate on lead pane")
	}

	sourceCell := w.Root.FindPane(paneID)
	if sourceCell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}
	targetCell := w.Root.FindPane(targetPaneID)
	if targetCell == nil {
		return fmt.Errorf("pane %d not found", targetPaneID)
	}
	if sourceCell.Parent != nil && sourceCell.Parent == targetCell.Parent {
		if w.ZoomedPaneID != 0 {
			w.Unzoom()
		}
		parent := sourceCell.Parent
		parent.Children = reorderLayoutChildren(parent.Children, sourceCell.IndexInParent(), targetCell.IndexInParent(), before)
		w.finishTreeMutation()
		return nil
	}

	_, fromIdx, err := w.rootChildForPaneID(paneID)
	if err != nil {
		return err
	}
	_, targetIdx, err := w.rootChildForPaneID(targetPaneID)
	if err != nil {
		return err
	}
	if fromIdx == targetIdx {
		return fmt.Errorf("panes %d and %d are in the same root-level group", paneID, targetPaneID)
	}

	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	root := w.logicalRoot()
	root.Children = reorderLayoutChildren(root.Children, fromIdx, targetIdx, before)

	w.finishTreeMutation()
	return nil
}

// MovePaneUp reorders paneID one slot earlier within its direct split group.
func (w *Window) MovePaneUp(paneID uint32) error {
	w.assertOwner("MovePaneUp")
	return w.movePaneWithinSplitGroup(paneID, -1)
}

// MovePaneDown reorders paneID one slot later within its direct split group.
func (w *Window) MovePaneDown(paneID uint32) error {
	w.assertOwner("MovePaneDown")
	return w.movePaneWithinSplitGroup(paneID, 1)
}

func (w *Window) movePaneWithinSplitGroup(paneID uint32, delta int) error {
	parent, idx, err := w.splitGroupForPaneID(paneID)
	if err != nil {
		return err
	}
	targetIdx := idx + delta
	switch {
	case delta < 0 && idx == 0:
		return fmt.Errorf("pane %d is already first in its split group", paneID)
	case delta > 0 && idx == len(parent.Children)-1:
		return fmt.Errorf("pane %d is already last in its split group", paneID)
	}
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	parent.Children = reorderLayoutChildren(parent.Children, idx, targetIdx, delta < 0)
	w.finishTreeMutation()
	return nil
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

	sourceCell := w.Root.FindPane(paneID)
	if sourceCell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}
	if w.Root.FindPane(targetPaneID) == nil {
		return fmt.Errorf("pane %d not found", targetPaneID)
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
		w.Unzoom()
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

// SwapPaneForward swaps the active pane with the next pane in walk order.
func (w *Window) SwapPaneForward() error {
	w.assertOwner("SwapPaneForward")
	if w.ActivePane != nil && w.IsLeadPane(w.ActivePane.ID) {
		return fmt.Errorf("cannot operate on lead pane")
	}
	cells := w.paneLeavesIn(w.logicalRoot())
	if len(cells) <= 1 {
		return nil
	}
	idx := w.activeCellIndex(cells)
	if idx < 0 {
		return fmt.Errorf("active pane not found in layout")
	}
	next := (idx + 1) % len(cells)
	return w.SwapPanes(cells[idx].Pane.ID, cells[next].Pane.ID)
}

// SwapPaneBackward swaps the active pane with the previous pane in walk order.
func (w *Window) SwapPaneBackward() error {
	w.assertOwner("SwapPaneBackward")
	if w.ActivePane != nil && w.IsLeadPane(w.ActivePane.ID) {
		return fmt.Errorf("cannot operate on lead pane")
	}
	cells := w.paneLeavesIn(w.logicalRoot())
	if len(cells) <= 1 {
		return nil
	}
	idx := w.activeCellIndex(cells)
	if idx < 0 {
		return fmt.Errorf("active pane not found in layout")
	}
	prev := (idx - 1 + len(cells)) % len(cells)
	return w.SwapPanes(cells[idx].Pane.ID, cells[prev].Pane.ID)
}

// RotatePanes cycles all pane positions and resizes PTYs to match.
// If forward is true, panes advance one position in walk order: each cell
// gets the pane from the previous cell, with the last pane wrapping to the
// first cell.
func (w *Window) RotatePanes(forward bool) error {
	w.assertOwner("RotatePanes")
	cells := w.paneLeavesIn(w.logicalRoot())
	if len(cells) <= 1 {
		return nil
	}
	if forward {
		last := cells[len(cells)-1].Pane
		for i := len(cells) - 1; i > 0; i-- {
			cells[i].Pane = cells[i-1].Pane
		}
		cells[0].Pane = last
	} else {
		first := cells[0].Pane
		for i := 0; i < len(cells)-1; i++ {
			cells[i].Pane = cells[i+1].Pane
		}
		cells[len(cells)-1].Pane = first
	}
	w.resizePTYs()
	return nil
}

// paneLeaves returns all leaf cells containing panes in depth-first order.
func (w *Window) paneLeaves() []*LayoutCell {
	return w.paneLeavesIn(w.Root)
}

func (w *Window) paneLeavesIn(root *LayoutCell) []*LayoutCell {
	var cells []*LayoutCell
	if root == nil {
		return cells
	}
	root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			cells = append(cells, c)
		}
	})
	return cells
}

// activeCellIndex returns the index of the active pane's cell in the given
// leaf cell slice, or -1 if not found.
func (w *Window) activeCellIndex(cells []*LayoutCell) int {
	for i, c := range cells {
		if c.Pane.ID == w.ActivePane.ID {
			return i
		}
	}
	return -1
}

// Zoom toggles a pane to fill the entire window. The layout tree is kept
// intact; the ZoomedPaneID field tells the client to render only this pane.
// The zoomed pane's PTY is resized to the full window dimensions.
func (w *Window) Zoom(paneID uint32) error {
	w.assertOwner("Zoom")
	if w.ZoomedPaneID == paneID {
		return w.Unzoom()
	}
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}

	// Cannot zoom if only one pane
	count := 0
	w.Root.Walk(func(_ *LayoutCell) { count++ })
	if count <= 1 {
		return fmt.Errorf("cannot zoom with only one pane")
	}

	w.ZoomedPaneID = paneID
	w.setActive(cell.Pane)

	// Resize zoomed pane PTY to full window
	cell.Pane.Resize(w.Width, PaneContentHeight(w.Height))

	return nil
}

// Unzoom restores the normal multi-pane view. The zoomed pane's PTY is
// resized back to match its layout cell.
func (w *Window) Unzoom() error {
	w.assertOwner("Unzoom")
	if w.ZoomedPaneID == 0 {
		return fmt.Errorf("no pane is zoomed")
	}

	paneID := w.ZoomedPaneID
	w.ZoomedPaneID = 0

	// Resize the previously-zoomed pane back to its cell size
	cell := w.Root.FindPane(paneID)
	if cell != nil && cell.Pane != nil {
		cell.Pane.Resize(cell.W, PaneContentHeight(cell.H))
	}

	return nil
}

// SplicePane replaces a leaf pane (by ID) with one or more proxy panes.
// If panes has 1 entry, it's a simple 1:1 replacement. If panes has 2+
// entries, the leaf is converted to a vertical split containing the
// new panes. The original cell's dimensions are preserved.
// Returns the list of newly created layout cells.
func (w *Window) SplicePane(oldPaneID uint32, newPanes []*Pane) ([]*LayoutCell, error) {
	w.assertOwner("SplicePane")
	if len(newPanes) == 0 {
		return nil, fmt.Errorf("no panes to splice")
	}

	cell := w.Root.FindPane(oldPaneID)
	if cell == nil {
		return nil, fmt.Errorf("pane %d not found in layout", oldPaneID)
	}

	// Auto-unzoom if the spliced pane was zoomed
	if w.ZoomedPaneID == oldPaneID {
		w.ZoomedPaneID = 0
	}

	// Single pane: simple replacement
	if len(newPanes) == 1 {
		cell.Pane = newPanes[0]
		newPanes[0].Resize(cell.W, PaneContentHeight(cell.H))
		if w.ActivePane != nil && w.ActivePane.ID == oldPaneID {
			w.setActive(newPanes[0])
		}
		return []*LayoutCell{cell}, nil
	}

	// Multiple panes: convert the leaf into a vertical split
	x, y, totalW, h := cell.X, cell.Y, cell.W, cell.H
	n := len(newPanes)
	seps := n - 1

	// Calculate width per pane
	available := totalW - seps
	if available < n*PaneMinSize {
		return nil, fmt.Errorf("not enough space to splice %d panes into %d cols", n, totalW)
	}

	cell.isLeaf = false
	cell.Pane = nil
	cell.Dir = SplitVertical
	cell.Children = make([]*LayoutCell, n)

	each := available / n
	xoff := x
	for i, pane := range newPanes {
		childW := each
		if i == n-1 {
			childW = available - each*(n-1) // remainder to last
		}
		leaf := NewLeaf(pane, xoff, y, childW, h)
		leaf.Parent = cell
		cell.Children[i] = leaf

		pane.Resize(childW, PaneContentHeight(h))
		xoff += childW + 1 // +1 for separator
	}

	// Update active pane if the replaced pane was active
	if w.ActivePane != nil && w.ActivePane.ID == oldPaneID {
		w.setActive(newPanes[0])
	}

	w.Root.FixOffsets()

	return cell.Children, nil
}

// UnsplicePane replaces all children of a spliced cell (that contains
// proxy panes for a specific host) with a single pane. Used to revert
// a takeover and restore the original SSH pane.
func (w *Window) UnsplicePane(hostName string, replacement *Pane) error {
	w.assertOwner("UnsplicePane")
	allProxyLeavesForHost := func(cell *LayoutCell) bool {
		if cell == nil {
			return false
		}
		hasLeaf := false
		ok := true
		cell.Walk(func(c *LayoutCell) {
			if !ok || c == nil || !c.IsLeaf() || c.Pane == nil {
				return
			}
			hasLeaf = true
			if !c.Pane.IsProxy() || c.Pane.Meta.Host != hostName {
				ok = false
			}
		})
		return hasLeaf && ok
	}

	// Find either:
	// - a full spliced parent containing only proxy panes for this host, or
	// - a single injected proxy leaf for this host
	var targetCell *LayoutCell
	w.Root.Walk(func(c *LayoutCell) {
		if targetCell != nil || c == nil || c.Pane == nil || !c.Pane.IsProxy() || c.Pane.Meta.Host != hostName {
			return
		}
		if c.Parent != nil && !c.Parent.IsLeaf() && allProxyLeavesForHost(c.Parent) {
			targetCell = c.Parent
			return
		}
		targetCell = c
	})
	if targetCell == nil {
		return fmt.Errorf("no spliced panes found for host %q", hostName)
	}

	// Convert back to a leaf with the replacement pane.
	if targetCell.IsLeaf() {
		targetCell.Pane = replacement
	} else {
		targetCell.isLeaf = true
		targetCell.Dir = -1
		targetCell.Pane = replacement
		targetCell.Children = nil
	}

	replacement.Resize(targetCell.W, PaneContentHeight(targetCell.H))
	if w.ActivePane == nil || w.ActivePane.Meta.Host == hostName || w.Root.FindPane(w.ActivePane.ID) == nil {
		w.setActive(replacement)
	} else if targetCell.Parent == nil {
		// If the root cell was collapsed back to a leaf, keep the current active
		// pane only when it still exists in the new layout.
		if w.Root.FindPane(w.ActivePane.ID) == nil {
			w.setActive(replacement)
		}
	}
	w.Root.FixOffsets()
	w.resizePTYs()

	return nil
}
