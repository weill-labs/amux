package client

import (
	"os"
	"strings"
	"sync/atomic"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// ClientRenderer manages client-side rendering state. It embeds the shared
// Renderer (which handles emulators, layout, and capture) and adds copy mode,
// dirty tracking, and the coalesced render loop.
type ClientRenderer struct {
	renderer *Renderer

	state           atomic.Pointer[clientSnapshot]
	recentInputUnix atomic.Int64
	scrollbackLines int
	OnUIEvent       func(string)
	CopyToClipboard func(string) // called when copy mode copies text; nil uses default

	// Render timing — configurable for tests. Zero values use defaults.
	renderFrameInterval  time.Duration
	renderPriorityWindow time.Duration
}

// NewClientRendererWithScrollback creates a client renderer with an explicit
// retained scrollback limit shared by local emulators and copy mode.
func NewClientRendererWithScrollback(width, height, scrollbackLines int) *ClientRenderer {
	cr := &ClientRenderer{
		renderer:        NewWithScrollback(width, height, scrollbackLines),
		scrollbackLines: scrollbackLines,
	}
	cr.state.Store(newClientSnapshot())
	return cr
}

func (cr *ClientRenderer) SetCapabilities(caps proto.ClientCapabilities) {
	cr.renderer.SetCapabilities(caps)
}

func (cr *ClientRenderer) Capabilities() proto.ClientCapabilities {
	return cr.renderer.Capabilities()
}

// HandleLayout processes a layout snapshot from the server. Returns true if the
// layout structure changed (panes moved/resized/added/removed).
func (cr *ClientRenderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	structureChanged, result := cr.handleLayoutResult(snap)
	cr.emitUIEvents(result.uiEvents)
	return structureChanged
}
func (cr *ClientRenderer) handleLayoutResult(snap *proto.LayoutSnapshot) (bool, clientUIResult) {
	structureChanged := cr.renderer.HandleLayout(snap)
	cr.syncCopyModeSizes()
	validPanes := make(map[uint32]bool)
	for _, ps := range snap.Panes {
		validPanes[ps.ID] = true
	}
	for _, ws := range snap.Windows {
		for _, ps := range ws.Panes {
			validPanes[ps.ID] = true
		}
	}
	result := cr.updateState(func(next *clientSnapshot) clientUIResult {
		for paneID := range next.baseHistory {
			if !validPanes[paneID] {
				delete(next.baseHistory, paneID)
			}
		}
		return next.ui.reduce(uiActionHandleLayout{structureChanged: structureChanged})
	})
	return structureChanged, result
}

// HandlePaneHistory stores retained server history for a pane during attach
// bootstrap. History is oldest-first and excludes the current visible screen.
func (cr *ClientRenderer) HandlePaneHistory(paneID uint32, lines []string) {
	history := append([]string(nil), lines...)
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		next.baseHistory[paneID] = history
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) emitUIEvent(name string) {
	if cr.OnUIEvent != nil {
		cr.OnUIEvent(name)
	}
}

func (cr *ClientRenderer) emitUIEvents(names []string) {
	for _, name := range names {
		cr.emitUIEvent(name)
	}
}

func (cr *ClientRenderer) reduceUI(action any) clientUIResult {
	return cr.updateState(func(next *clientSnapshot) clientUIResult {
		return next.ui.reduce(action)
	})
}

func (cr *ClientRenderer) captureUIState() *proto.CaptureUI {
	return cr.loadState().ui.captureUI()
}

func (cr *ClientRenderer) SetInputIdle(idle bool) {
	result := cr.reduceUI(uiActionSetInputIdle{idle: idle})
	cr.emitUIEvents(result.uiEvents)
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (cr *ClientRenderer) HandlePaneOutput(paneID uint32, data []byte) {
	cr.renderer.HandlePaneOutput(paneID, data)
	result := cr.reduceUI(uiActionPaneOutput{paneID: paneID})
	cr.emitUIEvents(result.uiEvents)
}

// Render produces ANSI output compositing all panes. Returns empty if no layout.
// When clearScreen is true, the terminal is fully erased before drawing (needed
// after layout changes). When false, content is overwritten in-place to avoid
// flicker during incremental updates like copy mode navigation.
func (cr *ClientRenderer) Render(clearScreen ...bool) string {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		next.ui.markRendered()
		return clientUIResult{}
	})
	state := cr.loadState()
	return cr.renderer.RenderFullWithOverlay(cr.paneLookup(state), cr.overlayStateFromSnapshot(state), clearScreen...)
}

