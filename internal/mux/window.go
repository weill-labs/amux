package mux

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// StatusLineRows is the number of rows reserved for the per-pane status line.
const StatusLineRows = 1

// DefaultRestoreHeight is the fallback pane height when restoring a minimized
// pane that has no saved height.
const DefaultRestoreHeight = 12

// Window holds the layout tree and active pane for one window.
type Window struct {
	ID           uint32
	Name         string
	Root         *LayoutCell
	ActivePane   *Pane
	Width        int
	Height       int
	ZoomedPaneID uint32 // non-zero when a pane is zoomed to full window
	minimizeSeq  uint64 // monotonic counter for LIFO minimize ordering
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

// SplitRoot splits the entire window at the root level.
// If the root already has the same split direction, the new pane is added
// as a sibling (equal distribution). Otherwise, wraps the root in a new parent.
// Auto-unzooms if a pane is zoomed.
func (w *Window) SplitRoot(dir SplitDir, newPane *Pane) (*Pane, error) {
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	newLeaf := NewLeaf(newPane, 0, 0, 0, 0)

	if !w.Root.IsLeaf() && w.Root.Dir == dir {
		// Same direction: add as sibling, redistribute equally
		newLeaf.Parent = w.Root
		w.Root.Children = append(w.Root.Children, newLeaf)
		// Give all children equal sizes so ResizeAll distributes fairly
		n := len(w.Root.Children)
		seps := n - 1
		if dir == SplitHorizontal {
			each := (w.Width - seps) / n
			for _, child := range w.Root.Children {
				child.ResizeAll(each, w.Height)
			}
			// Give remainder to last child
			w.Root.Children[n-1].ResizeAll(w.Width-seps-each*(n-1), w.Height)
		} else {
			each := (w.Height - seps) / n
			for _, child := range w.Root.Children {
				child.ResizeAll(w.Width, each)
			}
			w.Root.Children[n-1].ResizeAll(w.Width, w.Height-seps-each*(n-1))
		}
	} else {
		// Different direction or root is a leaf: wrap
		oldRoot := w.Root

		if dir == SplitHorizontal {
			size2 := (oldRoot.W - 1) / 2
			size1 := oldRoot.W - 1 - size2
			newLeaf.W = size2
			newLeaf.H = oldRoot.H
			oldRoot.ResizeAll(size1, oldRoot.H)
		} else {
			size2 := (oldRoot.H - 1) / 2
			size1 := oldRoot.H - 1 - size2
			newLeaf.W = oldRoot.W
			newLeaf.H = size2
			oldRoot.ResizeAll(oldRoot.W, size1)
		}

		newRoot := &LayoutCell{
			X: 0, Y: 0, W: w.Width, H: w.Height,
			Dir:      dir,
			Children: []*LayoutCell{oldRoot, newLeaf},
		}
		oldRoot.Parent = newRoot
		newLeaf.Parent = newRoot
		w.Root = newRoot
	}

	w.Root.FixOffsets()

	w.resizePTYs()

	w.setActive(newPane)
	return newPane, nil
}

// Split splits the active pane in the given direction, creating a new pane
// via the provided factory function. Returns the new pane.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Split(dir SplitDir, newPane *Pane) (*Pane, error) {
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	cell := w.Root.FindPane(w.ActivePane.ID)
	if cell == nil {
		return nil, fmt.Errorf("active pane %d not found in layout", w.ActivePane.ID)
	}

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
	if existingCell != nil {
		w.ActivePane.Resize(existingCell.W, PaneContentHeight(existingCell.H))
	}

	w.Root.FixOffsets()
	w.setActive(newPane)

	return newPane, nil
}

