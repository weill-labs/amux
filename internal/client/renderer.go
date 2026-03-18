// Package client provides the shared client-side rendering logic.
// It maintains per-pane terminal emulators and produces capture output
// (plain text, color map, JSON). The live rendering path (copy mode,
// dirty tracking) stays in the main package.
package client

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// Renderer manages client-side rendering state. It receives layout snapshots
// and raw pane output from the server, maintains local terminal emulators
// per pane, and uses the compositor to produce ANSI output.
type Renderer struct {
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
	windows      []proto.WindowSnapshot
	activeWinID  uint32

	// OnPaneResize is called during HandleLayout for each non-minimized pane
	// after its emulator is resized. The main package uses this to resize
	// copy mode instances. May be nil.
	OnPaneResize func(paneID uint32, w, h int)
}

// New creates a Renderer for the given terminal dimensions.
func New(width, height int) *Renderer {
	return &Renderer{
		emulators:  make(map[uint32]mux.TerminalEmulator),
		paneInfo:   make(map[uint32]proto.PaneSnapshot),
		compositor: render.NewCompositor(width, height, ""),
		width:      width,
		height:     height,
	}
}

// layoutFingerprint returns a string encoding the layout structure: pane IDs,
// positions, sizes, zoom state, and dimensions. Two layouts with the same
// fingerprint can be rendered without clearing the screen.
func (r *Renderer) layoutFingerprint() string {
	if r.layout == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d,%d,%d;", r.width, r.height, r.zoomedPaneID)
	r.layout.Walk(func(cell *mux.LayoutCell) {
		fmt.Fprintf(&b, "%d:%d,%d,%d,%d;", cell.CellPaneID(), cell.X, cell.Y, cell.W, cell.H)
	})
	return b.String()
}

// HandleLayout processes a layout snapshot from the server. Creates/removes
// emulators as panes appear/disappear, rebuilds the local layout tree, and
// resizes emulators to match their cells. Returns true if the layout structure
// changed (panes moved/resized/added/removed), false for metadata-only updates
// like focus changes.
func (r *Renderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldFP := r.layoutFingerprint()

	r.sessionName = snap.SessionName
	r.activePaneID = snap.ActivePaneID
	r.zoomedPaneID = snap.ZoomedPaneID

	// Collect all pane snapshots across all windows (or from legacy fields)
	allPanes := snap.Panes
	activeRoot := snap.Root
	if len(snap.Windows) > 0 {
		allPanes = nil
		r.windows = snap.Windows
		r.activeWinID = snap.ActiveWindowID
		for _, ws := range snap.Windows {
			allPanes = append(allPanes, ws.Panes...)
			if ws.ID == snap.ActiveWindowID {
				activeRoot = ws.Root
				r.activePaneID = ws.ActivePaneID
			}
		}
	}

	// Build map of current pane IDs from snapshot
	newPaneIDs := make(map[uint32]bool, len(allPanes))
	for _, ps := range allPanes {
		newPaneIDs[ps.ID] = true
		r.paneInfo[ps.ID] = ps
	}

	// Create emulators for new panes
	for _, ps := range allPanes {
		if _, exists := r.emulators[ps.ID]; !exists {
			var w, h int
			if ps.Minimized && ps.EmuWidth > 0 && ps.EmuHeight > 0 {
				// Use pre-minimize emulator dimensions so replayed
				// screen content isn't truncated into a tiny emulator.
				w, h = ps.EmuWidth, ps.EmuHeight
			} else {
				w, h = proto.FindPaneDimensions(snap, activeRoot, ps.ID, mux.PaneContentHeight)
			}
			r.emulators[ps.ID] = mux.NewVTEmulatorWithDrain(w, h)
		}
	}

	// Remove stale emulators (only remove panes that no longer exist in any window)
	for id := range r.emulators {
		if !newPaneIDs[id] {
			delete(r.emulators, id)
			delete(r.paneInfo, id)
		}
	}

	// Rebuild layout tree from the active window's root
	r.layout = mux.RebuildLayout(activeRoot)

	// Resize emulators to match their layout cells.
	// Minimized panes are skipped — their emulators stay at pre-minimize
	// dimensions so TUI app output is processed at the correct size.
	r.layout.Walk(func(cell *mux.LayoutCell) {
		if emu, ok := r.emulators[cell.PaneID]; ok {
			if info, ok := r.paneInfo[cell.PaneID]; ok && info.Minimized {
				return
			}
			contentH := mux.PaneContentHeight(cell.H)
			emu.Resize(cell.W, contentH)
			if r.OnPaneResize != nil {
				r.OnPaneResize(cell.PaneID, cell.W, contentH)
			}
		}
	})

	// Update dimensions and compositor
	r.width = snap.Width
	r.height = snap.Height + render.GlobalBarHeight
	r.compositor.SetSessionName(snap.SessionName)
	r.compositor.Resize(r.width, r.height)

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
		r.compositor.SetWindows(windows)
	}

	// When zoomed, resize the zoomed emulator to full window size
	if r.zoomedPaneID != 0 {
		if emu, ok := r.emulators[r.zoomedPaneID]; ok {
			layoutH := r.compositor.LayoutHeight()
			emu.Resize(r.width, mux.PaneContentHeight(layoutH))
		}
	}

	return r.layoutFingerprint() != oldFP
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (r *Renderer) HandlePaneOutput(paneID uint32, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if emu, ok := r.emulators[paneID]; ok {
		emu.Write(data)
	}
}

