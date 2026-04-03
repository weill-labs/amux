package client

import "github.com/weill-labs/amux/internal/render"

type paneDragOverlayState struct {
	sourcePaneID uint32
	indicator    *render.DropIndicatorOverlay
}

// paneDragSourcePaneID returns the pane ID being dragged, or 0 if no drag
// is in progress.
func paneDragSourcePaneID(drag *paneDragOverlayState) uint32 {
	if drag == nil {
		return 0
	}
	return drag.sourcePaneID
}

func (cr *ClientRenderer) showPaneDragOverlay(sourcePaneID uint32, indicator *render.DropIndicatorOverlay) {
	result := cr.updateState(func(next *clientSnapshot) clientUIResult {
		return next.ui.reduce(uiActionShowPaneDrag{drag: &paneDragOverlayState{
			sourcePaneID: sourcePaneID,
			indicator:    cloneDropIndicator(indicator),
		}})
	})
	cr.emitUIEvents(result.uiEvents)
}

func (cr *ClientRenderer) hidePaneDragOverlay() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		if next.ui.paneDrag == nil {
			return false, clientUIResult{}
		}
		return true, next.ui.reduce(uiActionHidePaneDrag{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func cloneDropIndicator(src *render.DropIndicatorOverlay) *render.DropIndicatorOverlay {
	if src == nil {
		return nil
	}
	dst := *src
	return &dst
}

func (cr *ClientRenderer) paneDragLabelsFromSnapshot(state *clientSnapshot) []render.PaneOverlayLabel {
	if state.ui.paneDrag == nil {
		return nil
	}

	return []render.PaneOverlayLabel{{
		PaneID: state.ui.paneDrag.sourcePaneID,
		Label:  "drag",
	}}
}

func (cr *ClientRenderer) paneDragIndicatorFromSnapshot(state *clientSnapshot) *render.DropIndicatorOverlay {
	if state.ui.paneDrag == nil || state.ui.paneDrag.indicator == nil {
		return nil
	}
	return cloneDropIndicator(state.ui.paneDrag.indicator)
}

func paneDragHidesCursor(state *clientSnapshot, activePaneID, paneID uint32) bool {
	return state.ui.paneDrag != nil && activePaneID != 0 && paneID == activePaneID
}
