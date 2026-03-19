package client

import (
	"os"
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

	mu           sync.Mutex
	dirty        bool
	copyModes    map[uint32]*copymode.CopyMode // per-pane copy mode state (nil = not in copy mode)
	displayPanes *displayPanesState
	chooser      *chooserState
	message      string
	OnUIEvent    func(string)
}

// NewClientRenderer creates a client renderer for the given terminal dimensions.
func NewClientRenderer(width, height int) *ClientRenderer {
	cr := &ClientRenderer{
		renderer:  New(width, height),
		copyModes: make(map[uint32]*copymode.CopyMode),
	}
	// Resize copy modes when the renderer resizes emulators during layout.
	cr.renderer.OnPaneResize = func(paneID uint32, w, h int) {
		cr.mu.Lock()
		cm := cr.copyModes[paneID]
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
	clearedDisplayPanes := false
	clearedChooser := ""
	cr.mu.Lock()
	if structureChanged && cr.displayPanes != nil {
		clearedDisplayPanes = true
		cr.displayPanes = nil
	}
	if structureChanged && cr.chooser != nil {
		clearedChooser = cr.chooser.mode.hiddenEvent()
		cr.chooser = nil
	}
	cr.message = ""
	cr.dirty = true
	cr.mu.Unlock()
	if clearedDisplayPanes {
		cr.emitUIEvent(proto.UIEventDisplayPanesHidden)
	}
	if clearedChooser != "" {
		cr.emitUIEvent(clearedChooser)
	}
	return structureChanged
}

func (cr *ClientRenderer) emitUIEvent(name string) {
	if cr.OnUIEvent != nil {
		cr.OnUIEvent(name)
	}
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (cr *ClientRenderer) HandlePaneOutput(paneID uint32, data []byte) {
	cr.renderer.HandlePaneOutput(paneID, data)
	cr.mu.Lock()
	if cr.message != "" {
		cr.message = ""
	}
	cr.dirty = true
	cr.mu.Unlock()
}

// Render produces ANSI output compositing all panes. Returns empty if no layout.
// When clearScreen is true, the terminal is fully erased before drawing (needed
// after layout changes). When false, content is overwritten in-place to avoid
// flicker during incremental updates like copy mode navigation.
func (cr *ClientRenderer) Render(clearScreen ...bool) string {
	cr.mu.Lock()
	cr.dirty = false
	cr.mu.Unlock()

	return cr.renderer.RenderFullWithOverlay(cr.paneLookup(), cr.overlayState(), clearScreen...)
}

// RenderDiff produces minimal ANSI output by diffing against the previous frame.
// This is the primary render path — no screen clearing, no flicker.
func (cr *ClientRenderer) RenderDiff() string {
	cr.mu.Lock()
	cr.dirty = false
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
		cm := cr.copyModes[paneID]
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.message = msg
	cr.dirty = true
}

func (cr *ClientRenderer) ClearPrefixMessage() {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.message == "" {
		return
	}
	cr.message = ""
	cr.dirty = true
}

func (cr *ClientRenderer) prefixMessage() string {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.message
}

// IsDirty returns true if there is new data to render.
func (cr *ClientRenderer) IsDirty() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.dirty
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
	return cr.renderer.CaptureJSON(agentStatus)
}

// CapturePaneText returns a single pane's content from client-side emulators.
func (cr *ClientRenderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	return cr.renderer.CapturePaneText(paneID, includeANSI)
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (cr *ClientRenderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	return cr.renderer.CapturePaneJSON(paneID, agentStatus)
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
)

// RenderMsg is an internal message type for the render coalescing loop.
type RenderMsg struct {
	Typ    RenderMsgType
	Layout *proto.LayoutSnapshot
	PaneID uint32
	Data   []byte
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

// EnterCopyMode enters copy mode for the given pane. Thread-safe.
func (cr *ClientRenderer) EnterCopyMode(paneID uint32) {
	emu, ok := cr.renderer.Emulator(paneID)
	if !ok {
		return
	}
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.copyModes[paneID] != nil {
		return // already in copy mode
	}
	w, h := emu.Size()
	_, curRow := emu.CursorPosition()
	cr.copyModes[paneID] = copymode.New(emu, w, h, curRow)
	cr.dirty = true
}

// CopyModeForPane returns the copy mode for the given pane, or nil. Thread-safe.
func (cr *ClientRenderer) CopyModeForPane(paneID uint32) *copymode.CopyMode {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.copyModes[paneID]
}

// InCopyMode reports whether the pane is currently in copy mode. Thread-safe.
func (cr *ClientRenderer) InCopyMode(paneID uint32) bool {
	return cr.CopyModeForPane(paneID) != nil
}

// ExitCopyMode exits copy mode for the given pane. Thread-safe.
func (cr *ClientRenderer) ExitCopyMode(paneID uint32) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	delete(cr.copyModes, paneID)
	cr.dirty = true
}

// ActiveCopyMode returns the copy mode for the active pane, or nil. Thread-safe.
func (cr *ClientRenderer) ActiveCopyMode() *copymode.CopyMode {
	activePaneID := cr.renderer.ActivePaneID()
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.copyModes[activePaneID]
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
		cr.dirty = true
		cr.mu.Unlock()
	}
	return action
}

// HandleCaptureRequest processes a capture request forwarded from the server.
// It renders from the client-side emulators and returns a response message.
func (cr *ClientRenderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
	return cr.renderer.HandleCaptureRequest(args, agentStatus)
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
