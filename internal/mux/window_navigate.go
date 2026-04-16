package mux

import "sync/atomic"

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
		if err := w.Unzoom(); err != nil {
			return
		}
	}
	w.setActive(p)
}

// Focus changes the active pane. Direction is "next", "left", "right", "up", "down".
// Uses tmux-style adjacency + perpendicular overlap + wrapping + recency tiebreaker.
// Auto-unzooms if a pane is zoomed.
func (w *Window) Focus(direction string) {
	w.assertOwner("Focus")
	if w.ZoomedPaneID != 0 {
		if err := w.Unzoom(); err != nil {
			return
		}
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
