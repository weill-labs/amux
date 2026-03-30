package client

import "github.com/weill-labs/amux/internal/render"

const windowRenamePromptTitle = "rename-window"

type windowRenamePromptState struct {
	value string
}

type promptCommand struct {
	command string
	args    []string
	bell    bool
}

func (st *windowRenamePromptState) title() string {
	return windowRenamePromptTitle
}

func (cr *ClientRenderer) WindowRenamePromptActive() bool {
	return cr.loadState().ui.windowRenamePrompt != nil
}

func (cr *ClientRenderer) ShowWindowRenamePrompt() bool {
	windows, activeWindowID := cr.renderer.WindowSnapshots()
	if len(windows) == 0 {
		return false
	}
	found := false
	for _, ws := range windows {
		if ws.ID == activeWindowID {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	result := cr.reduceUI(uiActionShowWindowRenamePrompt{
		prompt: &windowRenamePromptState{},
	})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) HideWindowRenamePrompt() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.windowRenamePrompt != nil
		return changed, next.ui.reduce(uiActionHideWindowRenamePrompt{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) HandleWindowRenamePromptInput(raw []byte) promptCommand {
	if len(raw) == 0 {
		return promptCommand{}
	}
	if !cr.WindowRenamePromptActive() {
		return promptCommand{}
	}

	if len(raw) == 3 && raw[0] == 0x1b && raw[1] == '[' {
		return promptCommand{bell: true}
	}

	result := promptCommand{}
	for _, b := range raw {
		switch {
		case b == 0x1b:
			cr.HideWindowRenamePrompt()
			return promptCommand{}
		case b == '\r' || b == '\n':
			return cr.submitWindowRenamePrompt()
		case b == 0x7f || b == 0x08:
			cr.editWindowRenamePrompt(-1, 0)
		case b >= 0x20 && b <= 0x7e:
			cr.editWindowRenamePrompt(0, b)
		default:
			result.bell = true
		}
	}
	return result
}

func (cr *ClientRenderer) editWindowRenamePrompt(backspace int, ch byte) {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.windowRenamePrompt == nil {
			return clientUIResult{}
		}
		next.ui.windowRenamePrompt = cloneWindowRenamePromptState(next.ui.windowRenamePrompt)
		if backspace < 0 {
			if len(next.ui.windowRenamePrompt.value) > 0 {
				next.ui.windowRenamePrompt.value = next.ui.windowRenamePrompt.value[:len(next.ui.windowRenamePrompt.value)-1]
			}
		} else if ch != 0 {
			next.ui.windowRenamePrompt.value += string(ch)
		}
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) submitWindowRenamePrompt() promptCommand {
	command, result := updateClientStateValue(cr, func(next *clientSnapshot) (promptCommand, clientUIResult) {
		if next.ui.windowRenamePrompt == nil {
			return promptCommand{}, clientUIResult{}
		}
		if next.ui.windowRenamePrompt.value == "" {
			return promptCommand{bell: true}, clientUIResult{}
		}
		value := next.ui.windowRenamePrompt.value
		return promptCommand{
			command: "rename-window",
			args:    []string{value},
		}, next.ui.reduce(uiActionHideWindowRenamePrompt{})
	})
	cr.emitUIEvents(result.uiEvents)
	return command
}

func (cr *ClientRenderer) windowRenamePromptOverlay() *render.TextInputOverlay {
	return cr.windowRenamePromptOverlayFromSnapshot(cr.loadState())
}

func (cr *ClientRenderer) windowRenamePromptOverlayFromSnapshot(state *clientSnapshot) *render.TextInputOverlay {
	if state.ui.windowRenamePrompt == nil {
		return nil
	}
	return &render.TextInputOverlay{
		Title: state.ui.windowRenamePrompt.title(),
		Input: state.ui.windowRenamePrompt.value,
	}
}
