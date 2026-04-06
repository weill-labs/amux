package client

import "github.com/weill-labs/amux/internal/render"

type windowTabDragOverlayState struct {
	indicator *render.WindowDropIndicatorOverlay
}

func (cr *ClientRenderer) showWindowTabDragOverlay(indicator *render.WindowDropIndicatorOverlay) {
	result := cr.updateState(func(next *clientSnapshot) clientUIResult {
		return next.ui.reduce(uiActionShowWindowTabDrag{drag: &windowTabDragOverlayState{
			indicator: cloneWindowDropIndicator(indicator),
		}})
	})
	cr.emitUIEvents(result.uiEvents)
}

func (cr *ClientRenderer) hideWindowTabDragOverlay() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		if next.ui.windowTabDrag == nil {
			return false, clientUIResult{}
		}
		return true, next.ui.reduce(uiActionHideWindowTabDrag{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func cloneWindowDropIndicator(src *render.WindowDropIndicatorOverlay) *render.WindowDropIndicatorOverlay {
	if src == nil {
		return nil
	}
	dst := *src
	return &dst
}

func (cr *ClientRenderer) windowTabDragIndicatorFromSnapshot(state *clientSnapshot) *render.WindowDropIndicatorOverlay {
	if state.ui.windowTabDrag == nil || state.ui.windowTabDrag.indicator == nil {
		return nil
	}
	return cloneWindowDropIndicator(state.ui.windowTabDrag.indicator)
}
