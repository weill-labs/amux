package main

import (
	"sync"
	"time"

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
	sessionName  string
	compositor   *render.Compositor
	width        int // full terminal width
	height       int // full terminal height
	dirty        bool
}

// NewClientRenderer creates a client renderer for the given terminal dimensions.
func NewClientRenderer(width, height int) *ClientRenderer {
	return &ClientRenderer{
		emulators:  make(map[uint32]mux.TerminalEmulator),
		paneInfo:   make(map[uint32]proto.PaneSnapshot),
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
			// Find cell dimensions from the active window's root
			w, h := snap.Width, mux.PaneContentHeight(snap.Height)
			if cell := findCellInSnapshot(activeRoot, ps.ID); cell != nil {
				w = cell.W
				h = mux.PaneContentHeight(cell.H)
			} else if len(snap.Windows) > 0 {
				// Search other windows for this pane's dimensions
				for _, ws := range snap.Windows {
					if cell := findCellInSnapshot(ws.Root, ps.ID); cell != nil {
						w = cell.W
						h = mux.PaneContentHeight(cell.H)
						break
					}
				}
			}
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

	// Resize emulators to match their layout cells
	cr.layout.Walk(func(cell *mux.LayoutCell) {
		if emu, ok := cr.emulators[cell.PaneID]; ok {
			emu.Resize(cell.W, mux.PaneContentHeight(cell.H))
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
func (cr *ClientRenderer) Render() []byte {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.layout == nil {
		return nil
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
		return &clientPaneData{emu: emu, info: info}
	}

	return cr.compositor.RenderFull(cr.layout, cr.activePaneID, lookup)
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
func (cr *ClientRenderer) renderCoalesced(msgCh <-chan *renderMsg, write func([]byte)) {
	var renderTimer *time.Timer
	var renderC <-chan time.Time

	doRender := func() {
		if data := cr.Render(); data != nil {
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
			case renderMsgBell:
				write([]byte{0x07})
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
	renderMsgExit
)

type renderMsg struct {
	typ    renderMsgType
	layout *proto.LayoutSnapshot
	paneID uint32
	data   []byte
}

// clientPaneData adapts an emulator + snapshot metadata for the PaneData interface.
type clientPaneData struct {
	emu  mux.TerminalEmulator
	info proto.PaneSnapshot
}

func (c *clientPaneData) RenderScreen() string {
	return c.emu.Render()
}

func (c *clientPaneData) CursorPos() (col, row int) {
	return c.emu.CursorPosition()
}

func (c *clientPaneData) CursorHidden() bool {
	return c.emu.CursorHidden()
}

func (c *clientPaneData) ID() uint32      { return c.info.ID }
func (c *clientPaneData) Name() string    { return c.info.Name }
func (c *clientPaneData) Host() string    { return c.info.Host }
func (c *clientPaneData) Task() string    { return c.info.Task }
func (c *clientPaneData) Color() string   { return c.info.Color }
func (c *clientPaneData) Minimized() bool { return c.info.Minimized }

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