// RenderDiff produces minimal ANSI output by diffing against the previous frame.
// This is the primary render path — no screen clearing, no flicker.
func (cr *ClientRenderer) RenderDiff() string {
	type renderState struct {
		snapshot   *clientSnapshot
		dirtyPanes map[uint32]struct{}
		fullRedraw bool
	}
	state, _ := updateClientStateValue(cr, func(next *clientSnapshot) (renderState, clientUIResult) {
		dirtyPanes := cloneDirtyPanes(next.ui.dirtyPanes)
		result := renderState{
			snapshot:   next,
			dirtyPanes: dirtyPanes,
			fullRedraw: next.ui.fullRedraw || len(dirtyPanes) == 0,
		}
		next.ui.markRendered()
		return result, clientUIResult{}
	})
	return cr.renderer.RenderDiffWithOverlayDirty(
		cr.paneLookup(state.snapshot),
		cr.overlayStateFromSnapshot(state.snapshot),
		state.dirtyPanes,
		state.fullRedraw,
	)
}

// paneLookup returns a lookup function for pane data including copy mode.
func (cr *ClientRenderer) paneLookup(state *clientSnapshot) func(*rendererActorState, uint32) render.PaneData {
	return func(st *rendererActorState, paneID uint32) render.PaneData {
		emu, ok := st.emulators[paneID]
		if !ok {
			return nil
		}
		info, ok := st.snapshot.paneInfo[paneID]
		if !ok {
			return nil
		}
		cm := state.ui.copyModes[paneID]
		return &clientPaneData{
			emu:        emu,
			info:       info,
			cm:         cm,
			hideCursor: paneDragHidesCursor(state, st.snapshot.activePaneID, paneID),
			caps:       st.snapshot.capabilities,
		}
	}
}

func (cr *ClientRenderer) overlayState() render.OverlayState {
	return cr.overlayStateFromSnapshot(cr.loadState())
}

func (cr *ClientRenderer) overlayStateFromSnapshot(state *clientSnapshot) render.OverlayState {
	return render.OverlayState{
		PaneLabels:    cr.overlayLabelsFromSnapshot(state),
		DropIndicator: cr.paneDragIndicatorFromSnapshot(state),
		Chooser:       cr.chooserOverlayFromSnapshot(state),
		HelpBar:       state.ui.helpBar.renderOverlay(cr.renderer.loadSnapshot().width),
		TextInput:     cr.windowRenamePromptOverlayFromSnapshot(state),
		Message:       state.ui.message,
	}
}

func (cr *ClientRenderer) ShowPrefixMessage(msg string) {
	result := cr.reduceUI(uiActionSetMessage{message: msg})
	cr.emitUIEvents(result.uiEvents)
}

func (cr *ClientRenderer) ClearPrefixMessage() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.message != ""
		if !changed {
			return false, clientUIResult{}
		}
		return true, next.ui.reduce(uiActionClearMessage{})
	})
	if !changed {
		return false
	}
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) prefixMessage() string {
	return cr.loadState().ui.message
}

// IsDirty returns true if there is new data to render.
func (cr *ClientRenderer) IsDirty() bool {
	return cr.loadState().ui.dirty
}

// Resize updates the client's terminal dimensions.
func (cr *ClientRenderer) Resize(width, height int) {
	cr.renderer.Resize(width, height)
	cr.syncCopyModeSizes()
}

func (cr *ClientRenderer) RequestFullRedraw() {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		next.ui.dirty = true
		next.ui.fullRedraw = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) renderOverflowThreshold() int {
	snap := cr.renderer.loadSnapshot()
	area := snap.width * snap.height
	if area < 1 {
		area = 1
	}
	return area * 4
}