// Resize updates the client's terminal dimensions.
func (r *Renderer) Resize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = width
	r.height = height
	r.compositor.Resize(width, height)
}

// RenderFull produces ANSI output compositing all panes. The paneLookup
// function maps pane IDs to PaneData — the caller provides this so it can
// inject copy-mode overlays or other per-pane customization.
// Returns empty string if no layout is available.
//
// The lock is released before calling into the compositor so the paneLookup
// callback may safely call Emulator/PaneInfo without deadlocking.
// Callers must ensure RenderFull and HandleLayout/Resize are not called
// concurrently (the compositor is unprotected after unlock). In practice
// this is guaranteed: the interactive client calls both from the single
// renderCoalesced goroutine, and the headless client is sequential.
func (r *Renderer) RenderFull(paneLookup func(uint32) render.PaneData, clearScreen ...bool) string {
	r.mu.Lock()
	if r.layout == nil {
		r.mu.Unlock()
		return ""
	}

	root := r.layout
	activePaneID := r.activePaneID
	if r.zoomedPaneID != 0 {
		root = mux.NewLeafByID(r.zoomedPaneID, 0, 0, r.width, r.compositor.LayoutHeight())
	}
	comp := r.compositor
	r.mu.Unlock()

	return comp.RenderFull(root, activePaneID, paneLookup, clearScreen...)
}

// Capture renders the full composited screen from client-side emulators.
// If stripANSI is true, returns a plain-text grid preserving visual layout.
func (r *Renderer) Capture(stripANSI bool) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.layout == nil {
		return ""
	}

	root, activePaneID := r.captureRootLocked()
	raw := r.compositor.RenderFull(root, activePaneID, r.paneLookupLocked, true)

	if stripANSI {
		return render.MaterializeGrid(raw, r.width, r.height)
	}
	return raw
}

// CaptureColorMap renders a color map from client-side emulators.
func (r *Renderer) CaptureColorMap() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.layout == nil {
		return ""
	}

	root, activePaneID := r.captureRootLocked()
	raw := r.compositor.RenderFull(root, activePaneID, r.paneLookupLocked, true)
	return render.ExtractColorMap(raw, r.width, r.height) + "\n"
}

// CaptureJSON renders a structured JSON capture from client-side emulators.
// Agent status (idle, current_command, child_pids) comes from the server.
func (r *Renderer) CaptureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.layout == nil {
		return "{}"
	}

	root, _ := r.captureRootLocked()

	capture := proto.CaptureJSON{
		Session: r.sessionName,
		Width:   r.width,
		Height:  r.height,
	}
	for _, ws := range r.windows {
		if ws.ID == r.activeWinID {
			capture.Window = proto.CaptureWindow{
				ID: ws.ID, Name: ws.Name, Index: ws.Index,
			}
			break
		}
	}

	root.Walk(func(c *mux.LayoutCell) {
		paneID := c.CellPaneID()
		if paneID == 0 {
			return
		}
		cp, ok := r.buildCapturePaneLocked(paneID, agentStatus)
		if !ok {
			return
		}
		cp.Position = &proto.CapturePos{
			X: c.X, Y: c.Y, Width: c.W, Height: c.H,
		}
		capture.Panes = append(capture.Panes, cp)
	})

	out, _ := json.MarshalIndent(capture, "", "  ")
	return string(out)
}

// CapturePaneText returns a single pane's content from client-side emulators.
func (r *Renderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	emu, ok := r.emulators[paneID]
	if !ok {
		return ""
	}
	if includeANSI {
		return emu.Render()
	}
	return strings.Join(mux.EmulatorContentLines(emu), "\n")
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (r *Renderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	cp, ok := r.buildCapturePaneLocked(paneID, agentStatus)
	if !ok {
		return "{}"
	}
	out, _ := json.MarshalIndent(cp, "", "  ")
	return string(out)
}

// ResolvePaneID resolves a pane reference to an ID from client-side state.
func (r *Renderer) ResolvePaneID(ref string) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Try numeric ID
	if id, err := strconv.ParseUint(ref, 10, 32); err == nil {
		if _, ok := r.paneInfo[uint32(id)]; ok {
			return uint32(id)
		}
	}
	// Try name or prefix match
	var prefixMatch uint32
	for _, info := range r.paneInfo {
		if info.Name == ref {
			return info.ID
		}
		if strings.HasPrefix(info.Name, ref) {
			prefixMatch = info.ID
		}
	}
	return prefixMatch
}

