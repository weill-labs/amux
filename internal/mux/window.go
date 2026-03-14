package mux

import "fmt"

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

	// Resize the new pane's PTY to match its layout cell
	newPane.Resize(newCell.W, newCell.H)

	// Resize the existing pane's PTY (its cell may have shrunk)
	existingCell := w.Root.FindPane(w.ActivePane.ID)
	if existingCell != nil {
		w.ActivePane.Resize(existingCell.W, existingCell.H)
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
			c.Pane.Resize(c.W, c.H)
		}
	})

	return nil
}

// Resize adjusts the layout to fit new terminal dimensions.
func (w *Window) Resize(width, height int) {
	w.Width = width
	w.Height = height
	w.Root.ResizeAll(width, height)
	w.Root.FixOffsets()

	// Resize all pane PTYs to match their new cell dimensions
	w.Root.Walk(func(c *LayoutCell) {
		if c.Pane != nil {
			c.Pane.Resize(c.W, c.H)
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