// CaptureJSON renders a structured JSON capture from client-side emulators.
func (cr *ClientRenderer) CaptureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	capture, ok := cr.renderer.captureJSONValue(agentStatus)
	if !ok {
		return caputil.JSONErrorOutput(false, "state_unavailable", "capture state is unavailable because no layout is ready")
	}
	capture.UI = cr.captureUIState()
	return marshalIndented(capture)
}

func (cr *ClientRenderer) CaptureJSONWithHistory(agentStatus map[uint32]proto.PaneAgentStatus) string {
	capture, ok := cr.renderer.captureJSONValueWithHistory(agentStatus, cr.loadState().baseHistory, true)
	if !ok {
		return caputil.JSONErrorOutput(false, "state_unavailable", "capture state is unavailable because no layout is ready")
	}
	capture.UI = cr.captureUIState()
	return marshalIndented(capture)
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (cr *ClientRenderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	pane, ok := cr.renderer.capturePaneValue(paneID, agentStatus)
	if !ok {
		return caputil.JSONErrorOutput(true, "state_unavailable", "pane capture state is unavailable")
	}
	pane.CopyMode = cr.InCopyMode(paneID)
	return marshalIndented(pane)
}

// ResolvePaneID resolves a pane reference to an ID from client-side state.
func (cr *ClientRenderer) ResolvePaneID(ref string) (uint32, error) {
	return cr.renderer.ResolvePaneID(ref)
}

// ActivePaneID returns the active pane ID. Thread-safe.
func (cr *ClientRenderer) ActivePaneID() uint32 {
	return cr.renderer.ActivePaneID()
}

// ActivePaneName returns the active pane's name, or "" if unknown. Thread-safe.
func (cr *ClientRenderer) ActivePaneName() string {
	return cr.renderer.ActivePaneName()
}

// Layout returns the current layout tree. Thread-safe.
func (cr *ClientRenderer) Layout() *mux.LayoutCell {
	return cr.renderer.Layout()
}

// RenderMsgType is the type tag for internal render messages.
type RenderMsgType int

const (
	RenderMsgLayout RenderMsgType = iota
	RenderMsgPaneOutput
	RenderMsgBell
	RenderMsgClipboard
	RenderMsgExit
	RenderMsgCopyMode
	RenderMsgCmdError
	RenderMsgLocalAction
)

// RenderMsg is an internal message type for the render coalescing loop.
type RenderMsg struct {
	Typ    RenderMsgType
	Layout *proto.LayoutSnapshot
	PaneID uint32
	Data   []byte
	Text   string
	Local  localRenderFunc
	Reply  chan any
}

type localRenderFunc func(*ClientRenderer) localRenderResult

type localRenderResult struct {
	effects []clientEffect
	value   any
}

type clientEffectKind int

const (
	clientEffectEmitUIEvents clientEffectKind = iota
	clientEffectClearPrevGrid
	clientEffectStopScheduledRender
	clientEffectScheduleRender
	clientEffectRenderNow
	clientEffectBell
	clientEffectWriteText
	clientEffectExit
)

type clientEffect struct {
	kind     clientEffectKind
	text     string
	uiEvents []string
}

type clientRenderLoopState struct {
	renderTimer         *time.Timer
	renderC             <-chan time.Time
	useFull             bool
	lastRender          time.Time
	renderFrameInterval time.Duration
	pendingOutputBytes  int
	forceFullRedraw     bool
}

func (st *clientRenderLoopState) stopScheduledRender() {
	if st.renderTimer == nil {
		return
	}
	st.renderTimer.Stop()
	st.renderTimer = nil
	st.renderC = nil
}

func (st *clientRenderLoopState) shouldRenderNow() bool {
	if st.renderTimer != nil {
		return false
	}
	if st.lastRender.IsZero() {
		return true
	}
	return time.Since(st.lastRender) >= st.renderFrameInterval
}

func (st *clientRenderLoopState) recordPaneOutput(bytes, threshold int) bool {
	if bytes > 0 {
		st.pendingOutputBytes += bytes
	}
	if threshold > 0 && st.pendingOutputBytes > threshold {
		st.forceFullRedraw = true
	}
	return st.forceFullRedraw
}

func (st *clientRenderLoopState) scheduleRender() {
	if st.renderTimer != nil {
		return
	}
	delay := st.renderFrameInterval
	if !st.lastRender.IsZero() {
		delay -= time.Since(st.lastRender)
		if delay < 0 {
			delay = 0
		}
	}
	st.renderTimer = time.NewTimer(delay)
	st.renderC = st.renderTimer.C
}

func appendUIEventEffect(effects []clientEffect, uiEvents []string) []clientEffect {
	if len(uiEvents) == 0 {
		return effects
	}
	return append(effects, clientEffect{
		kind:     clientEffectEmitUIEvents,
		uiEvents: uiEvents,
	})
}

func appendStopAndRenderNow(effects []clientEffect) []clientEffect {
	return append(effects,
		clientEffect{kind: clientEffectStopScheduledRender},
		clientEffect{kind: clientEffectRenderNow},
	)
}

func (cr *ClientRenderer) handleRenderMsg(msg *RenderMsg) []clientEffect {
	switch msg.Typ {
	case RenderMsgLayout:
		structureChanged, result := cr.handleLayoutResult(msg.Layout)
		effects := appendUIEventEffect(nil, result.uiEvents)
		if structureChanged {
			effects = append(effects, clientEffect{kind: clientEffectClearPrevGrid})
		}
		return appendStopAndRenderNow(effects)
	case RenderMsgPaneOutput:
		cr.HandlePaneOutput(msg.PaneID, msg.Data)
		if cr.shouldPrioritizePaneOutput(msg.PaneID) {
			return appendStopAndRenderNow(nil)
		}
		return []clientEffect{{kind: clientEffectScheduleRender}}
	case RenderMsgCopyMode:
		result := cr.enterCopyModeResult(msg.PaneID)
		return appendStopAndRenderNow(appendUIEventEffect(nil, result.uiEvents))
	case RenderMsgBell:
		return []clientEffect{{kind: clientEffectBell}}
	case RenderMsgClipboard:
		return []clientEffect{{
			kind: clientEffectWriteText,
			text: string(msg.Data),
		}}
	case RenderMsgCmdError:
		if !cr.ShowCommandError(msg.Text) {
			return nil
		}
		return []clientEffect{
			{kind: clientEffectStopScheduledRender},
			{kind: clientEffectBell},
			{kind: clientEffectRenderNow},
		}
	case RenderMsgExit:
		var effects []clientEffect
		if cr.IsDirty() {
			effects = append(effects, clientEffect{kind: clientEffectRenderNow})
		}
		effects = append(effects, clientEffect{kind: clientEffectExit})
		return effects
	default:
		return nil
	}
}

func (cr *ClientRenderer) handleLocalRenderMsg(state *clientRenderLoopState, msg *RenderMsg, write func(string)) bool {
	if msg.Local == nil {
		if msg.Reply != nil {
			msg.Reply <- nil
		}
		return false
	}
	result := msg.Local(cr)
	if cr.executeRenderEffects(state, result.effects, write) {
		if msg.Reply != nil {
			msg.Reply <- result.value
		}
		return true
	}
	if msg.Reply != nil {
		msg.Reply <- result.value
	}
	return false
}

func (cr *ClientRenderer) executeRenderEffects(state *clientRenderLoopState, effects []clientEffect, write func(string)) bool {
	for _, effect := range effects {
		switch effect.kind {
		case clientEffectEmitUIEvents:
			cr.emitUIEvents(effect.uiEvents)
		case clientEffectClearPrevGrid:
			cr.renderer.ClearPrevGrid()
		case clientEffectStopScheduledRender:
			state.stopScheduledRender()
		case clientEffectScheduleRender:
			if state.shouldRenderNow() {
				cr.renderNow(state, write)
			} else {
				state.scheduleRender()
			}
		case clientEffectRenderNow:
			cr.renderNow(state, write)
		case clientEffectBell:
			write("\x07")
		case clientEffectWriteText:
			write(effect.text)
		case clientEffectExit:
			return true
		}
	}
	return false
}

func (cr *ClientRenderer) renderNow(state *clientRenderLoopState, write func(string)) {
	var data string
	if state.useFull {
		data = cr.Render()
	} else {
		data = cr.RenderDiff()
	}
	if data != "" {
		write(wrapSynchronizedFrame(data))
	}
	state.lastRender = time.Now()
	state.renderTimer = nil
	state.renderC = nil
	state.pendingOutputBytes = 0
	state.forceFullRedraw = false
}

func (cr *ClientRenderer) MarkLocalInput() {
	cr.recentInputUnix.Store(time.Now().UnixNano())
}

func (cr *ClientRenderer) shouldPrioritizePaneOutput(paneID uint32) bool {
	if paneID == 0 || paneID != cr.ActivePaneID() {
		return false
	}
	ts := cr.recentInputUnix.Load()
	if ts == 0 {
		return false
	}
	return time.Since(time.Unix(0, ts)) <= cr.renderPriorityWindow
}

// RenderCoalesced runs a select loop that reads messages from msgCh,
// updates the client renderer, and coalesces renders at ~60fps.
// Uses the diff renderer for flicker-free incremental updates. Layout
// changes that move/resize panes clear the previous grid to force a
// full repaint through the diff engine.
func (cr *ClientRenderer) RenderCoalesced(msgCh <-chan *RenderMsg, write func(string)) {
	if cr.renderPriorityWindow == 0 {
		cr.renderPriorityWindow = config.RenderPriorityWindow
	}
	frameInterval := cr.renderFrameInterval
	if frameInterval == 0 {
		frameInterval = config.RenderFrameInterval
	}
	state := &clientRenderLoopState{
		useFull:             os.Getenv("AMUX_RENDER") == "full",
		renderFrameInterval: frameInterval,
	}

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			if msg.Typ == RenderMsgLocalAction {
				if cr.handleLocalRenderMsg(state, msg, write) {
					return
				}
				continue
			}
			if msg.Typ == RenderMsgPaneOutput && state.recordPaneOutput(len(msg.Data), cr.renderOverflowThreshold()) {
				cr.RequestFullRedraw()
			}
			if cr.executeRenderEffects(state, cr.handleRenderMsg(msg), write) {
				return
			}
		case <-state.renderC:
			cr.renderNow(state, write)
		}
	}
}

