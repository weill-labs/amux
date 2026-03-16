package main

import (
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// ClientRenderer manages client-side rendering state. It embeds the shared
// Renderer (which handles emulators, layout, and capture) and adds copy mode,
// dirty tracking, and the coalesced render loop.
type ClientRenderer struct {
	renderer *client.Renderer

	mu        sync.Mutex
	dirty     bool
	copyModes map[uint32]*copymode.CopyMode // per-pane copy mode state (nil = not in copy mode)
}

// NewClientRenderer creates a client renderer for the given terminal dimensions.
func NewClientRenderer(width, height int) *ClientRenderer {
	cr := &ClientRenderer{
		renderer:  client.New(width, height),
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

// HandleLayout processes a layout snapshot from the server.
func (cr *ClientRenderer) HandleLayout(snap *proto.LayoutSnapshot) {
	cr.renderer.HandleLayout(snap)
	cr.mu.Lock()
	cr.dirty = true
	cr.mu.Unlock()
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (cr *ClientRenderer) HandlePaneOutput(paneID uint32, data []byte) {
	cr.renderer.HandlePaneOutput(paneID, data)
	cr.mu.Lock()
	cr.dirty = true
	cr.mu.Unlock()
}

// Render produces ANSI output compositing all panes. Returns empty if no layout.
func (cr *ClientRenderer) Render() string {
	cr.mu.Lock()
	cr.dirty = false
	cr.mu.Unlock()

	return cr.renderer.RenderFull(func(paneID uint32) render.PaneData {
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
	})
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

// renderCoalesced runs a select loop that reads messages from msgCh,
// updates the client renderer, and coalesces renders at ~60fps.
// Layout changes render immediately; pane output is debounced.
func (cr *ClientRenderer) renderCoalesced(msgCh <-chan *renderMsg, write func(string)) {
	var renderTimer *time.Timer
	var renderC <-chan time.Time

	doRender := func() {
		if data := cr.Render(); data != "" {
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
			switch msg.typ {
			case renderMsgLayout:
				cr.HandleLayout(msg.layout)
				// Layout changes render immediately
				if renderTimer != nil {
					renderTimer.Stop()
				}
				doRender()
			case renderMsgPaneOutput:
				cr.HandlePaneOutput(msg.paneID, msg.data)
				scheduleRender()
			case renderMsgCopyMode:
				cr.EnterCopyMode(msg.paneID)
				if renderTimer != nil {
					renderTimer.Stop()
				}
				doRender()
			case renderMsgBell:
				write("\x07")
			case renderMsgClipboard:
				write(string(msg.data))
			case renderMsgExit:
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

// renderMsg is an internal message type for the render coalescing loop.
type renderMsgType int

const (
	renderMsgLayout     renderMsgType = iota
	renderMsgPaneOutput
	renderMsgBell
	renderMsgClipboard
	renderMsgExit
	renderMsgCopyMode
)

type renderMsg struct {
	typ    renderMsgType
	layout *proto.LayoutSnapshot
	paneID uint32
	data   []byte
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
	cr.copyModes[paneID] = copymode.New(emu, w, h)
	cr.dirty = true
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

func (c *clientPaneData) ID() uint32        { return c.info.ID }
func (c *clientPaneData) Name() string      { return c.info.Name }
func (c *clientPaneData) Host() string      { return c.info.Host }
func (c *clientPaneData) Task() string      { return c.info.Task }
func (c *clientPaneData) Color() string     { return c.info.Color }
func (c *clientPaneData) Minimized() bool   { return c.info.Minimized }
func (c *clientPaneData) Idle() bool        { return c.info.Idle }
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
