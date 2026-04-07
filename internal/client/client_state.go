package client

import (
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type clientSnapshot struct {
	baseHistory map[uint32][]proto.StyledLine
	ui          clientUIState
	copyBuffer  string
}

func newClientSnapshot() *clientSnapshot {
	return &clientSnapshot{
		baseHistory: make(map[uint32][]proto.StyledLine),
		ui:          newClientUIState(),
	}
}

func cloneClientSnapshot(prev *clientSnapshot) clientSnapshot {
	next := clientSnapshot{
		baseHistory: cloneBaseHistory(prev.baseHistory),
		ui:          cloneClientUIState(prev.ui),
		copyBuffer:  prev.copyBuffer,
	}
	return next
}

func cloneBaseHistory(src map[uint32][]proto.StyledLine) map[uint32][]proto.StyledLine {
	dst := make(map[uint32][]proto.StyledLine, len(src))
	for paneID, lines := range src {
		// Retained history is immutable once stored; snapshots can share the
		// styled-line slices and only copy the map header on UI-only updates.
		dst[paneID] = lines
	}
	return dst
}

func cloneClientUIState(src clientUIState) clientUIState {
	dst := src
	dst.copyModes = make(map[uint32]*copymode.CopyMode, len(src.copyModes))
	for paneID, mode := range src.copyModes {
		dst.copyModes[paneID] = mode
	}
	dst.dirtyPanes = make(map[uint32]struct{}, len(src.dirtyPanes))
	for paneID := range src.dirtyPanes {
		dst.dirtyPanes[paneID] = struct{}{}
	}
	dst.displayPanes = cloneDisplayPanesState(src.displayPanes)
	dst.chooser = cloneChooserState(src.chooser)
	dst.paneDrag = clonePaneDragOverlayState(src.paneDrag)
	dst.windowRenamePrompt = cloneWindowRenamePromptState(src.windowRenamePrompt)
	dst.helpBar = cloneHelpBarState(src.helpBar)
	return dst
}

func cloneChooserState(src *chooserState) *chooserState {
	if src == nil {
		return nil
	}
	dst := *src
	if src.windows != nil {
		dst.windows = append([]proto.WindowSnapshot(nil), src.windows...)
	}
	if src.items != nil {
		dst.items = append([]chooserItem(nil), src.items...)
	}
	return &dst
}

func cloneDisplayPanesState(src *displayPanesState) *displayPanesState {
	if src == nil {
		return nil
	}
	dst := &displayPanesState{
		labels:  append([]render.PaneOverlayLabel(nil), src.labels...),
		targets: make(map[byte]uint32, len(src.targets)),
	}
	for key, paneID := range src.targets {
		dst.targets[key] = paneID
	}
	return dst
}

func cloneWindowRenamePromptState(src *windowRenamePromptState) *windowRenamePromptState {
	if src == nil {
		return nil
	}
	dst := *src
	return &dst
}

func clonePaneDragOverlayState(src *paneDragOverlayState) *paneDragOverlayState {
	if src == nil {
		return nil
	}
	dst := *src
	if src.indicator != nil {
		indicator := *src.indicator
		dst.indicator = &indicator
	}
	return &dst
}

func cloneHelpBarState(src *helpBarState) *helpBarState {
	if src == nil {
		return nil
	}
	dst := *src
	if src.items != nil {
		dst.items = append(dst.items[:0:0], src.items...)
	}
	return &dst
}

func (cr *ClientRenderer) loadState() *clientSnapshot {
	return cr.state.Load()
}

func (cr *ClientRenderer) updateState(apply func(*clientSnapshot) clientUIResult) clientUIResult {
	for {
		prev := cr.loadState()
		next := cloneClientSnapshot(prev)
		result := apply(&next)
		if cr.state.CompareAndSwap(prev, &next) {
			return result
		}
	}
}

func updateClientStateValue[T any](cr *ClientRenderer, apply func(*clientSnapshot) (T, clientUIResult)) (T, clientUIResult) {
	for {
		prev := cr.loadState()
		next := cloneClientSnapshot(prev)
		value, result := apply(&next)
		if cr.state.CompareAndSwap(prev, &next) {
			return value, result
		}
	}
}

func (cr *ClientRenderer) markDirty() {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) copyBufferValue() string {
	return cr.loadState().copyBuffer
}