func wrapSynchronizedFrame(data string) string {
	if data == "" {
		return ""
	}
	return render.SynchronizedUpdateBegin + data + render.SynchronizedUpdateEnd
}

func cloneDirtyPanes(src map[uint32]struct{}) map[uint32]struct{} {
	dst := make(map[uint32]struct{}, len(src))
	for paneID := range src {
		dst[paneID] = struct{}{}
	}
	return dst
}

func (cr *ClientRenderer) syncCopyModeSizes() {
	state := cr.loadState()
	for paneID, cm := range state.ui.copyModes {
		w, h, ok := cr.renderer.PaneSize(paneID)
		if !ok || cm == nil {
			continue
		}
		cm.Resize(w, h)
	}
}

func (cr *ClientRenderer) ShowCommandError(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	result := cr.reduceUI(uiActionSetMessage{message: text})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) ClearCommandFeedback() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.message != ""
		if !changed {
			return false, clientUIResult{}
		}
		return true, next.ui.reduce(uiActionClearMessage{})
	})
	if !changed {
		return false
	}
	cr.emitUIEvents(result.uiEvents)
	return true
}

// EnterCopyMode enters copy mode for the given pane. Thread-safe.
func (cr *ClientRenderer) EnterCopyMode(paneID uint32) {
	result := cr.enterCopyModeResult(paneID)
	cr.emitUIEvents(result.uiEvents)
}

