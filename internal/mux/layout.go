package mux

import "fmt"

// SplitDir indicates horizontal or vertical split direction.
type SplitDir int

const (
	SplitVertical   SplitDir = iota // vertical divider: children left-to-right
	SplitHorizontal                 // horizontal divider: children top-to-bottom
)

// LayoutCell is a node in the layout tree. Internal nodes (Dir != -1) hold
// children arranged horizontally or vertically. Leaf nodes hold a pane.
type LayoutCell struct {
	// Position and size (in terminal cells). Separator borders are NOT
	// included in these dimensions — they occupy the 1-cell gap between siblings.
	X, Y int
	W, H int

	// Dir is the split direction for internal nodes. For leaves, Dir is -1.
	Dir SplitDir

	// Leaf: points to a pane. Nil for internal nodes and client-side cells.
	Pane *Pane

	// PaneID is set on client-side rebuilt cells (where Pane is nil).
	PaneID uint32

	// Tree structure
	Parent   *LayoutCell
	Children []*LayoutCell

	// DissolveHost marks a horizontal wrapper whose first child is the visible
	// host column and whose remaining children are dissolved columns that should
	// remain collapsed below it.
	DissolveHost bool

	// DissolvedColumn marks a whole column subtree that has been dissolved into
	// the next visible column to the right. RestoreW records the width to
	// reassign when the column is reconstituted.
	DissolvedColumn bool
	RestoreW        int

	isLeaf bool
}

// PaneMinSize is the minimum pane dimension (width or height).
const PaneMinSize = 2

// NewLeaf creates a leaf cell containing a pane.
func NewLeaf(pane *Pane, x, y, w, h int) *LayoutCell {
	return &LayoutCell{
		X: x, Y: y, W: w, H: h,
		Pane:   pane,
		isLeaf: true,
		Dir:    -1,
	}
}

// NewLeafByID creates a client-side leaf cell with only a PaneID (no Pane pointer).
// Used for temporary layout trees like the zoomed-pane view.
func NewLeafByID(paneID uint32, x, y, w, h int) *LayoutCell {
	return &LayoutCell{
		X: x, Y: y, W: w, H: h,
		PaneID: paneID,
		isLeaf: true,
		Dir:    -1,
	}
}

// IsLeaf returns true if this cell is a pane leaf.
func (c *LayoutCell) IsLeaf() bool {
	return c.isLeaf
}

// Split divides this leaf cell into two. The existing pane stays in the first
// child; the new pane goes in the second. Returns the new leaf cell.
func (c *LayoutCell) Split(dir SplitDir, newPane *Pane) (*LayoutCell, error) {
	if !c.isLeaf {
		return nil, fmt.Errorf("can only split leaf cells")
	}

	// Check minimum space: need room for both panes + 1 separator
	var available int
	if dir == SplitVertical {
		available = c.W
	} else {
		available = c.H
	}
	if available < 2*PaneMinSize+1 {
		return nil, fmt.Errorf("not enough space to split (%d < %d)", available, 2*PaneMinSize+1)
	}

	// Case A: parent exists and has the same split direction — add as sibling
	// and redistribute space equally among all siblings.
	if c.Parent != nil && !c.Parent.isLeaf && c.Parent.Dir == dir {
		newLeaf := NewLeaf(newPane, 0, 0, 0, 0)
		newLeaf.Parent = c.Parent

		// Insert after c in parent's children
		idx := c.IndexInParent()
		parent := c.Parent
		parent.Children = append(parent.Children, nil)
		copy(parent.Children[idx+2:], parent.Children[idx+1:])
		parent.Children[idx+1] = newLeaf

		parent.distributeEqual()
		return newLeaf, nil
	}

	// Case B: create a new parent wrapping both cells
	// Split in half (first child gets the extra cell if odd)
	size2 := (available - 1) / 2
	size1 := available - 1 - size2
	oldPane := c.Pane

	// Convert this cell from leaf to internal node
	c.isLeaf = false
	c.Pane = nil
	c.Dir = dir

	// Create two leaf children
	var child1, child2 *LayoutCell
	if dir == SplitVertical {
		child1 = NewLeaf(oldPane, c.X, c.Y, size1, c.H)
		child2 = NewLeaf(newPane, c.X+size1+1, c.Y, size2, c.H)
	} else {
		child1 = NewLeaf(oldPane, c.X, c.Y, c.W, size1)
		child2 = NewLeaf(newPane, c.X, c.Y+size1+1, c.W, size2)
	}
	child1.Parent = c
	child2.Parent = c
	c.Children = []*LayoutCell{child1, child2}

	// Update the pane's layout cell reference
	return child2, nil
}

