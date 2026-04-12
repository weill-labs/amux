package mux

// PaneContentHeight returns the PTY height for a pane in a layout cell,
// accounting for the per-pane status line.
func PaneContentHeight(cellH int) int {
	h := cellH - StatusLineRows
	if h < 1 {
		h = 1
	}
	return h
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

// Equalize rebalances the window layout. When widths is true, vertical splits
// are redistributed evenly, treating an anchored lead as part of the root
// column set. When heights is true, rows within each logical column are
// redistributed evenly. Returns true if the layout changed.
func (w *Window) Equalize(widths, heights bool) bool {
	w.assertOwner("Equalize")
	if w.Root == nil || (!widths && !heights) {
		return false
	}

	logical := w.logicalRoot()
	if logical == nil {
		return false
	}

	widthChanged := widths && w.equalizeWidthsNeeded(logical)

	var columns []*LayoutCell
	heightChanged := false
	if heights {
		columns = collectEqualizeColumns(logical)
		for _, column := range columns {
			if column.equalizeLeafHeightsNeeded() {
				heightChanged = true
				break
			}
		}
	}

	if !widthChanged && !heightChanged {
		return false
	}

	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}

	if widthChanged {
		w.equalizeWidths(logical)
	}
	if heightChanged {
		for _, column := range columns {
			if !column.equalizeLeafHeightsNeeded() {
				continue
			}
			column.equalizeLeafHeights()
		}
	}

	w.Root.FixOffsets()
	w.resizePTYs()
	return true
}

func (w *Window) equalizeWidthsNeeded(logical *LayoutCell) bool {
	if logical == nil {
		return false
	}
	if !w.hasAnchoredLead() {
		return logical.equalizeAxisNeeded(SplitVertical)
	}
	if w.anchoredLeadColumnsWidthChanged() {
		return true
	}
	return logical.equalizeAxisNeeded(SplitVertical)
}

func (w *Window) equalizeWidths(logical *LayoutCell) {
	if logical == nil {
		return
	}
	if !w.hasAnchoredLead() {
		logical.equalizeAxisRecursive(SplitVertical)
		return
	}
	w.equalizeAnchoredLeadColumns()
	logical.equalizeAxisRecursive(SplitVertical)
}

func (w *Window) anchoredLeadColumnsWidthChanged() bool {
	columns := w.anchoredLeadWidthColumns()
	if len(columns) < 2 {
		return false
	}
	sizes := equalSplitSizes(w.Width, len(columns))
	for i, column := range columns {
		if column.W != sizes[i] {
			return true
		}
	}
	return false
}

func (w *Window) equalizeAnchoredLeadColumns() {
	columns := w.anchoredLeadWidthColumns()
	if len(columns) < 2 {
		return
	}

	lead := w.Root.Children[0]
	logical := w.Root.Children[1]
	sizes := equalSplitSizes(w.Width, len(columns))
	lead.ResizeSubtree(sizes[0], w.Root.H)

	if len(columns) == 2 {
		logical.ResizeSubtree(sizes[1], w.Root.H)
		return
	}

	logicalW := 0
	for i, size := range sizes[1:] {
		if i > 0 {
			logicalW++
		}
		logicalW += size
	}
	logical.ResizeSubtree(logicalW, w.Root.H)
}

func (w *Window) anchoredLeadWidthColumns() []*LayoutCell {
	if !w.hasAnchoredLead() {
		return nil
	}

	logical := w.logicalRoot()
	columns := []*LayoutCell{w.Root.Children[0]}
	if logical == nil {
		return columns
	}
	if !logical.IsLeaf() && logical.Dir == SplitVertical {
		return append(columns, logical.Children...)
	}
	return append(columns, logical)
}

func collectEqualizeColumns(root *LayoutCell) []*LayoutCell {
	if root == nil {
		return nil
	}
	if leafCount := root.horizontalLeafCount(); leafCount > 1 {
		return []*LayoutCell{root}
	}
	if root.IsLeaf() {
		return nil
	}

	columns := make([]*LayoutCell, 0, len(root.Children))
	for _, child := range root.Children {
		columns = append(columns, collectEqualizeColumns(child)...)
	}
	return columns
}