func (cr *ClientRenderer) enterCopyModeResult(paneID uint32) clientUIResult {
	state := cr.loadState()
	if state.ui.copyModes[paneID] != nil {
		return clientUIResult{} // already in copy mode
	}
	buffer, ok := cr.renderer.PaneBufferSnapshot(paneID, state.baseHistory[paneID])
	if !ok {
		return clientUIResult{}
	}
	w, h := buffer.Size()
	cm := copymode.New(buffer, w, h, buffer.cursorRow)
	return cr.reduceUI(uiActionEnterCopyMode{paneID: paneID, mode: cm})
}

// CopyModeForPane returns the copy mode for the given pane, or nil. Thread-safe.
func (cr *ClientRenderer) CopyModeForPane(paneID uint32) *copymode.CopyMode {
	return cr.loadState().ui.copyModes[paneID]
}

// InCopyMode reports whether the pane is currently in copy mode. Thread-safe.
func (cr *ClientRenderer) InCopyMode(paneID uint32) bool {
	return cr.CopyModeForPane(paneID) != nil
}

// ExitCopyMode exits copy mode for the given pane. Thread-safe.
func (cr *ClientRenderer) ExitCopyMode(paneID uint32) {
	result := cr.reduceUI(uiActionExitCopyMode{paneID: paneID})
	cr.emitUIEvents(result.uiEvents)
}