// Close removes this leaf from the tree and gives its space to a sibling.
// Returns the sibling that received the space.
func (c *LayoutCell) Close() *LayoutCell {
	if c.Parent == nil {
		// Root cell — nothing to collapse into
		return nil
	}

	parent := c.Parent
	idx := c.IndexInParent()

	// Remove from parent
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)

	// Pick a recipient for focus transfer (prefer next sibling, fall back to previous)
	var recipient *LayoutCell
	if idx < len(parent.Children) {
		recipient = parent.Children[idx]
	} else if len(parent.Children) > 0 {
		recipient = parent.Children[len(parent.Children)-1]
	}

	// Redistribute space equally among remaining siblings
	if len(parent.Children) > 0 {
		parent.distributeEqual()
	}

	// Collapse single-child parent
	if len(parent.Children) == 1 {
		only := parent.Children[0]
		only.Parent = parent.Parent
		only.X = parent.X
		only.Y = parent.Y
		only.W = parent.W
		only.H = parent.H

		if parent.Parent != nil {
			pidx := parent.IndexInParent()
			parent.Parent.Children[pidx] = only
		} else {
			// only becomes the new root — caller must update window.Root
		}
		return only
	}

	return recipient
}

// FixOffsets recalculates all (X, Y) positions in the tree based on sizes.
// Call after any structural change (split, close, resize).
func (c *LayoutCell) FixOffsets() {
	if c.isLeaf {
		return
	}

	if c.Dir == SplitVertical {
		xoff := c.X
		for _, child := range c.Children {
			child.X = xoff
			child.Y = c.Y
			child.FixOffsets()
			xoff += child.W + 1 // +1 for separator
		}
	} else {
		yoff := c.Y
		for _, child := range c.Children {
			child.X = c.X
			child.Y = yoff
			child.FixOffsets()
			yoff += child.H + 1
		}
	}
}

// ResizeAll adjusts the layout tree to fit new terminal dimensions.
func (c *LayoutCell) ResizeAll(newW, newH int) {
	if c == nil {
		return
	}

	// Match tmux's resize behavior: distribute exact deltas cell-by-cell
	// instead of recomputing proportions from already-rounded pane sizes.
	xchange := newW - c.W
	xlimit := c.resizeCheck(SplitVertical)
	if xchange < -xlimit {
		xchange = -xlimit
	}
	if xchange != 0 {
		c.resizeAdjust(SplitVertical, xchange)
	}

	ychange := newH - c.H
	ylimit := c.resizeCheck(SplitHorizontal)
	if ychange < -ylimit {
		ychange = -ylimit
	}
	if ychange != 0 {
		c.resizeAdjust(SplitHorizontal, ychange)
	}

	c.FixOffsets()
}

// ResizeSubtree adjusts this cell and all descendants to the target size.
// Use this when a caller may already have mutated c.W or c.H directly. Calling
// ResizeAll with those already-updated dimensions can produce a zero delta and
// leave descendants stale, so this first reconstructs the subtree's current
// aggregate size from its children before applying the target dimensions.
func (c *LayoutCell) ResizeSubtree(newW, newH int) {
	if c == nil {
		return
	}
	if c.IsLeaf() {
		c.W = newW
		c.H = newH
		return
	}
	if len(c.Children) == 0 {
		return
	}

	if c.Dir == SplitVertical {
		totalW := len(c.Children) - 1
		for _, child := range c.Children {
			totalW += child.W
		}
		c.W = totalW
		c.H = c.Children[0].H
	} else {
		totalH := len(c.Children) - 1
		for _, child := range c.Children {
			totalH += child.H
		}
		c.W = c.Children[0].W
		c.H = totalH
	}

	c.ResizeAll(newW, newH)
}

