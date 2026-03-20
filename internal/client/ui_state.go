package client

import (
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/proto"
)

type clientUIState struct {
	dirty        bool
	copyModes    map[uint32]*copymode.CopyMode
	displayPanes *displayPanesState
	chooser      *chooserState
	message      string
	inputIdle    bool
}

func newClientUIState() clientUIState {
	return clientUIState{
		copyModes: make(map[uint32]*copymode.CopyMode),
		inputIdle: true,
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

type uiActionPaneOutput struct{}

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
		st.message = ""
		st.dirty = true
		return clientUIResult{}
	case uiActionSetMessage:
		st.message = action.message
		st.dirty = true
		return clientUIResult{}
	case uiActionClearMessage:
		if st.message == "" {
			return clientUIResult{}
		}
		st.message = ""
		st.dirty = true
		return clientUIResult{}
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
		if st.chooser != nil {
			result.uiEvents = append(result.uiEvents, st.chooser.mode.hiddenEvent())
			st.chooser = nil
		}
	}
	st.message = ""
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

func (st *clientUIState) markRendered() {
	st.dirty = false
}

func (st *clientUIState) captureUI() *proto.CaptureUI {
	chooser := ""
	if st.chooser != nil {
		chooser = string(st.chooser.mode)
	}
	return &proto.CaptureUI{
		CopyMode:     len(st.copyModes) > 0,
		DisplayPanes: st.displayPanes != nil,
		Chooser:      chooser,
		InputIdle:    st.inputIdle,
	}
}
