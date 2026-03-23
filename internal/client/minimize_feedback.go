package client

import "github.com/weill-labs/amux/internal/mux"

const (
	minimizeNoStackedSiblingsReason = "cannot minimize: pane has no stacked siblings"
	minimizeRightmostColumnReason   = "cannot minimize: pane is in the rightmost column"
)

func (cr *ClientRenderer) toggleMinimizeBlockedReason() string {
	activePaneID := cr.ActivePaneID()
	info, ok := cr.renderer.PaneInfo(activePaneID)
	if !ok || info.Minimized {
		return ""
	}

	layout := cr.Layout()
	if layout == nil {
		return ""
	}
	cell := layout.FindByPaneID(activePaneID)
	if cell == nil {
		return ""
	}
	column := cell
	for column.Parent != nil && column.Parent.Dir == mux.SplitHorizontal {
		column = column.Parent
	}
	if column.Parent == nil {
		return minimizeNoStackedSiblingsReason
	}

	hasVisiblePeer := false
	column.Walk(func(c *mux.LayoutCell) {
		if hasVisiblePeer || c.CellPaneID() == activePaneID {
			return
		}
		if info, ok := cr.renderer.PaneInfo(c.CellPaneID()); ok && !info.Minimized {
			hasVisiblePeer = true
		}
	})
	if hasVisiblePeer {
		return ""
	}

	if column.Parent.Dir != mux.SplitVertical {
		return minimizeNoStackedSiblingsReason
	}
	if column.IndexInParent() == len(column.Parent.Children)-1 {
		return minimizeRightmostColumnReason
	}

	return ""
}

func (cr *ClientRenderer) subtreeHasVisiblePane(root *mux.LayoutCell) bool {
	hasVisible := false
	root.Walk(func(cell *mux.LayoutCell) {
		if hasVisible {
			return
		}
		if info, ok := cr.renderer.PaneInfo(cell.CellPaneID()); ok && !info.Minimized {
			hasVisible = true
		}
	})
	return hasVisible
}
