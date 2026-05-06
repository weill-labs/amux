package client

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/weill-labs/amux/internal/bubblesutil"
	"github.com/weill-labs/amux/internal/render"
)

const paneRenamePromptTitle = "rename-pane"

type paneRenamePromptState struct {
	paneRef string
	input   bubblesutil.TextInputState
}

func (st *paneRenamePromptState) title() string {
	return paneRenamePromptTitle
}

func (cr *ClientRenderer) PaneRenamePromptActive() bool {
	return cr.loadState().ui.paneRenamePrompt != nil
}

func (cr *ClientRenderer) ShowPaneRenamePrompt() bool {
	paneID := cr.ActivePaneID()
	if paneID == 0 {
		return false
	}
	pane, ok := cr.renderer.PaneInfoSnapshot(paneID)
	if !ok {
		return false
	}
	paneRef := pane.Name
	if paneRef == "" {
		paneRef = fmt.Sprintf("%d", paneID)
	}
	result := cr.reduceUI(uiActionShowPaneRenamePrompt{
		prompt: &paneRenamePromptState{paneRef: paneRef},
	})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) HidePaneRenamePrompt() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.paneRenamePrompt != nil
		return changed, next.ui.reduce(uiActionHidePaneRenamePrompt{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) HandlePaneRenamePromptInput(raw []byte) promptCommand {
	if len(raw) == 0 {
		return promptCommand{}
	}
	if !cr.PaneRenamePromptActive() {
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
			cr.HidePaneRenamePrompt()
			return promptCommand{}
		case tea.KeyEnter:
			return cr.submitPaneRenamePrompt()
		default:
			if bubblesutil.IsTextInputKey(msg.Type) {
				cr.applyPaneRenamePromptKey(msg)
				continue
			}
			result.bell = true
		}
	}
	return result
}

func (cr *ClientRenderer) applyPaneRenamePromptKey(msg tea.KeyMsg) {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.paneRenamePrompt == nil {
			return clientUIResult{}
		}
		next.ui.paneRenamePrompt = clonePaneRenamePromptState(next.ui.paneRenamePrompt)
		next.ui.paneRenamePrompt.input.Update(msg)
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) submitPaneRenamePrompt() promptCommand {
	command, result := updateClientStateValue(cr, func(next *clientSnapshot) (promptCommand, clientUIResult) {
		if next.ui.paneRenamePrompt == nil {
			return promptCommand{}, clientUIResult{}
		}
		value := next.ui.paneRenamePrompt.input.Value
		if !validPaneRenamePromptName(value) {
			return promptCommand{bell: true}, clientUIResult{}
		}
		paneRef := next.ui.paneRenamePrompt.paneRef
		return promptCommand{
			command: "rename",
			args:    []string{paneRef, value},
		}, next.ui.reduce(uiActionHidePaneRenamePrompt{})
	})
	cr.emitUIEvents(result.uiEvents)
	return command
}

func validPaneRenamePromptName(name string) bool {
	return name != "" &&
		!strings.Contains(name, "/") &&
		strings.IndexFunc(name, unicode.IsSpace) < 0
}

func (cr *ClientRenderer) paneRenamePromptOverlay() *render.TextInputOverlay {
	return cr.paneRenamePromptOverlayFromSnapshot(cr.loadState())
}

func (cr *ClientRenderer) paneRenamePromptOverlayFromSnapshot(state *clientSnapshot) *render.TextInputOverlay {
	if state.ui.paneRenamePrompt == nil {
		return nil
	}
	return &render.TextInputOverlay{
		Title: state.ui.paneRenamePrompt.title(),
		Input: state.ui.paneRenamePrompt.input.Value,
	}
}
