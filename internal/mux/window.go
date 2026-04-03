package mux

import (
	"fmt"

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
	LeadPaneID   uint32 // non-zero when a pane is designated lead; multi-pane windows anchor it as a full-height left column
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
	if w.hasPendingLead() {
		// First split on a single-pane lead window always anchors the lead on the
		// left regardless of the requested split direction.
		return w.materializePendingLead(newPane, opts)
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
	if w.hasPendingLead() && targetPaneID == w.LeadPaneID {
		return w.materializePendingLead(newPane, opts)
	}
	if w.IsLeadPane(targetPaneID) {
		return nil, fmt.Errorf("cannot operate on lead pane")
	}
	cell, err := w.mustFindPane(targetPaneID)
	if err != nil {
		return nil, err
	}
	return w.splitCellWithOptions(cell, dir, newPane, opts)
}

func (w *Window) splitCellWithOptions(cell *LayoutCell, dir SplitDir, newPane *Pane, opts SplitOptions) (*Pane, error) {
	newCell, err := cell.splitWithOrder(dir, newPane, false)
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

func canSplitCell(cell *LayoutCell, dir SplitDir) bool {
	if cell == nil {
		return false
	}
	available := cell.W
	if dir == SplitHorizontal {
		available = cell.H
	}
	return available >= 2*PaneMinSize+1
}

func splitAvailable(cell *LayoutCell, dir SplitDir) int {
	if cell == nil {
		return 0
	}
	if dir == SplitHorizontal {
		return cell.H
	}
	return cell.W
}

func (w *Window) restoreActivePaneByID(activePaneID uint32, preferred *Pane) {
	if preferred != nil && activePaneID == preferred.ID {
		w.setActive(preferred)
		return
	}
	if activePaneID != 0 && w.Root != nil {
		if cell := w.Root.FindPane(activePaneID); cell != nil && cell.Pane != nil {
			w.setActive(cell.Pane)
			return
		}
	}
	if preferred != nil {
		w.setActive(preferred)
	}
}

// MovePaneIntoSplit reparents paneID into a split of targetPaneID, inserting
// the moved pane before the target for left/top drops and after for right/bottom.
func (w *Window) MovePaneIntoSplit(paneID, targetPaneID uint32, dir SplitDir, insertFirst bool) error {
	w.assertOwner("MovePaneIntoSplit")
	if paneID == targetPaneID {
		return nil
	}
	if w.IsLeadPane(paneID) || w.IsLeadPane(targetPaneID) {
		return fmt.Errorf("cannot operate on lead pane")
	}

	sourceCell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
	}
	targetCell, err := w.mustFindPane(targetPaneID)
	if err != nil {
		return err
	}
	if !canSplitCell(targetCell, dir) {
		return fmt.Errorf("not enough space to split (%d < %d)", splitAvailable(targetCell, dir), 2*PaneMinSize+1)
	}

	sourcePane := sourceCell.Pane
	activePaneID := uint32(0)
	if w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	if err := w.ClosePane(paneID); err != nil {
		return err
	}
	targetCell, err = w.mustFindPane(targetPaneID)
	if err != nil {
		return err
	}

	newCell, err := targetCell.splitWithOrder(dir, sourcePane, insertFirst)
	if err != nil {
		return err
	}
	sourcePane.Resize(newCell.W, PaneContentHeight(newCell.H))

	var existingCell *LayoutCell
	if targetCell.IsLeaf() {
		existingCell = targetCell
	} else if insertFirst && len(targetCell.Children) > 1 {
		existingCell = targetCell.Children[1]
	} else if len(targetCell.Children) > 0 {
		existingCell = targetCell.Children[0]
	}
	if existingCell != nil && existingCell.Pane != nil {
		existingCell.Pane.Resize(existingCell.W, PaneContentHeight(existingCell.H))
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	w.restoreZoomedPaneSize()
	w.restoreActivePaneByID(activePaneID, sourcePane)
	return nil
}

// MovePaneToRootEdge reparents paneID into a new split at the logical root.
func (w *Window) MovePaneToRootEdge(paneID uint32, dir SplitDir, insertFirst bool) error {
	w.assertOwner("MovePaneToRootEdge")
	if w.IsLeadPane(paneID) {
		return fmt.Errorf("cannot operate on lead pane")
	}

	root := w.logicalRoot()
	if !canSplitCell(root, dir) {
		return fmt.Errorf("not enough space to split (%d < %d)", splitAvailable(root, dir), 2*PaneMinSize+1)
	}

	sourceCell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
	}
	sourcePane := sourceCell.Pane
	activePaneID := uint32(0)
	if w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	if err := w.ClosePane(paneID); err != nil {
		return err
	}
	root = w.logicalRoot()
	if root == nil {
		return fmt.Errorf("no layout")
	}
	if _, err := w.splitSubtreeRootWithOptions(root, dir, sourcePane, insertFirst, SplitOptions{}); err != nil {
		return err
	}
	w.restoreActivePaneByID(activePaneID, sourcePane)
	return nil
}

// ClosePane removes a pane from the layout and reclaims its space.
// If the closed pane was zoomed, zoom is automatically cleared.
func (w *Window) ClosePane(paneID uint32) error {
	w.assertOwner("ClosePane")
	cell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
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

func (w *Window) mustFindPane(paneID uint32) (*LayoutCell, error) {
	var cell *LayoutCell
	if w.Root != nil {
		cell = w.Root.FindPane(paneID)
	}
	if cell == nil {
		return nil, fmt.Errorf("pane %d not found in layout", paneID)
	}
	return cell, nil
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
	cell, err := w.mustFindPane(paneID)
	if err != nil {
		return nil, -1, err
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
	cell1, err := w.mustFindPane(id1)
	if err != nil {
		return err
	}
	cell2, err := w.mustFindPane(id2)
	if err != nil {
		return err
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

// MovePane reorders paneID before or after targetPaneID. If both panes share an
// immediate parent split group, it reorders them within that group; otherwise
// it moves the root-level group containing paneID relative to targetPaneID.
func (w *Window) MovePane(paneID, targetPaneID uint32, before bool) error {
	w.assertOwner("MovePane")
	if paneID == targetPaneID {
		return nil
	}

	if w.IsLeadPane(paneID) || w.IsLeadPane(targetPaneID) {
		return fmt.Errorf("cannot operate on lead pane")
	}

	sourceCell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
	}
	targetCell, err := w.mustFindPane(targetPaneID)
	if err != nil {
		return err
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
	// MovePaneUp inserts before the previous sibling; MovePaneDown inserts after
	// the next sibling, so only negative deltas map to reorderLayoutChildren's
	// "before target" mode.
	parent.Children = reorderLayoutChildren(parent.Children, idx, targetIdx, delta < 0)
	w.finishTreeMutation()
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

	cell, err := w.mustFindPane(paneID)
	if err != nil {
		return err
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

// ReplacePane swaps a leaf's pane pointer in place without changing the layout
// geometry or pane ID-based zoom/lead bookkeeping.
func (w *Window) ReplacePane(oldPaneID uint32, replacement *Pane) error {
	w.assertOwner("ReplacePane")
	cell, err := w.mustFindPane(oldPaneID)
	if err != nil {
		return err
	}
	cell.Pane = replacement
	w.finishTreeMutation()
	w.restoreZoomedPaneSize()
	if w.ActivePane != nil && w.ActivePane.ID == oldPaneID {
		w.setActive(replacement)
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

	cell, err := w.mustFindPane(oldPaneID)
	if err != nil {
		return nil, err
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
