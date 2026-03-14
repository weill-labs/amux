package mux

import (
	"fmt"
	"strings"
)

// StatusLineRows is the number of rows reserved for the per-pane status line.
// The compositor draws the status line; pane PTYs get cellH - StatusLineRows.
const StatusLineRows = 1

// Window holds the layout tree and active pane for one window.
type Window struct {
	Root       *LayoutCell
	ActivePane *Pane
	Width      int
	Height     int
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
func (w *Window) SplitRoot(dir SplitDir, newPane *Pane) (*Pane, error) {
	// Wrap the current root in a new parent and add the new pane as a sibling
	oldRoot := w.Root

	newLeaf := NewLeaf(newPane, 0, 0, 0, 0)
	var child1H, child2H int

	if dir == SplitHorizontal {
		size2 := (oldRoot.W - 1) / 2
		size1 := oldRoot.W - 1 - size2
		child1H = oldRoot.H
		child2H = oldRoot.H
		oldRoot.W = size1
		newLeaf.W = size2
		newLeaf.H = oldRoot.H
	} else {
		size2 := (oldRoot.H - 1) / 2
		size1 := oldRoot.H - 1 - size2
		child1H = size1
		child2H = size2
		oldRoot.H = size1
		newLeaf.W = oldRoot.W
		newLeaf.H = size2
	}

	newRoot := &LayoutCell{
		X: 0, Y: 0, W: w.Width, H: w.Height,
		Dir:      dir,
		Children: []*LayoutCell{oldRoot, newLeaf},
	}
	oldRoot.Parent = newRoot
	newLeaf.Parent = newRoot
	w.Root = newRoot

	w.Root.FixOffsets()

	// Resize all PTYs
	_ = child1H
	_ = child2H
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			c.Pane.Resize(c.W, paneHeight(c.H))
		}
	})

	w.ActivePane = newPane
	return newPane, nil
}

// Split splits the active pane in the given direction, creating a new pane
// via the provided factory function. Returns the new pane.
func (w *Window) Split(dir SplitDir, newPane *Pane) (*Pane, error) {
	cell := w.Root.FindPane(w.ActivePane.ID)
	if cell == nil {
		return nil, fmt.Errorf("active pane %d not found in layout", w.ActivePane.ID)
	}

	newCell, err := cell.Split(dir, newPane)
	if err != nil {
		return nil, err
	}

	// Resize PTYs to match layout cells (minus status line row)
	newPane.Resize(newCell.W, paneHeight(newCell.H))

	existingCell := w.Root.FindPane(w.ActivePane.ID)
	if existingCell != nil {
		w.ActivePane.Resize(existingCell.W, paneHeight(existingCell.H))
	}

	w.Root.FixOffsets()
	w.ActivePane = newPane

	return newPane, nil
}

// ClosePane removes a pane from the layout and reclaims its space.
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

	// Update active pane if the closed pane was active
	if w.ActivePane.ID == paneID {
		if result != nil && result.IsLeaf() && result.Pane != nil {
			w.ActivePane = result.Pane
		} else {
			// Find any leaf
			w.Root.Walk(func(c *LayoutCell) {
				if w.ActivePane.ID == paneID && c.Pane != nil {
					w.ActivePane = c.Pane
				}
			})
		}
	}

	// Resize the recipient pane to match its new cell dimensions
	w.Root.FixOffsets()
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			c.Pane.Resize(c.W, paneHeight(c.H))
		}
	})

	return nil
}

// Resize adjusts the layout to fit new terminal dimensions.
func (w *Window) Resize(width, height int) {
	w.Width = width
	w.Height = height
	w.Root.ResizeAll(width, height)

	// Resize all pane PTYs to match their new cell dimensions
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			c.Pane.Resize(c.W, paneHeight(c.H))
		}
	})
}

// Focus changes the active pane. Direction is "next", "left", "right", "up", "down".
func (w *Window) Focus(direction string) {
	panes := w.Panes()
	if len(panes) <= 1 {
		return
	}

	if direction == "next" {
		// Cycle to next pane
		for i, p := range panes {
			if p.ID == w.ActivePane.ID {
				w.ActivePane = panes[(i+1)%len(panes)]
				return
			}
		}
		return
	}

	// Directional focus: find the active cell, then find the nearest neighbor
	activeCell := w.Root.FindPane(w.ActivePane.ID)
	if activeCell == nil {
		return
	}

	// Center point of active pane
	cx := activeCell.X + activeCell.W/2
	cy := activeCell.Y + activeCell.H/2

	var best *LayoutCell
	bestDist := int(^uint(0) >> 1) // max int

	w.Root.Walk(func(cell *LayoutCell) {
		if cell.Pane == nil || cell.Pane.ID == w.ActivePane.ID {
			return
		}

		ncx := cell.X + cell.W/2
		ncy := cell.Y + cell.H/2

		match := false
		switch direction {
		case "left":
			match = ncx < cx
		case "right":
			match = ncx > cx
		case "up":
			match = ncy < cy
		case "down":
			match = ncy > cy
		}

		if !match {
			return
		}

		dx := cx - ncx
		dy := cy - ncy
		dist := dx*dx + dy*dy
		if dist < bestDist {
			bestDist = dist
			best = cell
		}
	})

	if best != nil {
		w.ActivePane = best.Pane
	}
}

// paneHeight returns the PTY height for a pane in a layout cell,
// accounting for the per-pane status line.
func paneHeight(cellH int) int {
	h := cellH - StatusLineRows
	if h < 1 {
		h = 1
	}
	return h
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
	// Prefix match on name
	for _, p := range w.Panes() {
		if strings.HasPrefix(p.Meta.Name, ref) {
			return p
		}
	}
	return nil
}

// Minimize shrinks a pane's layout cell to StatusLineRows + 1 (just status + 1 row).
func (w *Window) Minimize(paneID uint32) error {
	cell := w.Root.FindPane(paneID)
	if cell == nil {
		return fmt.Errorf("pane %d not found", paneID)
	}
	if cell.Pane.Meta.Minimized {
		return fmt.Errorf("pane already minimized")
	}

	cell.Pane.Meta.Minimized = true
	cell.Pane.Meta.RestoreH = cell.H

	cell.H = StatusLineRows + 1
	cell.Pane.Resize(cell.W, 1)

	// Give reclaimed space to a sibling
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
						sib.Pane.Resize(sib.W, paneHeight(sib.H))
					}
					break
				}
			}
		}
	}

	w.Root.FixOffsets()
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
		savedH = 12
	}

	// Take space from a sibling
	needed := savedH - cell.H
	if cell.Parent != nil && needed > 0 {
		for _, sib := range cell.Parent.Children {
			if sib != cell {
				give := needed
				if cell.Parent.Dir == SplitVertical && sib.H-give >= PaneMinSize+StatusLineRows {
					sib.H -= give
					if !sib.IsLeaf() {
						sib.ResizeAll(sib.W, sib.H)
					} else if sib.Pane != nil {
						sib.Pane.Resize(sib.W, paneHeight(sib.H))
					}
					break
				}
			}
		}
	}

	cell.H = savedH
	cell.Pane.Meta.Minimized = false
	cell.Pane.Meta.RestoreH = 0
	cell.Pane.Resize(cell.W, paneHeight(cell.H))

	w.Root.FixOffsets()
	return nil
}
