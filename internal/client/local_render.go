package client

import "os"

// Local render actions are reserved for client state that cannot be updated
// safely through the clientSnapshot CAS helpers. CopyMode instances are shared,
// deeply mutable structs, so attached-client access is serialized onto the
// render loop. Simpler UI state such as messages, chooser visibility, and pane
// overlays lives in clientSnapshot and can continue to use updateState /
// updateClientStateValue from any goroutine.
func applyLocalRenderResultDirect(cr *ClientRenderer, result localRenderResult) {
	state := &clientRenderLoopState{
		useFull:             os.Getenv("AMUX_RENDER") == "full",
		renderFrameInterval: defaultRenderFrameInterval,
	}
	_ = cr.executeRenderEffects(state, result.effects, func(string) {})
}

func runLocalRenderAction(cr *ClientRenderer, msgCh chan<- *RenderMsg, fn localRenderFunc) {
	if msgCh == nil {
		applyLocalRenderResultDirect(cr, fn(cr))
		return
	}
	msgCh <- &RenderMsg{Typ: RenderMsgLocalAction, Local: fn}
}

func callLocalRenderAction[T any](cr *ClientRenderer, msgCh chan<- *RenderMsg, fn localRenderFunc) T {
	var zero T
	if msgCh == nil {
		result := fn(cr)
		applyLocalRenderResultDirect(cr, result)
		if result.value == nil {
			return zero
		}
		return result.value.(T)
	}
	reply := make(chan any, 1)
	msgCh <- &RenderMsg{Typ: RenderMsgLocalAction, Local: fn, Reply: reply}
	value := <-reply
	if value == nil {
		return zero
	}
	return value.(T)
}

func renderNowResult() localRenderResult {
	return localRenderResult{effects: appendStopAndRenderNow(nil)}
}

func overlayRenderNowResult() localRenderResult {
	effects := []clientEffect{{kind: clientEffectClearPrevGrid}}
	effects = append(effects, appendStopAndRenderNow(nil)...)
	return localRenderResult{effects: effects}
}
