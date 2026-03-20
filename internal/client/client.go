package client

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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

	mu          sync.Mutex
	baseHistory map[uint32][]string
	ui          clientUIState
	copyBuffer  string
	OnUIEvent   func(string)
}

// NewClientRenderer creates a client renderer for the given terminal dimensions.
func NewClientRenderer(width, height int) *ClientRenderer {
	cr := &ClientRenderer{
		renderer:    New(width, height),
		baseHistory: make(map[uint32][]string),
		ui:          newClientUIState(),
	}
	// Resize copy modes when the renderer resizes emulators during layout.
	cr.renderer.OnPaneResize = func(paneID uint32, w, h int) {
		cr.mu.Lock()
		cm := cr.ui.copyModes[paneID]
		cr.mu.Unlock()
		if cm != nil {
			cm.Resize(w, h)
		}
	}
	return cr
}

// HandleLayout processes a layout snapshot from the server. Returns true if the
// layout structure changed (panes moved/resized/added/removed).
func (cr *ClientRenderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	structureChanged := cr.renderer.HandleLayout(snap)
	validPanes := make(map[uint32]bool)
	for _, ps := range snap.Panes {
		validPanes[ps.ID] = true
	}
	for _, ws := range snap.Windows {
		for _, ps := range ws.Panes {
			validPanes[ps.ID] = true
		}
	}
	cr.mu.Lock()
	for paneID := range cr.baseHistory {
		if !validPanes[paneID] {
			delete(cr.baseHistory, paneID)
		}
	}
	result := cr.ui.reduce(uiActionHandleLayout{structureChanged: structureChanged})
	cr.mu.Unlock()
	cr.emitUIEvents(result.uiEvents)
	return structureChanged
}

// HandlePaneHistory stores retained server history for a pane during attach
// bootstrap. History is oldest-first and excludes the current visible screen.
func (cr *ClientRenderer) HandlePaneHistory(paneID uint32, lines []string) {
	history := append([]string(nil), lines...)
	cr.mu.Lock()
	cr.baseHistory[paneID] = history
	cr.mu.Unlock()
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.reduce(action)
}

func (cr *ClientRenderer) captureUIState() *proto.CaptureUI {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.captureUI()
}

func (cr *ClientRenderer) SetInputIdle(idle bool) {
	result := cr.reduceUI(uiActionSetInputIdle{idle: idle})
	cr.emitUIEvents(result.uiEvents)
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (cr *ClientRenderer) HandlePaneOutput(paneID uint32, data []byte) {
	cr.renderer.HandlePaneOutput(paneID, data)
	cr.reduceUI(uiActionPaneOutput{})
}

// Render produces ANSI output compositing all panes. Returns empty if no layout.
// When clearScreen is true, the terminal is fully erased before drawing (needed
// after layout changes). When false, content is overwritten in-place to avoid
// flicker during incremental updates like copy mode navigation.
func (cr *ClientRenderer) Render(clearScreen ...bool) string {
	cr.mu.Lock()
	cr.ui.markRendered()
	cr.mu.Unlock()

	return cr.renderer.RenderFullWithOverlay(cr.paneLookup(), cr.overlayState(), clearScreen...)
}

// RenderDiff produces minimal ANSI output by diffing against the previous frame.
// This is the primary render path — no screen clearing, no flicker.
func (cr *ClientRenderer) RenderDiff() string {
	cr.mu.Lock()
	cr.ui.markRendered()
	cr.mu.Unlock()

	return cr.renderer.RenderDiffWithOverlay(cr.paneLookup(), cr.overlayState())
}

// paneLookup returns a lookup function for pane data including copy mode.
func (cr *ClientRenderer) paneLookup() func(uint32) render.PaneData {
	return func(paneID uint32) render.PaneData {
		emu, ok := cr.renderer.Emulator(paneID)
		if !ok {
			return nil
		}
		info, ok := cr.renderer.PaneInfo(paneID)
		if !ok {
			return nil
		}
		cr.mu.Lock()
		cm := cr.ui.copyModes[paneID]
		cr.mu.Unlock()
		return &clientPaneData{emu: emu, info: info, cm: cm}
	}
}

func (cr *ClientRenderer) overlayState() render.OverlayState {
	return render.OverlayState{
		PaneLabels: cr.overlayLabels(),
		Chooser:    cr.chooserOverlay(),
		Message:    cr.prefixMessage(),
	}
}

func (cr *ClientRenderer) ShowPrefixMessage(msg string) {
	cr.reduceUI(uiActionSetMessage{message: msg})
}

func (cr *ClientRenderer) ClearPrefixMessage() {
	cr.reduceUI(uiActionClearMessage{})
}

func (cr *ClientRenderer) prefixMessage() string {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.message
}

// IsDirty returns true if there is new data to render.
func (cr *ClientRenderer) IsDirty() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.dirty
}

