package client

import "github.com/weill-labs/amux/internal/mux"

const (
	minimizeNoStackedSiblingsReason = "cannot minimize: pane has no stacked siblings"
	minimizeLeftRightSplitReason    = "cannot minimize: pane is in a left/right split; minimize only works in stacked top/bottom groups"
	minimizeLastVisibleReason       = "cannot minimize: pane is the last visible pane in this stacked group"
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
	if cell.Parent == nil {
		return minimizeNoStackedSiblingsReason
	}
	if cell.Parent.Dir != mux.SplitHorizontal {
		return minimizeLeftRightSplitReason
	}

	visibleSiblings := 0
	for _, sib := range cell.Parent.Children {
		if sib == cell {
			continue
		}
		if cr.subtreeHasVisiblePane(sib) {
			visibleSiblings++
		}
	}
	if visibleSiblings == 0 {
		return minimizeLastVisibleReason
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