// NormalizeMinimizedHeights restores the invariant that minimized panes in a
// horizontal split occupy only their status line. Any reclaimed height is
// redistributed to direct non-minimized siblings in walk order.
func (c *LayoutCell) NormalizeMinimizedHeights(isMinimized func(*LayoutCell) bool) {
	if c == nil || c.IsLeaf() {
		return
	}

	for _, child := range c.Children {
		child.NormalizeMinimizedHeights(isMinimized)
	}

	if c.Dir != SplitHorizontal {
		return
	}

	targets := make([]int, len(c.Children))
	flexible := make([]int, 0, len(c.Children))
	total := 0
	for i, child := range c.Children {
		targets[i] = child.H
		if child.DissolvedColumn {
			targets[i] = child.collapsedHeight(isMinimized)
		} else if isMinimized(child) {
			targets[i] = StatusLineRows
		} else {
			flexible = append(flexible, i)
		}
		total += targets[i]
	}
	if len(flexible) == 0 {
		return
	}

	remaining := c.H - (len(c.Children) - 1) - total
	for remaining > 0 {
		for _, idx := range flexible {
			if remaining == 0 {
				break
			}
			targets[idx]++
			remaining--
		}
	}

	changed := false
	for i, child := range c.Children {
		targetH := targets[i]
		if child.W == c.W && child.H == targetH {
			continue
		}
		changed = true
		c.resizeHorizontalChild(child, targetH, isMinimized)
	}
	if changed {
		c.FixOffsets()
	}
}

func (c *LayoutCell) resizeHorizontalChild(child *LayoutCell, targetH int, isMinimized func(*LayoutCell) bool) {
	if child.IsLeaf() {
		child.W = c.W
		child.H = targetH
		return
	}
	child.ResizeSubtree(c.W, targetH)
	child.NormalizeMinimizedHeights(isMinimized)
}

func (c *LayoutCell) collapsedHeight(isMinimized func(*LayoutCell) bool) int {
	if c == nil {
		return 0
	}
	if c.IsLeaf() {
		if isMinimized(c) || c.DissolvedColumn {
			return StatusLineRows
		}
		return c.H
	}

	if c.Dir == SplitVertical {
		height := 0
		for _, child := range c.Children {
			if childH := child.collapsedHeight(isMinimized); childH > height {
				height = childH
			}
		}
		if height == 0 {
			return StatusLineRows
		}
		return height
	}

	height := 0
	for i, child := range c.Children {
		if i > 0 {
			height++
		}
		height += child.collapsedHeight(isMinimized)
	}
	if height == 0 {
		return StatusLineRows
	}
	return height
}

func (c *LayoutCell) resizeCheck(axis SplitDir) int {
	if c.IsLeaf() {
		size := c.W
		if axis == SplitHorizontal {
			size = c.H
		}
		if size > PaneMinSize {
			return size - PaneMinSize
		}
		return 0
	}

	if c.Dir == axis {
		available := 0
		for _, child := range c.Children {
			available += child.resizeCheck(axis)
		}
		return available
	}

	minimum := -1
	for _, child := range c.Children {
		available := child.resizeCheck(axis)
		if minimum == -1 || available < minimum {
			minimum = available
		}
	}
	if minimum < 0 {
		return 0
	}
	return minimum
}

func (c *LayoutCell) resizeAdjust(axis SplitDir, change int) {
	if axis == SplitVertical {
		c.W += change
	} else {
		c.H += change
	}

	if c.IsLeaf() {
		return
	}

	if c.Dir != axis {
		for _, child := range c.Children {
			child.resizeAdjust(axis, change)
		}
		return
	}

	for change != 0 {
		for _, child := range c.Children {
			if change == 0 {
				break
			}
			if change > 0 {
				child.resizeAdjust(axis, 1)
				change--
				continue
			}
			if child.resizeCheck(axis) > 0 {
				child.resizeAdjust(axis, -1)
				change++
			}
		}
	}
}

// HasNonMinimizedLeaf returns true if any leaf in the subtree has a
// non-minimized pane. Used by minimize guards to check whether a subtree
// sibling actually has visible content.
func (c *LayoutCell) HasNonMinimizedLeaf() bool {
	if c.IsLeaf() {
		return c.Pane != nil && !c.Pane.Meta.Minimized
	}
	for _, child := range c.Children {
		if child.HasNonMinimizedLeaf() {
			return true
		}
	}
	return false
}

// Walk calls fn for every leaf cell in the tree (depth-first).
func (c *LayoutCell) Walk(fn func(*LayoutCell)) {
	if c.isLeaf {
		fn(c)
		return
	}
	for _, child := range c.Children {
		child.Walk(fn)
	}
}

// FindPane returns the leaf cell containing the given pane, or nil.
func (c *LayoutCell) FindPane(paneID uint32) *LayoutCell {
	var found *LayoutCell
	c.Walk(func(leaf *LayoutCell) {
		if leaf.Pane != nil && leaf.Pane.ID == paneID {
			found = leaf
		}
	})
	return found
}

