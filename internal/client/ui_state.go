package client

import (
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/proto"
)

type clientUIState struct {
	dirty              bool
	dirtyPanes         map[uint32]struct{}
	fullRedraw         bool
	copyModes          map[uint32]*copymode.CopyMode
	displayPanes       *displayPanesState
	paneDrag           *paneDragOverlayState
	windowTabDrag      *windowTabDragOverlayState
	chooser            *chooserState
	windowRenamePrompt *windowRenamePromptState
	helpBar            *helpBarState
	message            string
	inputIdle          bool
}

func newClientUIState() clientUIState {
	return clientUIState{
		copyModes:  make(map[uint32]*copymode.CopyMode),
		dirtyPanes: make(map[uint32]struct{}),
		inputIdle:  true,
	}
}

type clientUIResult struct {
	uiEvents []string
}

type uiActionHandleLayout struct {
	structureChanged bool
}

type uiActionSetInputIdle struct {
	idle bool
}

type uiActionPaneOutput struct {
	paneID uint32
}

type uiActionSetMessage struct {
	message string
}

type uiActionClearMessage struct{}

type uiActionShowDisplayPanes struct {
	displayPanes *displayPanesState
}

type uiActionHideDisplayPanes struct{}

type uiActionShowChooser struct {
	chooser *chooserState
}

type uiActionHideChooser struct{}

type uiActionShowPaneDrag struct {
	drag *paneDragOverlayState
}

type uiActionHidePaneDrag struct{}

type uiActionShowWindowTabDrag struct {
	drag *windowTabDragOverlayState
}

type uiActionHideWindowTabDrag struct{}

type uiActionShowWindowRenamePrompt struct {
	prompt *windowRenamePromptState
}

type uiActionHideWindowRenamePrompt struct{}

type uiActionShowHelpBar struct {
	bar *helpBarState
}

type uiActionHideHelpBar struct{}

type uiActionEnterCopyMode struct {
	paneID uint32
	mode   *copymode.CopyMode
}

type uiActionExitCopyMode struct {
	paneID uint32
}

func (st *clientUIState) reduce(action any) clientUIResult {
	switch action := action.(type) {
	case uiActionHandleLayout:
		return st.reduceHandleLayout(action)
	case uiActionSetInputIdle:
		return st.reduceSetInputIdle(action)
	case uiActionPaneOutput:
		st.markPaneDirty(action.paneID)
		return clientUIResult{}
	case uiActionSetMessage:
		wasVisible := st.message != ""
		st.message = action.message
		st.dirty = true
		if !wasVisible && action.message != "" {
			return clientUIResult{uiEvents: []string{proto.UIEventPrefixMessageShown}}
		}
		if wasVisible && action.message == "" {
			return clientUIResult{uiEvents: []string{proto.UIEventPrefixMessageHidden}}
		}
		return clientUIResult{}
	case uiActionClearMessage:
		if st.message == "" {
			return clientUIResult{}
		}
		st.message = ""
		st.dirty = true
		return clientUIResult{uiEvents: []string{proto.UIEventPrefixMessageHidden}}
	case uiActionShowDisplayPanes:
		wasActive := st.displayPanes != nil
		st.displayPanes = action.displayPanes
		st.dirty = true
		if wasActive {
			return clientUIResult{}
		}
		return clientUIResult{uiEvents: []string{proto.UIEventDisplayPanesShown}}
	case uiActionHideDisplayPanes:
		if st.displayPanes == nil {
			return clientUIResult{}
		}
		st.displayPanes = nil
		st.dirty = true
		return clientUIResult{uiEvents: []string{proto.UIEventDisplayPanesHidden}}
	case uiActionShowChooser:
		return st.reduceShowChooser(action)
	case uiActionHideChooser:
		if st.chooser == nil {
			return clientUIResult{}
		}
		mode := st.chooser.mode
		st.chooser = nil
		st.dirty = true
		return clientUIResult{uiEvents: []string{mode.hiddenEvent()}}
	case uiActionShowPaneDrag:
		st.paneDrag = action.drag
		st.dirty = true
		return clientUIResult{}
	case uiActionHidePaneDrag:
		if st.paneDrag == nil {
			return clientUIResult{}
		}
		st.paneDrag = nil
		st.dirty = true
		return clientUIResult{}
	case uiActionShowWindowTabDrag:
		st.windowTabDrag = action.drag
		st.dirty = true
		return clientUIResult{}
	case uiActionHideWindowTabDrag:
		if st.windowTabDrag == nil {
			return clientUIResult{}
		}
		st.windowTabDrag = nil
		st.dirty = true
		return clientUIResult{}
	case uiActionShowWindowRenamePrompt:
		return st.reduceShowWindowRenamePrompt(action)
	case uiActionHideWindowRenamePrompt:
		if st.windowRenamePrompt == nil {
			return clientUIResult{}
		}
		st.windowRenamePrompt = nil
		st.dirty = true
		return clientUIResult{}
	case uiActionShowHelpBar:
		return st.reduceShowHelpBar(action)
	case uiActionHideHelpBar:
		if st.helpBar == nil {
			return clientUIResult{}
		}
		st.helpBar = nil
		st.dirty = true
		return clientUIResult{}
	case uiActionEnterCopyMode:
		wasVisible := len(st.copyModes) > 0
		if st.copyModes[action.paneID] != nil {
			return clientUIResult{}
		}
		st.copyModes[action.paneID] = action.mode
		st.dirty = true
		if wasVisible {
			return clientUIResult{}
		}
		return clientUIResult{uiEvents: []string{proto.UIEventCopyModeShown}}
	case uiActionExitCopyMode:
		wasVisible := len(st.copyModes) > 0
		delete(st.copyModes, action.paneID)
		st.dirty = true
		if wasVisible && len(st.copyModes) == 0 {
			return clientUIResult{uiEvents: []string{proto.UIEventCopyModeHidden}}
		}
		return clientUIResult{}
	default:
		panic("unknown client UI action")
	}
}

