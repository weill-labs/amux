package main

import (
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// ClientRenderer manages client-side rendering state. It receives layout
// snapshots and raw pane output from the server, maintains local terminal
// emulators per pane, and uses the compositor to produce ANSI output.
type ClientRenderer struct {
	mu           sync.Mutex
	emulators    map[uint32]mux.TerminalEmulator
	paneInfo     map[uint32]proto.PaneSnapshot
	layout       *mux.LayoutCell
	activePaneID uint32
	zoomedPaneID uint32
	sessionName  string
	compositor   *render.Compositor
	width        int // full terminal width
	height       int // full terminal height
	dirty        bool
	copyModes    map[uint32]*copymode.CopyMode // per-pane copy mode state (nil = not in copy mode)
}

// NewClientRenderer creates a client renderer for the given terminal dimensions.
func NewClientRenderer(width, height int) *ClientRenderer {
	return &ClientRenderer{
		emulators:  make(map[uint32]mux.TerminalEmulator),
		paneInfo:   make(map[uint32]proto.PaneSnapshot),
		copyModes:  make(map[uint32]*copymode.CopyMode),
		compositor: render.NewCompositor(width, height, ""),
		width:      width,
		height:     height,
	}
}

// HandleLayout processes a layout snapshot from the server. Creates/removes
// emulators as panes appear/disappear, rebuilds the local layout tree, and
// resizes emulators to match their cells.
func (cr *ClientRenderer) HandleLayout(snap *proto.LayoutSnapshot) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	cr.sessionName = snap.SessionName
	cr.activePaneID = snap.ActivePaneID
	cr.zoomedPaneID = snap.ZoomedPaneID

	// Collect all pane snapshots across all windows (or from legacy fields)
	allPanes := snap.Panes
	activeRoot := snap.Root
	if len(snap.Windows) > 0 {
		allPanes = nil
		for _, ws := range snap.Windows {
			allPanes = append(allPanes, ws.Panes...)
			if ws.ID == snap.ActiveWindowID {
				activeRoot = ws.Root
				cr.activePaneID = ws.ActivePaneID
			}
		}
	}

	// Build map of current pane IDs from snapshot
	newPaneIDs := make(map[uint32]bool, len(allPanes))
	for _, ps := range allPanes {
		newPaneIDs[ps.ID] = true
		cr.paneInfo[ps.ID] = ps
	}

	// Create emulators for new panes
	for _, ps := range allPanes {
		if _, exists := cr.emulators[ps.ID]; !exists {
			w, h := findPaneDimensions(snap, activeRoot, ps.ID)
			cr.emulators[ps.ID] = mux.NewVTEmulatorWithDrain(w, h)
		}
	}

	// Remove stale emulators (only remove panes that no longer exist in any window)
	for id := range cr.emulators {
		if !newPaneIDs[id] {
			delete(cr.emulators, id)
			delete(cr.paneInfo, id)
		}
	}

	// Rebuild layout tree from the active window's root
	cr.layout = mux.RebuildLayout(activeRoot)

	// Resize emulators (and active copy modes) to match their layout cells.
	// Minimized panes are skipped — their emulators stay at pre-minimize
	// dimensions so TUI app output is processed at the correct size.
	cr.layout.Walk(func(cell *mux.LayoutCell) {
		if emu, ok := cr.emulators[cell.PaneID]; ok {
			if info, ok := cr.paneInfo[cell.PaneID]; ok && info.Minimized {
				return
			}
			contentH := mux.PaneContentHeight(cell.H)
			emu.Resize(cell.W, contentH)
			if cm := cr.copyModes[cell.PaneID]; cm != nil {
				cm.Resize(cell.W, contentH)
			}
		}
	})

	// Update compositor
	cr.compositor.SetSessionName(snap.SessionName)
	cr.compositor.Resize(snap.Width, snap.Height+render.GlobalBarHeight)

	// Pass window info for the global bar
	if len(snap.Windows) > 0 {
		windows := make([]render.WindowInfo, len(snap.Windows))
		for i, ws := range snap.Windows {
			windows[i] = render.WindowInfo{
				Index:    ws.Index,
				Name:     ws.Name,
				IsActive: ws.ID == snap.ActiveWindowID,
				Panes:    len(ws.Panes),
			}
		}
		cr.compositor.SetWindows(windows)
	}

	// When zoomed, resize the zoomed emulator to full window size
	if cr.zoomedPaneID != 0 {
		if emu, ok := cr.emulators[cr.zoomedPaneID]; ok {
			layoutH := cr.compositor.LayoutHeight()
			emu.Resize(cr.width, mux.PaneContentHeight(layoutH))
		}
	}

	cr.dirty = true
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (cr *ClientRenderer) HandlePaneOutput(paneID uint32, data []byte) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if emu, ok := cr.emulators[paneID]; ok {
		emu.Write(data)
		cr.dirty = true
	}
}