// ActiveCopyMode returns the copy mode for the active pane, or nil. Thread-safe.
func (cr *ClientRenderer) ActiveCopyMode() *copymode.CopyMode {
	activePaneID := cr.renderer.ActivePaneID()
	return cr.loadState().ui.copyModes[activePaneID]
}

// VisibleLayout returns the layout tree currently visible to the user.
func (cr *ClientRenderer) VisibleLayout() *mux.LayoutCell {
	return cr.renderer.VisibleLayout()
}

// WheelScrollCopyMode scrolls a pane already in copy mode without moving its cursor.
func (cr *ClientRenderer) WheelScrollCopyMode(paneID uint32, lines int, up bool) copymode.Action {
	cm := cr.CopyModeForPane(paneID)
	if cm == nil {
		return copymode.ActionNone
	}

	var action copymode.Action
	if up {
		action = cm.WheelScrollUp(lines)
	} else {
		action = cm.WheelScrollDown(lines)
	}

	switch action {
	case copymode.ActionExit:
		cr.ExitCopyMode(paneID)
	case copymode.ActionRedraw:
		cr.markDirty()
	}
	return action
}

// CopyModeSetCursor moves the copy-mode cursor to a viewport-relative position.
func (cr *ClientRenderer) CopyModeSetCursor(paneID uint32, col, row int) copymode.Action {
	cm := cr.CopyModeForPane(paneID)
	if cm == nil {
		return copymode.ActionNone
	}
	action := cm.SetCursor(col, row)
	if action == copymode.ActionRedraw {
		cr.markDirty()
	}
	return action
}

// CopyModeStartSelection begins a character selection at the current cursor.
func (cr *ClientRenderer) CopyModeStartSelection(paneID uint32) copymode.Action {
	cm := cr.CopyModeForPane(paneID)
	if cm == nil {
		return copymode.ActionNone
	}
	action := cm.StartSelection()
	if action == copymode.ActionRedraw {
		cr.markDirty()
	}
	return action
}

// CopyModeCopySelection copies the current selection and exits copy mode.
func (cr *ClientRenderer) CopyModeCopySelection(paneID uint32) {
	cm := cr.CopyModeForPane(paneID)
	if cm == nil {
		return
	}
	cr.copyModeCopy(cm)
	cr.ExitCopyMode(paneID)
}

func (cr *ClientRenderer) copyModeCopy(cm *copymode.CopyMode) {
	text, appendCopy := cm.ConsumeCopyText()
	if text == "" {
		text = cm.SelectedText()
	}
	if text == "" {
		return
	}

	text, _ = updateClientStateValue(cr, func(next *clientSnapshot) (string, clientUIResult) {
		if appendCopy {
			next.copyBuffer += text
			return next.copyBuffer, clientUIResult{}
		}
		next.copyBuffer = text
		return text, clientUIResult{}
	})

	if cr.CopyToClipboard != nil {
		cr.CopyToClipboard(text)
	} else {
		copyToClipboardLocal(defaultClipboardDeps(), text)
	}
}

func (cr *ClientRenderer) CopyBuffer() string {
	return cr.copyBufferValue()
}