// Resize updates the client's terminal dimensions.
func (cr *ClientRenderer) Resize(width, height int) {
	cr.renderer.Resize(width, height)
}

// Capture renders the full composited screen from client-side emulators.
func (cr *ClientRenderer) Capture(stripANSI bool) string {
	return cr.renderer.Capture(stripANSI)
}

// CaptureDisplay returns what the diff renderer thinks the terminal displays.
func (cr *ClientRenderer) CaptureDisplay() string {
	return cr.renderer.CaptureDisplay()
}

// CaptureColorMap renders a color map from client-side emulators.
func (cr *ClientRenderer) CaptureColorMap() string {
	return cr.renderer.CaptureColorMap()
}

// CaptureJSON renders a structured JSON capture from client-side emulators.
func (cr *ClientRenderer) CaptureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(cr.renderer.CaptureJSON(agentStatus)), &capture); err != nil {
		return cr.renderer.CaptureJSON(agentStatus)
	}
	capture.UI = cr.captureUIState()
	out, _ := json.MarshalIndent(capture, "", "  ")
	return string(out)
}

// CapturePaneText returns a single pane's content from client-side emulators.
func (cr *ClientRenderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	return cr.renderer.CapturePaneText(paneID, includeANSI)
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (cr *ClientRenderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	base := cr.renderer.CapturePaneJSON(paneID, agentStatus)
	if strings.TrimSpace(base) == "{}" {
		return base
	}

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(base), &pane); err != nil {
		return base
	}
	pane.CopyMode = cr.InCopyMode(paneID)
	out, _ := json.MarshalIndent(pane, "", "  ")
	return string(out)
}

// ResolvePaneID resolves a pane reference to an ID from client-side state.
func (cr *ClientRenderer) ResolvePaneID(ref string) uint32 {
	return cr.renderer.ResolvePaneID(ref)
}

// ActivePaneID returns the active pane ID. Thread-safe.
func (cr *ClientRenderer) ActivePaneID() uint32 {
	return cr.renderer.ActivePaneID()
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
)

// RenderMsg is an internal message type for the render coalescing loop.
type RenderMsg struct {
	Typ    RenderMsgType
	Layout *proto.LayoutSnapshot
	PaneID uint32
	Data   []byte
	Text   string
}

// RenderCoalesced runs a select loop that reads messages from msgCh,
// updates the client renderer, and coalesces renders at ~60fps.
// Uses the diff renderer for flicker-free incremental updates. Layout
// changes that move/resize panes clear the previous grid to force a
// full repaint through the diff engine.
func (cr *ClientRenderer) RenderCoalesced(msgCh <-chan *RenderMsg, write func(string)) {
	var renderTimer *time.Timer
	var renderC <-chan time.Time

	useFull := os.Getenv("AMUX_RENDER") == "full"
	doRender := func() {
		var data string
		if useFull {
			data = cr.Render()
		} else {
			data = cr.RenderDiff()
		}
		if data != "" {
			write(data)
		}
		renderTimer = nil
		renderC = nil
	}

	scheduleRender := func() {
		if renderTimer == nil {
			renderTimer = time.NewTimer(16 * time.Millisecond)
			renderC = renderTimer.C
		}
	}

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			switch msg.Typ {
			case RenderMsgLayout:
				structureChanged := cr.HandleLayout(msg.Layout)
				// When pane positions/sizes changed, clear prevGrid so the
				// diff engine does a full repaint (no stale cells).
				if structureChanged {
					cr.renderer.ClearPrevGrid()
				}
				if renderTimer != nil {
					renderTimer.Stop()
				}
				doRender()
			case RenderMsgPaneOutput:
				cr.HandlePaneOutput(msg.PaneID, msg.Data)
				scheduleRender()
			case RenderMsgCopyMode:
				cr.EnterCopyMode(msg.PaneID)
				if renderTimer != nil {
					renderTimer.Stop()
				}
				doRender()
			case RenderMsgBell:
				write("\x07")
			case RenderMsgClipboard:
				write(string(msg.Data))
			case RenderMsgCmdError:
				if cr.ShowCommandError(msg.Text) {
					if renderTimer != nil {
						renderTimer.Stop()
					}
					write("\x07")
					doRender()
				}
			case RenderMsgExit:
				// Final render before exit
				if cr.IsDirty() {
					doRender()
				}
				return
			}
		case <-renderC:
			doRender()
		}
	}
}

func (cr *ClientRenderer) ShowCommandError(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	cr.reduceUI(uiActionSetMessage{message: text})
	return true
}

func (cr *ClientRenderer) ClearCommandFeedback() bool {
	cr.mu.Lock()
	changed := cr.ui.message != ""
	cr.ui.reduce(uiActionClearMessage{})
	cr.mu.Unlock()
	return changed
}

