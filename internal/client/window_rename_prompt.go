package client

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/weill-labs/amux/internal/bubblesutil"
	"github.com/weill-labs/amux/internal/render"
)

const windowRenamePromptTitle = "rename-window"

type windowRenamePromptState struct {
	input bubblesutil.TextInputState
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
	for _, ws := range windows {
		if ws.ID != activeWindowID {
			continue
		}
		result := cr.reduceUI(uiActionShowWindowRenamePrompt{
			prompt: &windowRenamePromptState{},
		})
		cr.emitUIEvents(result.uiEvents)
		return true
	}
	return false
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

	result := promptCommand{}
	for len(raw) > 0 {
		msg, consumed, ok := bubblesutil.DecodeKey(raw)
		if !ok || consumed <= 0 {
			result.bell = true
			raw = raw[1:]
			continue
		}
		raw = raw[consumed:]

		switch msg.Type {
		case tea.KeyEsc:
			cr.HideWindowRenamePrompt()
			return promptCommand{}
		case tea.KeyEnter:
			return cr.submitWindowRenamePrompt()
		default:
			if bubblesutil.IsTextInputKey(msg.Type) {
				cr.applyWindowRenamePromptKey(msg)
				continue
			}
			result.bell = true
		}
	}
	return result
}

func (cr *ClientRenderer) editWindowRenamePrompt(backspace int, ch byte) {
	switch {
	case backspace < 0:
		cr.applyWindowRenamePromptKey(tea.KeyMsg{Type: tea.KeyBackspace})
	case ch != 0:
		cr.applyWindowRenamePromptKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(ch)}})
	default:
		return
	}
}

func (cr *ClientRenderer) applyWindowRenamePromptKey(msg tea.KeyMsg) {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.windowRenamePrompt == nil {
			return clientUIResult{}
		}
		next.ui.windowRenamePrompt = cloneWindowRenamePromptState(next.ui.windowRenamePrompt)
		next.ui.windowRenamePrompt.input.Update(msg)
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) submitWindowRenamePrompt() promptCommand {
	command, result := updateClientStateValue(cr, func(next *clientSnapshot) (promptCommand, clientUIResult) {
		if next.ui.windowRenamePrompt == nil {
			return promptCommand{}, clientUIResult{}
		}
		if next.ui.windowRenamePrompt.input.Value == "" {
			return promptCommand{bell: true}, clientUIResult{}
		}
		value := next.ui.windowRenamePrompt.input.Value
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
		Input: state.ui.windowRenamePrompt.input.Value,
	}
}