func (c *LayoutCell) horizontalLeafCount() int {
	if c == nil {
		return 0
	}
	if c.IsLeaf() {
		return 1
	}
	if c.Dir != SplitHorizontal {
		return 0
	}

	total := 0
	for _, child := range c.Children {
		count := child.horizontalLeafCount()
		if count == 0 {
			return 0
		}
		total += count
	}
	return total
}

func (c *LayoutCell) equalizeLeafHeightsNeeded() bool {
	targets := c.equalizeLeafHeightTargets()
	if len(targets) == 0 {
		return false
	}

	index := 0
	needed := false
	c.Walk(func(leaf *LayoutCell) {
		if needed || index >= len(targets) {
			return
		}
		if leaf.H != targets[index] {
			needed = true
			return
		}
		index++
	})
	return needed
}

func (c *LayoutCell) equalizeLeafHeights() {
	targets := c.equalizeLeafHeightTargets()
	if len(targets) == 0 {
		return
	}
	c.equalizeLeafHeightsWithTargets(targets)
}

func (c *LayoutCell) equalizeLeafHeightTargets() []int {
	leafCount := c.horizontalLeafCount()
	if leafCount < 2 {
		return nil
	}
	return equalSplitSizes(c.H, leafCount)
}

func (c *LayoutCell) equalizeLeafHeightsWithTargets(targets []int) {
	if c == nil || len(targets) == 0 {
		return
	}
	if c.IsLeaf() {
		c.ResizeSubtree(c.W, targets[0])
		return
	}

	offset := 0
	for _, child := range c.Children {
		leafCount := child.horizontalLeafCount()
		childTargets := targets[offset : offset+leafCount]
		targetHeight := leafCount - 1
		for _, height := range childTargets {
			targetHeight += height
		}
		child.ResizeSubtree(c.W, targetHeight)
		child.equalizeLeafHeightsWithTargets(childTargets)
		offset += leafCount
	}
}

func (w *Window) resizePaneGrow(siblings []*LayoutCell, idx int, axis SplitDir, needed int) int {
	grower := siblings[idx]

	// Match tmux layout_resize_pane_grow: walk tail-ward first, then fall back
	// to the head if no right/bottom sibling can donate enough space.
	remaining := w.transferSiblingRange(grower, siblings, idx, axis, needed, idx+1, len(siblings), 1)
	if remaining == 0 {
		return needed
	}
	remaining = w.transferSiblingRange(grower, siblings, idx, axis, remaining, idx-1, -1, -1)
	return needed - remaining
}

func (w *Window) resizePaneShrink(siblings []*LayoutCell, idx int, axis SplitDir, needed int) int {
	// Match tmux layout_resize_pane_shrink: grow the sibling across the border
	// and walk left/up from the border cell looking for donors.
	return needed - w.transferSiblingRange(siblings[idx+1], siblings, idx+1, axis, needed, idx, -1, -1)
}

func transferEdges(growerIdx, donorIdx int) (resizeEdge, resizeEdge) {
	if donorIdx < growerIdx {
		return resizeFromStart, resizeFromEnd
	}
	return resizeFromEnd, resizeFromStart
}

func (w *Window) transferSiblingRange(grower *LayoutCell, siblings []*LayoutCell, growerIdx int, axis SplitDir, remaining, start, stop, step int) int {
	for donorIdx := start; donorIdx != stop && remaining > 0; donorIdx += step {
		growerEdge, donorEdge := transferEdges(growerIdx, donorIdx)
		remaining -= transferAxisSize(grower, siblings[donorIdx], axis, remaining, growerEdge, donorEdge)
	}
	return remaining
}

func transferAxisSize(grower, donor *LayoutCell, axis SplitDir, needed int, growerEdge, donorEdge resizeEdge) int {
	available := donor.resizeCheck(axis)
	if available == 0 {
		return 0
	}
	if available > needed {
		available = needed
	}

	grower.resizeToAxisFromEdge(axis, grower.axisSize(axis)+available, growerEdge)
	donor.resizeToAxisFromEdge(axis, donor.axisSize(axis)-available, donorEdge)
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