// Render produces ANSI output compositing all panes. Returns nil if no layout.
func (cr *ClientRenderer) Render() string {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.layout == nil {
		return ""
	}

	cr.dirty = false

	lookup := func(paneID uint32) render.PaneData {
		emu, ok := cr.emulators[paneID]
		if !ok {
			return nil
		}
		info, ok := cr.paneInfo[paneID]
		if !ok {
			return nil
		}
		return &clientPaneData{emu: emu, info: info, cm: cr.copyModes[paneID]}
	}

	root := cr.layout
	if cr.zoomedPaneID != 0 {
		// When zoomed, create a temporary single-leaf layout at full window size
		root = mux.NewLeafByID(cr.zoomedPaneID, 0, 0, cr.width, cr.compositor.LayoutHeight())
	}

	return cr.compositor.RenderFull(root, cr.activePaneID, lookup)
}

// IsDirty returns true if there is new data to render.
func (cr *ClientRenderer) IsDirty() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.dirty
}

// Resize updates the client's terminal dimensions.
func (cr *ClientRenderer) Resize(width, height int) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.width = width
	cr.height = height
	cr.compositor.Resize(width, height)
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	emu, ok := cr.emulators[paneID]
	if !ok {
		return
	}
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.copyModes[cr.activePaneID]
}

// ActivePaneID returns the active pane ID. Thread-safe.
func (cr *ClientRenderer) ActivePaneID() uint32 {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.activePaneID
}

// clientPaneData adapts an emulator + snapshot metadata for the PaneData interface.
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

func (c *clientPaneData) ID() uint32      { return c.info.ID }
func (c *clientPaneData) Name() string    { return c.info.Name }
func (c *clientPaneData) Host() string    { return c.info.Host }
func (c *clientPaneData) Task() string    { return c.info.Task }
func (c *clientPaneData) Color() string   { return c.info.Color }
func (c *clientPaneData) Minimized() bool { return c.info.Minimized }
func (c *clientPaneData) InCopyMode() bool {
	return c.cm != nil
}

// findPaneDimensions returns the width and content height for a pane,
// searching the active window's root first, then all other windows.
// Falls back to the full snapshot dimensions if not found.
func findPaneDimensions(snap *proto.LayoutSnapshot, activeRoot proto.CellSnapshot, paneID uint32) (int, int) {
	if cell := findCellInSnapshot(activeRoot, paneID); cell != nil {
		return cell.W, mux.PaneContentHeight(cell.H)
	}
	for _, ws := range snap.Windows {
		if cell := findCellInSnapshot(ws.Root, paneID); cell != nil {
			return cell.W, mux.PaneContentHeight(cell.H)
		}
	}
	return snap.Width, mux.PaneContentHeight(snap.Height)
}

// findCellInSnapshot finds a cell by pane ID in a CellSnapshot tree.
func findCellInSnapshot(cs proto.CellSnapshot, paneID uint32) *proto.CellSnapshot {
	if cs.IsLeaf && cs.PaneID == paneID {
		return &cs
	}
	for i := range cs.Children {
		if found := findCellInSnapshot(cs.Children[i], paneID); found != nil {
			return found
		}
	}
	return nil
}