// ClosePane removes a pane from the layout and reclaims its space.
// If the closed pane was zoomed, zoom is automatically cleared.
func (w *Window) ClosePane(paneID uint32) error {
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

// Resize adjusts the layout to fit new terminal dimensions.
func (w *Window) Resize(width, height int) {
	w.Width = width
	w.Height = height
	w.Root.ResizeAll(width, height)

	w.resizePTYs()

	// If a pane is zoomed, its PTY should match the full window, not its cell
	if w.ZoomedPaneID != 0 {
		cell := w.Root.FindPane(w.ZoomedPaneID)
		if cell != nil && cell.Pane != nil {
			cell.Pane.Resize(width, PaneContentHeight(height))
		}
	}
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
func (w *Window) FocusPane(p *Pane) {
	w.setActive(p)
}

// Focus changes the active pane. Direction is "next", "left", "right", "up", "down".
// Uses tmux-style adjacency + perpendicular overlap + wrapping + recency tiebreaker.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Focus(direction string) {
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

// forceResizeChildren propagates a parent's dimensions to its children.
// Close() updates the parent's W/H but children retain old sizes.
func forceResizeChildren(cell *LayoutCell) {
	if cell.IsLeaf() {
		return
	}
	targetW, targetH := cell.W, cell.H
	childTotal := 0
	for _, child := range cell.Children {
		if cell.Dir == SplitHorizontal {
			childTotal += child.W
		} else {
			childTotal += child.H
		}
	}
	childTotal += len(cell.Children) - 1

	if cell.Dir == SplitHorizontal {
		cell.W = childTotal
	} else {
		cell.H = childTotal
	}
	cell.ResizeAll(targetW, targetH)
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
// For vertical borders (horizontal split), delta is applied horizontally.
// For horizontal borders (vertical split), delta is applied vertically.
// Returns true if a resize was performed.
func (w *Window) ResizeBorder(x, y, delta int) bool {
	hit := w.Root.FindBorderAt(x, y)
	if hit == nil || delta == 0 {
		return false
	}

	var leftSize, rightSize *int
	if hit.Dir == SplitHorizontal {
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
		hit.Left.ResizeAll(hit.Left.W, hit.Left.H)
	}
	if !hit.Right.IsLeaf() {
		hit.Right.ResizeAll(hit.Right.W, hit.Right.H)
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
	if w.ActivePane == nil || delta <= 0 {
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
		axis, change = SplitHorizontal, -delta
	case "right":
		axis, change = SplitHorizontal, delta
	case "up":
		axis, change = SplitVertical, -delta
	case "down":
		axis, change = SplitVertical, delta
	default:
		return false
	}

	// Find the active pane's leaf cell
	leaf := w.Root.FindPane(w.ActivePane.ID)
	if leaf == nil {
		return false
	}

	// Walk up the tree to find the nearest ancestor with matching axis.
	cell := leaf
	for cell.Parent != nil {
		if cell.Parent.Dir == axis {
			idx := cell.indexInParent()
			siblings := cell.Parent.Children

			// tmux convention: resize the border adjacent to this cell.
			// If we're the last child, use the border to our left (idx-1, idx).
			// Otherwise, use the border to our right (idx, idx+1).
			var left, right *LayoutCell
			if idx == len(siblings)-1 {
				left, right = siblings[idx-1], siblings[idx]
			} else {
				left, right = siblings[idx], siblings[idx+1]
			}

			if change > 0 {
				return w.resizeBetween(left, right, axis, change)
			}
			return w.resizeBetween(right, left, axis, -change)
		}
		cell = cell.Parent
	}

	return false
}

// resizeBetween transfers delta cells from donor to grower along the given axis.
func (w *Window) resizeBetween(grower, donor *LayoutCell, axis SplitDir, delta int) bool {
	var growerSize, donorSize *int
	if axis == SplitHorizontal {
		growerSize = &grower.W
		donorSize = &donor.W
	} else {
		growerSize = &grower.H
		donorSize = &donor.H
	}

	// Clamp so donor doesn't go below minimum
	if *donorSize-delta < PaneMinSize {
		delta = *donorSize - PaneMinSize
	}
	if delta <= 0 {
		return false
	}

	*growerSize += delta
	*donorSize -= delta

	// Propagate size changes to subtrees
	if !grower.IsLeaf() {
		grower.ResizeAll(grower.W, grower.H)
	}
	if !donor.IsLeaf() {
		donor.ResizeAll(donor.W, donor.H)
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	return true
}

// resizePTYs resizes all pane PTYs to match their layout cell dimensions.
// Minimized panes are skipped — their PTYs stay at pre-minimize dimensions.
func (w *Window) resizePTYs() {
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil && !c.Pane.Meta.Minimized {
			c.Pane.Resize(c.W, PaneContentHeight(c.H))
		}
	})
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

// ResolvePane finds a pane by name or numeric ID string.
func (w *Window) ResolvePane(ref string) *Pane {
	for _, p := range w.Panes() {
		if p.Meta.Name == ref || fmt.Sprintf("%d", p.ID) == ref {
			return p
		}
	}
	for _, p := range w.Panes() {
		if strings.HasPrefix(p.Meta.Name, ref) {
			return p
		}
	}
	return nil
}

// SwapPanes exchanges the Pane pointers of two layout cells and resizes PTYs
// to match their new cell dimensions.
// Both the Pane struct and its Meta travel together (swap-with-meta semantics).
func (w *Window) SwapPanes(id1, id2 uint32) error {
	if id1 == id2 {
		return nil
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

// SwapPaneForward swaps the active pane with the next pane in walk order.
func (w *Window) SwapPaneForward() error {
	cells := w.paneLeaves()
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
	cells := w.paneLeaves()
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
func (w *Window) RotatePanes(forward bool) {
	cells := w.paneLeaves()
	if len(cells) <= 1 {
		return
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
}

// paneLeaves returns all non-minimized leaf cells containing panes
// (depth-first order). Minimized panes are excluded because their cell
// height doesn't match normal panes — swapping would produce inconsistent state.
func (w *Window) paneLeaves() []*LayoutCell {
	var cells []*LayoutCell
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil && !c.Pane.Meta.Minimized {
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

// Minimize shrinks a pane's layout cell to just the status line (header only).
// Only allowed in vertical splits with at least one non-minimized sibling.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Minimize(paneID uint32) error {
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}
	if cell.Pane.Meta.Minimized {
		return fmt.Errorf("pane already minimized")
	}

	// Only allow minimize in vertical splits (stacked panes).
	// A pane at root or in a horizontal split has no vertical sibling
	// to absorb the reclaimed height.
	if cell.Parent == nil || cell.Parent.Dir != SplitVertical {
		return fmt.Errorf("cannot minimize: pane is not in a vertical split")
	}

	// Require at least one non-minimized sibling to remain visible.
	nonMinSibs := 0
	for _, sib := range cell.Parent.Children {
		if sib == cell {
			continue
		}
		if !sib.IsLeaf() {
			nonMinSibs++ // subtrees always count as non-minimized
		} else if sib.Pane != nil && !sib.Pane.Meta.Minimized {
			nonMinSibs++
		}
	}
	if nonMinSibs == 0 {
		return fmt.Errorf("cannot minimize the last visible pane in this group")
	}

	cell.Pane.Meta.Minimized = true
	cell.Pane.Meta.RestoreH = cell.H
	w.minimizeSeq++
	cell.Pane.Meta.MinimizedSeq = w.minimizeSeq

	cell.H = StatusLineRows
	// Don't resize the PTY — TUI apps (Claude Code, vim, etc.) may not
	// recover properly from being resized to 1 row. The PTY and emulator
	// stay at their original dimensions; only the layout cell shrinks.

	if cell.Parent != nil {
		reclaimed := cell.Pane.Meta.RestoreH - cell.H
		if reclaimed > 0 {
			for _, sib := range cell.Parent.Children {
				if sib != cell && !sib.IsLeaf() || (sib.IsLeaf() && sib.Pane != nil && !sib.Pane.Meta.Minimized) {
					if cell.Parent.Dir == SplitVertical {
						sib.H += reclaimed
					}
					if !sib.IsLeaf() {
						sib.ResizeAll(sib.W, sib.H)
					} else if sib.Pane != nil {
						sib.Pane.Resize(sib.W, PaneContentHeight(sib.H))
					}
					break
				}
			}
		}
	}

	w.Root.FixOffsets()
	return nil
}

// Zoom toggles a pane to fill the entire window. The layout tree is kept
// intact; the ZoomedPaneID field tells the client to render only this pane.
// The zoomed pane's PTY is resized to the full window dimensions.
func (w *Window) Zoom(paneID uint32) error {
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

// Restore expands a minimized pane back to its saved height.
func (w *Window) Restore(paneID uint32) error {
	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}
	if !cell.Pane.Meta.Minimized {
		return fmt.Errorf("pane is not minimized")
	}

	savedH := cell.Pane.Meta.RestoreH
	if savedH <= 0 {
		savedH = DefaultRestoreHeight
	}

	if cell.Parent != nil {
		needed := savedH - cell.H
		for _, sib := range cell.Parent.Children {
			if sib != cell {
				if cell.Parent.Dir == SplitVertical && sib.H-needed >= PaneMinSize+StatusLineRows {
					sib.H -= needed
					if !sib.IsLeaf() {
						sib.ResizeAll(sib.W, sib.H)
					} else if sib.Pane != nil {
						sib.Pane.Resize(sib.W, PaneContentHeight(sib.H))
					}
					break
				}
			}
		}
	}

	cell.H = savedH
	cell.Pane.Meta.Minimized = false
	cell.Pane.Meta.RestoreH = 0
	cell.Pane.Meta.MinimizedSeq = 0
	cell.Pane.Resize(cell.W, PaneContentHeight(cell.H))

	w.Root.FixOffsets()
	return nil
}

// ToggleMinimize toggles the active pane's minimized state.
// Returns the affected pane's name and whether it was minimized (true) or restored (false).
func (w *Window) ToggleMinimize() (name string, minimized bool, err error) {
	if w.ActivePane == nil {
		return "", false, fmt.Errorf("no active pane")
	}

	name = w.ActivePane.Meta.Name
	if w.ActivePane.Meta.Minimized {
		err = w.Restore(w.ActivePane.ID)
		return name, false, err
	}
	err = w.Minimize(w.ActivePane.ID)
	return name, true, err
}

// recoverMinimizeSeq recomputes minimizeSeq from existing pane MinimizedSeq
// values after a checkpoint restore or hot-reload.
func (w *Window) recoverMinimizeSeq() {
	var maxSeq uint64
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil && c.Pane.Meta.MinimizedSeq > maxSeq {
			maxSeq = c.Pane.Meta.MinimizedSeq
		}
	})
	w.minimizeSeq = maxSeq
}

// SplicePane replaces a leaf pane (by ID) with one or more proxy panes.
// If panes has 1 entry, it's a simple 1:1 replacement. If panes has 2+
// entries, the leaf is converted to a horizontal split containing the
// new panes. The original cell's dimensions are preserved.
// Returns the list of newly created layout cells.
func (w *Window) SplicePane(oldPaneID uint32, newPanes []*Pane) ([]*LayoutCell, error) {
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

	// Multiple panes: convert the leaf into a horizontal split
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
	cell.Dir = SplitHorizontal
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
	// Find a cell containing proxy panes for this host
	var targetCell *LayoutCell
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil && c.Pane.Meta.Host == hostName && c.Pane.IsProxy() {
			if c.Parent != nil && !c.Parent.IsLeaf() {
				targetCell = c.Parent
			}
		}
	})
	if targetCell == nil {
		return fmt.Errorf("no spliced panes found for host %q", hostName)
	}

	// Convert back to a leaf with the replacement pane
	targetCell.isLeaf = true
	targetCell.Dir = -1
	targetCell.Pane = replacement
	targetCell.Children = nil

	replacement.Resize(targetCell.W, PaneContentHeight(targetCell.H))
	w.setActive(replacement)
	w.Root.FixOffsets()
	w.resizePTYs()

	return nil
}