// CellPaneID returns the effective pane ID for this leaf cell.
// Server-side cells have Pane set (returns Pane.ID).
// Client-side cells have PaneID set (returns PaneID).
func (c *LayoutCell) CellPaneID() uint32 {
	if c.Pane != nil {
		return c.Pane.ID
	}
	return c.PaneID
}

// FindByPaneID returns the leaf cell with the given pane ID.
// Works for both server-side cells (Pane.ID) and client-side cells (PaneID).
func (c *LayoutCell) FindByPaneID(paneID uint32) *LayoutCell {
	var found *LayoutCell
	c.Walk(func(leaf *LayoutCell) {
		if leaf.CellPaneID() == paneID {
			found = leaf
		}
	})
	return found
}

// FindLeafAt returns the leaf cell containing (x, y), or nil if (x, y) is
// on a border or outside all cells. Coordinates are 0-based.
func (c *LayoutCell) FindLeafAt(x, y int) *LayoutCell {
	if c.isLeaf {
		if x >= c.X && x < c.X+c.W && y >= c.Y && y < c.Y+c.H {
			return c
		}
		return nil
	}
	for _, child := range c.Children {
		if leaf := child.FindLeafAt(x, y); leaf != nil {
			return leaf
		}
	}
	return nil
}

// BorderHit describes the two children on either side of a border.
type BorderHit struct {
	Left  *LayoutCell // child on the left/top side
	Right *LayoutCell // child on the right/bottom side
	Dir   SplitDir    // split direction of the parent
}

// FindBorderAt returns the two adjacent children and split direction for
// a border at (x, y), or nil if (x, y) is not on a border.
func (c *LayoutCell) FindBorderAt(x, y int) *BorderHit {
	if c.isLeaf {
		return nil
	}
	for i := 0; i < len(c.Children)-1; i++ {
		left := c.Children[i]
		right := c.Children[i+1]
		if c.Dir == SplitVertical {
			// Vertical border at x = left.X + left.W
			borderX := left.X + left.W
			if x == borderX && y >= c.Y && y < c.Y+c.H {
				return &BorderHit{Left: left, Right: right, Dir: c.Dir}
			}
		} else {
			// Horizontal border at y = left.Y + left.H
			borderY := left.Y + left.H
			if y == borderY && x >= c.X && x < c.X+c.W {
				return &BorderHit{Left: left, Right: right, Dir: c.Dir}
			}
		}
	}
	// Search children recursively
	for _, child := range c.Children {
		if hit := child.FindBorderAt(x, y); hit != nil {
			return hit
		}
	}
	return nil
}

// FindBorderNear returns a border at (x, y) or within a one-cell cardinal
// neighborhood. This matches tmux's drag behavior, which tolerates slight
// pointer drift while dragging a border.
func (c *LayoutCell) FindBorderNear(x, y int) *BorderHit {
	offsets := [][2]int{
		{0, 0},
		{0, 1},
		{1, 0},
		{0, -1},
		{-1, 0},
	}
	for _, off := range offsets {
		if hit := c.FindBorderAt(x+off[0], y+off[1]); hit != nil {
			return hit
		}
	}
	return nil
}

// distributeEqual sets all children to equal sizes along the split direction.
// The last child receives the remainder to account for integer rounding.
func (c *LayoutCell) distributeEqual() {
	n := len(c.Children)
	seps := n - 1
	if c.Dir == SplitVertical {
		each := (c.W - seps) / n
		for i, child := range c.Children {
			targetW := each
			if i == n-1 {
				targetW = c.W - seps - each*(n-1)
			}
			if child.IsLeaf() {
				child.W = targetW
				child.H = c.H
			} else {
				child.ResizeAll(targetW, c.H)
			}
		}
	} else {
		each := (c.H - seps) / n
		for i, child := range c.Children {
			targetH := each
			if i == n-1 {
				targetH = c.H - seps - each*(n-1)
			}
			if child.IsLeaf() {
				child.H = targetH
				child.W = c.W
			} else {
				child.ResizeAll(c.W, targetH)
			}
		}
	}
}

// IndexInParent returns the index of this cell within its parent's Children
// slice, or -1 if the cell has no parent.
func (c *LayoutCell) IndexInParent() int {
	if c.Parent == nil {
		return -1
	}
	for i, sib := range c.Parent.Children {
		if sib == c {
			return i
		}
	}
	return -1
}