// ActivePaneID returns the active pane ID. Thread-safe.
func (r *Renderer) ActivePaneID() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activePaneID
}

// Layout returns the current layout tree. Thread-safe.
func (r *Renderer) Layout() *mux.LayoutCell {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.layout
}

// Emulator returns the terminal emulator for the given pane. Thread-safe.
func (r *Renderer) Emulator(paneID uint32) (mux.TerminalEmulator, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	emu, ok := r.emulators[paneID]
	return emu, ok
}

// PaneInfo returns the pane snapshot for the given pane. Thread-safe.
func (r *Renderer) PaneInfo(paneID uint32) (proto.PaneSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.paneInfo[paneID]
	return info, ok
}

// captureRootLocked returns the layout root and active pane ID for capture.
// Caller must hold r.mu.
func (r *Renderer) captureRootLocked() (*mux.LayoutCell, uint32) {
	root := r.layout
	if r.zoomedPaneID != 0 {
		root = mux.NewLeafByID(r.zoomedPaneID, 0, 0, r.width, r.compositor.LayoutHeight())
	}
	return root, r.activePaneID
}

// buildCapturePaneLocked builds a CapturePane from emulator state for the given pane.
// Returns false if the pane or its emulator is not found. Caller must hold r.mu.
func (r *Renderer) buildCapturePaneLocked(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) (proto.CapturePane, bool) {
	emu, ok := r.emulators[paneID]
	if !ok {
		return proto.CapturePane{}, false
	}
	info, ok := r.paneInfo[paneID]
	if !ok {
		return proto.CapturePane{}, false
	}
	col, row := emu.CursorPosition()
	cp := proto.CapturePane{
		ID:        info.ID,
		Name:      info.Name,
		Active:    info.ID == r.activePaneID,
		Minimized: info.Minimized,
		Zoomed:    info.ID == r.zoomedPaneID,
		Host:      info.Host,
		Task:      info.Task,
		Color:     info.Color,
		Cursor: proto.CaptureCursor{
			Col:    col,
			Row:    row,
			Hidden: emu.CursorHidden(),
		},
		Content: mux.EmulatorContentLines(emu),
	}
	cp.ApplyAgentStatus(agentStatus)
	return cp, true
}

// paneLookupLocked returns a PaneData for the given pane ID using the basic
// adapter (no copy mode). Caller must hold r.mu.
func (r *Renderer) paneLookupLocked(paneID uint32) render.PaneData {
	emu, ok := r.emulators[paneID]
	if !ok {
		return nil
	}
	info, ok := r.paneInfo[paneID]
	if !ok {
		return nil
	}
	return &PaneData{Emu: emu, Info: info}
}

// HandleCaptureRequest processes capture args and returns a proto.Message
// with the rendered output. This is the shared implementation used by both
// the live client (main package) and the headless test client.
func (r *Renderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
	var includeANSI, colorMap, formatJSON bool
	var paneRef string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ansi":
			includeANSI = true
		case "--colors":
			colorMap = true
		case "--format":
			if i+1 < len(args) && args[i+1] == "json" {
				formatJSON = true
				i++ // consume "json"
			}
		default:
			paneRef = args[i]
		}
	}

	if (includeANSI && colorMap) || (includeANSI && formatJSON) || (colorMap && formatJSON) {
		return &proto.Message{Type: proto.MsgTypeCaptureResponse,
			CmdErr: "--ansi, --colors, and --format json are mutually exclusive"}
	}

	if paneRef != "" {
		if colorMap {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse,
				CmdErr: "--colors is only supported for full screen capture"}
		}
		paneID := r.ResolvePaneID(paneRef)
		if paneID == 0 {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse,
				CmdErr: fmt.Sprintf("pane %q not found", paneRef)}
		}
		var out string
		if formatJSON {
			out = r.CapturePaneJSON(paneID, agentStatus)
		} else {
			out = r.CapturePaneText(paneID, includeANSI)
		}
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out + "\n"}
	}

	var out string
	if formatJSON {
		out = r.CaptureJSON(agentStatus) + "\n"
	} else if colorMap {
		out = r.CaptureColorMap()
	} else {
		out = r.Capture(!includeANSI)
	}
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out}
}
