package client

import "github.com/weill-labs/amux/internal/render"

type paneDragOverlayState struct {
	sourcePaneID uint32
	targetPaneID uint32
	targetLabel  string
	indicator    *render.DropIndicatorOverlay
}

func (cr *ClientRenderer) showPaneDragOverlay(sourcePaneID, targetPaneID uint32, targetLabel string, indicator *render.DropIndicatorOverlay) {
	result := cr.updateState(func(next *clientSnapshot) clientUIResult {
		return next.ui.reduce(uiActionShowPaneDrag{drag: &paneDragOverlayState{
			sourcePaneID: sourcePaneID,
			targetPaneID: targetPaneID,
			targetLabel:  targetLabel,
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

	labels := []render.PaneOverlayLabel{{
		PaneID: state.ui.paneDrag.sourcePaneID,
		Label:  "drag",
	}}
	if state.ui.paneDrag.targetPaneID != 0 && state.ui.paneDrag.targetLabel != "" {
		labels = append(labels, render.PaneOverlayLabel{
			PaneID: state.ui.paneDrag.targetPaneID,
			Label:  state.ui.paneDrag.targetLabel,
		})
	}
	return labels
}

func (cr *ClientRenderer) paneDragIndicatorFromSnapshot(state *clientSnapshot) *render.DropIndicatorOverlay {
	if state.ui.paneDrag == nil || state.ui.paneDrag.indicator == nil {
		return nil
	}
	return cloneDropIndicator(state.ui.paneDrag.indicator)
}