// EnterCopyMode enters copy mode for the given pane. Thread-safe.
func (cr *ClientRenderer) EnterCopyMode(paneID uint32) {
	emu, ok := cr.renderer.Emulator(paneID)
	if !ok {
		return
	}
	cr.mu.Lock()
	if cr.ui.copyModes[paneID] != nil {
		cr.mu.Unlock()
		return // already in copy mode
	}
	baseHistory := append([]string(nil), cr.baseHistory[paneID]...)
	cr.mu.Unlock()
	w, h := emu.Size()
	_, curRow := emu.CursorPosition()
	cm := copymode.New(&historyEmulator{
		emu:         emu,
		baseHistory: baseHistory,
	}, w, h, curRow)
	result := cr.reduceUI(uiActionEnterCopyMode{paneID: paneID, mode: cm})
	cr.emitUIEvents(result.uiEvents)
}

// CopyModeForPane returns the copy mode for the given pane, or nil. Thread-safe.
func (cr *ClientRenderer) CopyModeForPane(paneID uint32) *copymode.CopyMode {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.copyModes[paneID]
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.copyModes[activePaneID]
}

// VisibleLayout returns the layout tree currently visible to the user.
func (cr *ClientRenderer) VisibleLayout() *mux.LayoutCell {
	return cr.renderer.VisibleLayout()
}

// Emulator returns the emulator for the given pane. Thread-safe.
func (cr *ClientRenderer) Emulator(paneID uint32) (mux.TerminalEmulator, bool) {
	return cr.renderer.Emulator(paneID)
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
		cr.mu.Lock()
		cr.ui.dirty = true
		cr.mu.Unlock()
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
		cr.mu.Lock()
		cr.ui.dirty = true
		cr.mu.Unlock()
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
		cr.mu.Lock()
		cr.ui.dirty = true
		cr.mu.Unlock()
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

	cr.mu.Lock()
	if appendCopy {
		cr.copyBuffer += text
		text = cr.copyBuffer
	} else {
		cr.copyBuffer = text
	}
	cr.mu.Unlock()

	copyToClipboard(text)
}

// HandleCaptureRequest processes a capture request forwarded from the server.
// It renders from the client-side emulators and returns a response message.
func (cr *ClientRenderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
	var includeANSI, colorMap, formatJSON, displayMode bool
	var paneRef string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ansi":
			includeANSI = true
		case "--colors":
			colorMap = true
		case "--display":
			displayMode = true
		case "--format":
			if i+1 < len(args) && args[i+1] == "json" {
				formatJSON = true
				i++
			}
		default:
			paneRef = args[i]
		}
	}
	if !formatJSON || includeANSI || colorMap || displayMode {
		return cr.renderer.HandleCaptureRequest(args, agentStatus)
	}
	if paneRef != "" {
		paneID := cr.ResolvePaneID(paneRef)
		if paneID == 0 {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdErr: fmt.Sprintf("pane %q not found", paneRef)}
		}
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: cr.CapturePaneJSON(paneID, agentStatus) + "\n"}
	}
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: cr.CaptureJSON(agentStatus) + "\n"}
}

// clientPaneData adapts an emulator + snapshot metadata for the PaneData interface.
// This version includes copy mode overlay — the shared PaneData in the client
// package does not.
type clientPaneData struct {
	emu  mux.TerminalEmulator
	info proto.PaneSnapshot
	cm   *copymode.CopyMode // nil when not in copy mode
}

func (c *clientPaneData) RenderScreen(active bool) string {
	if c.cm != nil {
		return c.cm.RenderViewport()
	}
	if !active {
		return c.emu.RenderWithoutCursorBlock()
	}
	return c.emu.Render()
}

func (c *clientPaneData) CellAt(col, row int, active bool) render.ScreenCell {
	if c.cm != nil {
		return c.cm.CellAt(col, row)
	}
	cell := c.emu.CellAt(col, row)
	sc := render.CellFromUV(cell)
	if !active {
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

func (c *clientPaneData) CursorHidden() bool {
	if c.cm != nil {
		return true // copy mode manages its own cursor via reverse video
	}
	return c.emu.CursorHidden()
}

func (c *clientPaneData) HasCursorBlock() bool {
	if c.cm != nil {
		return false // copy mode renders its own reverse-video cursor
	}
	return c.emu.HasCursorBlock()
}

func (c *clientPaneData) ID() uint32         { return c.info.ID }
func (c *clientPaneData) Name() string       { return c.info.Name }
func (c *clientPaneData) Host() string       { return c.info.Host }
func (c *clientPaneData) Task() string       { return c.info.Task }
func (c *clientPaneData) Color() string      { return c.info.Color }
func (c *clientPaneData) Minimized() bool    { return c.info.Minimized }
func (c *clientPaneData) Idle() bool         { return c.info.Idle }
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