// HandleCaptureRequest processes a capture request forwarded from the server.
// It renders from the client-side emulators and returns a response message.
func (cr *ClientRenderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
	req := caputil.ParseArgs(args)
	if !req.FormatJSON || req.IncludeANSI || req.ColorMap || req.DisplayMode {
		return cr.renderer.HandleCaptureRequest(args, agentStatus)
	}
	if req.PaneRef != "" {
		paneID, err := cr.ResolvePaneID(req.PaneRef)
		if err != nil {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdErr: err.Error()}
		}
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: cr.CapturePaneJSON(paneID, agentStatus) + "\n"}
	}
	if req.HistoryMode {
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: cr.CaptureJSONWithHistory(agentStatus) + "\n"}
	}
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: cr.CaptureJSON(agentStatus) + "\n"}
}

// clientPaneData adapts an emulator + snapshot metadata for the render.PaneData
// interface, including optional copy mode overlay.
type clientPaneData struct {
	emu        mux.TerminalEmulator
	info       proto.PaneSnapshot
	cm         *copymode.CopyMode // nil when not in copy mode
	hideCursor bool
	caps       proto.ClientCapabilities
}

func (c *clientPaneData) suppressCursor(active bool) bool {
	return !active || c.hideCursor
}

func (c *clientPaneData) localCursorHidden() bool {
	return c.cm != nil || c.hideCursor
}

func (c *clientPaneData) RenderScreen(active bool) string {
	var rendered string
	if c.cm != nil {
		rendered = render.RenderPaneViewportANSI(c.cm.ViewportWidth(), c.cm.ViewportHeight(), active, c)
	} else if c.suppressCursor(active) {
		rendered = c.emu.RenderWithoutCursorBlock()
	} else {
		rendered = c.emu.Render()
	}
	return filterRenderedANSI(rendered, c.caps)
}

func (c *clientPaneData) CellAt(col, row int, active bool) render.ScreenCell {
	if c.cm != nil {
		return render.ScreenCellFromCopyMode(c.cm.ViewportCellAt(col, row))
	}
	cell := c.emu.CellAt(col, row)
	sc := render.CellFromUV(cell)
	if c.suppressCursor(active) {
		stripCursorBlock(&sc, c.emu, col, row)
	}
	return sc
}

func (c *clientPaneData) CursorPos() (col, row int) {
	if c.cm != nil {
		return c.cm.CursorPos()
	}
	return c.emu.CursorPosition()
}

func (c *clientPaneData) CopyModeOverlay() *proto.ViewportOverlay {
	if c.cm != nil {
		return c.cm.ViewportOverlay()
	}
	return nil
}

func (c *clientPaneData) CursorHidden() bool {
	if c.localCursorHidden() {
		return true // copy mode and pane drag manage cursor visibility locally
	}
	return c.emu.CursorHidden()
}

func (c *clientPaneData) HasCursorBlock() bool {
	if c.localCursorHidden() {
		return false // local cursor suppression strips any emulator cursor block
	}
	return c.emu.HasCursorBlock()
}

func (c *clientPaneData) ID() uint32   { return c.info.ID }
func (c *clientPaneData) Name() string { return c.info.Name }
func (c *clientPaneData) TrackedPRs() []proto.TrackedPR {
	return proto.CloneTrackedPRs(c.info.TrackedPRs)
}
func (c *clientPaneData) TrackedIssues() []proto.TrackedIssue {
	return proto.CloneTrackedIssues(c.info.TrackedIssues)
}
func (c *clientPaneData) Host() string       { return c.info.Host }
func (c *clientPaneData) Task() string       { return c.info.Task }
func (c *clientPaneData) Color() string      { return c.info.Color }
func (c *clientPaneData) Idle() bool         { return c.info.Idle }
func (c *clientPaneData) IsLead() bool       { return c.info.Lead }
func (c *clientPaneData) ConnStatus() string { return c.info.ConnStatus }
func (c *clientPaneData) InCopyMode() bool {
	return c.cm != nil
}
func (c *clientPaneData) CopyModeSearch() string {
	if c.cm != nil {
		return c.cm.SearchBarText()
	}
	return ""
}