func (st *clientUIState) reduceHandleLayout(action uiActionHandleLayout) clientUIResult {
	var result clientUIResult
	if action.structureChanged {
		if st.displayPanes != nil {
			st.displayPanes = nil
			result.uiEvents = append(result.uiEvents, proto.UIEventDisplayPanesHidden)
		}
		if st.paneDrag != nil {
			st.paneDrag = nil
		}
		if st.windowTabDrag != nil {
			st.windowTabDrag = nil
		}
		if st.chooser != nil {
			result.uiEvents = append(result.uiEvents, st.chooser.mode.hiddenEvent())
			st.chooser = nil
		}
		if st.windowRenamePrompt != nil {
			st.windowRenamePrompt = nil
		}
		if st.helpBar != nil {
			st.helpBar = nil
		}
		if st.message != "" {
			// Metadata-only layout refreshes are common (idle/CWD/branch updates).
			// Keep local feedback visible until the layout actually changes.
			st.message = ""
			result.uiEvents = append(result.uiEvents, proto.UIEventPrefixMessageHidden)
		}
	}
	st.dirty = true
	return result
}

func (st *clientUIState) reduceSetInputIdle(action uiActionSetInputIdle) clientUIResult {
	if st.inputIdle == action.idle {
		return clientUIResult{}
	}
	st.inputIdle = action.idle
	if action.idle {
		return clientUIResult{uiEvents: []string{proto.UIEventInputIdle}}
	}
	return clientUIResult{uiEvents: []string{proto.UIEventInputBusy}}
}

func (st *clientUIState) reduceShowChooser(action uiActionShowChooser) clientUIResult {
	var result clientUIResult
	if st.displayPanes != nil {
		st.displayPanes = nil
		result.uiEvents = append(result.uiEvents, proto.UIEventDisplayPanesHidden)
	}
	if st.paneDrag != nil {
		st.paneDrag = nil
	}
	if st.windowRenamePrompt != nil {
		st.windowRenamePrompt = nil
	}
	if st.helpBar != nil {
		st.helpBar = nil
	}
	previous := st.chooser
	st.chooser = action.chooser
	st.dirty = true
	if previous == nil || previous.mode != action.chooser.mode {
		if previous != nil {
			result.uiEvents = append(result.uiEvents, previous.mode.hiddenEvent())
		}
		result.uiEvents = append(result.uiEvents, action.chooser.mode.shownEvent())
	}
	return result
}

func (st *clientUIState) reduceShowWindowRenamePrompt(action uiActionShowWindowRenamePrompt) clientUIResult {
	result := clientUIResult{}
	if st.displayPanes != nil {
		st.displayPanes = nil
		result.uiEvents = append(result.uiEvents, proto.UIEventDisplayPanesHidden)
	}
	if st.paneDrag != nil {
		st.paneDrag = nil
	}
	if st.chooser != nil {
		result.uiEvents = append(result.uiEvents, st.chooser.mode.hiddenEvent())
		st.chooser = nil
	}
	if st.helpBar != nil {
		st.helpBar = nil
	}
	st.windowRenamePrompt = action.prompt
	st.dirty = true
	return result
}

func (st *clientUIState) reduceShowHelpBar(action uiActionShowHelpBar) clientUIResult {
	result := clientUIResult{}
	if st.displayPanes != nil {
		st.displayPanes = nil
		result.uiEvents = append(result.uiEvents, proto.UIEventDisplayPanesHidden)
	}
	if st.chooser != nil {
		result.uiEvents = append(result.uiEvents, st.chooser.mode.hiddenEvent())
		st.chooser = nil
	}
	if st.windowRenamePrompt != nil {
		st.windowRenamePrompt = nil
	}
	if st.paneDrag != nil {
		st.paneDrag = nil
	}
	st.helpBar = action.bar
	st.dirty = true
	return result
}

func (st *clientUIState) markRendered() {
	st.dirty = false
	st.fullRedraw = false
	st.dirtyPanes = make(map[uint32]struct{})
}

func (st *clientUIState) markPaneDirty(paneID uint32) {
	st.dirty = true
	if paneID == 0 {
		return
	}
	if st.dirtyPanes == nil {
		st.dirtyPanes = make(map[uint32]struct{})
	}
	st.dirtyPanes[paneID] = struct{}{}
}

func (st *clientUIState) captureUI() *proto.CaptureUI {
	chooser := ""
	if st.chooser != nil {
		chooser = string(st.chooser.mode)
	}
	prompt := ""
	if st.windowRenamePrompt != nil {
		prompt = st.windowRenamePrompt.title()
	}
	return &proto.CaptureUI{
		CopyMode:     len(st.copyModes) > 0,
		DisplayPanes: st.displayPanes != nil,
		Chooser:      chooser,
		Prompt:       prompt,
		InputIdle:    st.inputIdle,
	}
}
