package mux

import "fmt"

// SplitDir indicates horizontal or vertical split direction.
type SplitDir int

const (
	SplitHorizontal SplitDir = iota // children left-to-right
	SplitVertical                   // children top-to-bottom
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

	// Leaf: points to a pane. Nil for internal nodes.
	Pane *Pane

	// Tree structure
	Parent   *LayoutCell
	Children []*LayoutCell

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
	if dir == SplitHorizontal {
		available = c.W
	} else {
		available = c.H
	}
	if available < 2*PaneMinSize+1 {
		return nil, fmt.Errorf("not enough space to split (%d < %d)", available, 2*PaneMinSize+1)
	}

	// Split in half (first child gets the extra cell if odd)
	size2 := (available - 1) / 2
	size1 := available - 1 - size2

	// Case A: parent exists and has the same split direction — add as sibling
	if c.Parent != nil && !c.Parent.isLeaf && c.Parent.Dir == dir {
		newLeaf := NewLeaf(newPane, 0, 0, 0, 0) // offsets fixed later
		newLeaf.Parent = c.Parent

		// Shrink current cell, insert new sibling after it
		if dir == SplitHorizontal {
			newLeaf.W = size2
			newLeaf.H = c.H
			c.W = size1
		} else {
			newLeaf.W = c.W
			newLeaf.H = size2
			c.H = size1
		}

		// Insert after c in parent's children
		idx := c.indexInParent()
		parent := c.Parent
		parent.Children = append(parent.Children, nil)
		copy(parent.Children[idx+2:], parent.Children[idx+1:])
		parent.Children[idx+1] = newLeaf

		return newLeaf, nil
	}

	// Case B: create a new parent wrapping both cells
	oldPane := c.Pane

	// Convert this cell from leaf to internal node
	c.isLeaf = false
	c.Pane = nil
	c.Dir = dir

	// Create two leaf children
	var child1, child2 *LayoutCell
	if dir == SplitHorizontal {
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
	idx := c.indexInParent()

	// Pick recipient: prefer next sibling, fall back to previous
	var recipient *LayoutCell
	if idx+1 < len(parent.Children) {
		recipient = parent.Children[idx+1]
	} else if idx > 0 {
		recipient = parent.Children[idx-1]
	}

	// Transfer space (+1 for the separator that disappears)
	if recipient != nil {
		if parent.Dir == SplitHorizontal {
			recipient.W += c.W + 1
		} else {
			recipient.H += c.H + 1
		}
	}

	// Remove from parent
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)

	// Collapse single-child parent
	if len(parent.Children) == 1 {
		only := parent.Children[0]
		only.Parent = parent.Parent
		only.X = parent.X
		only.Y = parent.Y
		only.W = parent.W
		only.H = parent.H

		if parent.Parent != nil {
			pidx := parent.indexInParent()
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

	if c.Dir == SplitHorizontal {
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
	if c.isLeaf {
		c.W = newW
		c.H = newH
		return
	}

	dw := newW - c.W
	dh := newH - c.H
	c.W = newW
	c.H = newH

	if c.Dir == SplitHorizontal {
		// Distribute width change proportionally among children
		c.distributeResize(dw, true)
		// All children get the full height
		for _, child := range c.Children {
			child.H = newH
			if !child.isLeaf {
				child.ResizeAll(child.W, child.H)
			}
		}
	} else {
		// Distribute height change proportionally among children
		c.distributeResize(dh, false)
		// All children get the full width
		for _, child := range c.Children {
			child.W = newW
			if !child.isLeaf {
				child.ResizeAll(child.W, child.H)
			}
		}
	}

	c.FixOffsets()
}

// distributeResize spreads a size delta proportionally across children.
func (c *LayoutCell) distributeResize(delta int, horizontal bool) {
	n := len(c.Children)
	if n == 0 || delta == 0 {
		return
	}

	// Distribute evenly, remainder to the last child
	per := delta / n
	remainder := delta - per*n

	for i, child := range c.Children {
		extra := per
		if i == n-1 {
			extra += remainder
		}
		if horizontal {
			child.W += extra
			if child.W < PaneMinSize {
				child.W = PaneMinSize
			}
		} else {
			child.H += extra
			if child.H < PaneMinSize {
				child.H = PaneMinSize
			}
		}
	}
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

func (c *LayoutCell) indexInParent() int {
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
