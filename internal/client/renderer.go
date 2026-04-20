// Package client provides the shared client-side rendering logic.
// It maintains per-pane terminal emulators and produces capture output
// (plain text, color map, JSON). The live rendering path (copy mode,
// dirty tracking) stays in the main package.
package client

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// Renderer manages client-side rendering state. It receives layout snapshots
// and raw pane output from the server, maintains local terminal emulators
// per pane, and uses the compositor to produce ANSI output.
type Renderer struct {
	state           atomic.Pointer[rendererSnapshot]
	commands        chan rendererCommand
	scrollbackLines int
	closeOnce       sync.Once

	// OnPaneResize is called during HandleLayout after an emulator is resized.
	// The main package uses this to resize copy mode instances. May be nil.
	OnPaneResize func(paneID uint32, w, h int)
}

// NewWithScrollback creates a Renderer with an explicit retained scrollback limit.
func NewWithScrollback(width, height, scrollbackLines int) *Renderer {
	r := &Renderer{
		commands:        make(chan rendererCommand),
		scrollbackLines: scrollbackLines,
	}
	initial := newRendererSnapshot(width, height, scrollbackLines)
	r.state.Store(initial)
	go r.actorLoop(initial, width, height)
	return r
}

// Close shuts down the renderer actor loop and closes its pane emulators.
func (r *Renderer) Close() {
	r.closeOnce.Do(func() {
		r.withActor(func(st *rendererActorState) {
			for _, emu := range st.emulators {
				_ = emu.Close()
			}
		})
		close(r.commands)
	})
}

// HandleLayout processes a layout snapshot from the server. Creates/removes
// emulators as panes appear/disappear, rebuilds the local layout tree, and
// resizes emulators to match their cells. Returns true if the layout structure
// changed (panes moved/resized/added/removed), false for metadata-only updates
// like focus changes.
func (r *Renderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	return withRendererActorValue(r, func(st *rendererActorState) bool {
		prev := st.snapshot
		prevEmulators := st.emulators
		oldFP := prev.layoutFingerprint()

		next := &rendererSnapshot{
			paneInfo:        make(map[uint32]proto.PaneSnapshot),
			sessionName:     snap.SessionName,
			sessionNotice:   snap.Notice,
			capabilities:    prev.capabilities,
			activePaneID:    snap.ActivePaneID,
			zoomedPaneID:    snap.ZoomedPaneID,
			leadPaneID:      snap.LeadPaneID,
			width:           prev.width,
			height:          prev.height,
			activeWinID:     snap.ActiveWindowID,
			scrollbackLines: prev.scrollbackLines,
		}

		allPanes := snap.Panes
		activeRoot := snap.Root
		if len(snap.Windows) > 0 {
			next.windows = cloneWindowSnapshots(snap.Windows)
			allPanes = nil
			for _, ws := range snap.Windows {
				allPanes = append(allPanes, ws.Panes...)
				if ws.ID == snap.ActiveWindowID {
					activeRoot = ws.Root
					next.activePaneID = ws.ActivePaneID
				}
			}
		}
		next.paneOrder = make([]uint32, 0, len(allPanes))

		for _, ps := range allPanes {
			next.paneOrder = append(next.paneOrder, ps.ID)
			next.paneInfo[ps.ID] = ps
		}

		next.layout = mux.RebuildLayout(activeRoot)
		clientLayoutH := next.height - render.GlobalBarHeight
		if next.layout != nil && (snap.Width != next.width || snap.Height != clientLayoutH) {
			// Rescale the client-side layout before resizing emulators so wrap
			// and cursor metadata match this client's window, not the server's
			// max-size snapshot.
			next.layout.ResizeAll(next.width, clientLayoutH)
		}
		next.visiblePaneIDs = next.visiblePaneSet(clientLayoutH)
		nextEmulators := make(map[uint32]mux.TerminalEmulator, len(prevEmulators)+len(next.visiblePaneIDs))
		for _, ps := range allPanes {
			if emu := prevEmulators[ps.ID]; emu != nil {
				nextEmulators[ps.ID] = emu
				continue
			}
			if !next.paneInActiveLayout(ps.ID) {
				continue
			}
			w, h, ok := st.paneEmulatorDimensions(next, ps.ID)
			if !ok {
				continue
			}
			nextEmulators[ps.ID] = mux.NewVTEmulatorWithDrainAndScrollback(w, h, next.scrollbackLines)
		}

		// Close emulators for panes that no longer exist in the layout.
		// Each emulator has a drain goroutine that blocks on io.Pipe.Read;
		// without Close() the goroutine and pipe FDs leak.
		for id, emu := range prevEmulators {
			if _, exists := next.paneInfo[id]; exists {
				continue
			}
			delete(st.pendingPaneOutput, id)
			_ = emu.Close()
		}
		st.warmVisiblePanes(next, nextEmulators)
		r.resizeSnapshotEmulators(next, nextEmulators)

		st.compositor.SetSessionName(snap.SessionName)
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
			st.compositor.SetWindows(windows)
		} else {
			st.compositor.SetWindows(nil)
		}

		if next.zoomedPaneID != 0 {
			if emu := nextEmulators[next.zoomedPaneID]; emu != nil {
				layoutH := st.compositor.LayoutHeight()
				emu.Resize(next.width, mux.PaneContentHeight(layoutH))
			}
		}

		st.snapshot = next
		st.emulators = nextEmulators
		r.publishSnapshot(next)
		return next.layoutFingerprint() != oldFP
	})
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator when that
// pane is visible. Hidden panes buffer output and replay it lazily on demand.
func (r *Renderer) HandlePaneOutput(paneID uint32, data []byte) bool {
	return withRendererActorValue(r, func(st *rendererActorState) bool {
		if _, ok := st.snapshot.paneInfo[paneID]; !ok {
			return false
		}
		if !st.snapshot.paneVisible(paneID) {
			st.bufferPaneOutput(paneID, data)
			return false
		}
		emu := st.ensurePaneEmulator(paneID)
		if emu == nil {
			return false
		}
		st.warmPaneOutput(paneID, st.emulators)
		_, _ = emu.Write(data)
		return true
	})
}

type paneOutputRenderInfo struct {
	paneVisible   bool
	screenChanged bool
	cursorChanged bool
}

type terminalCursorState struct {
	col    int
	row    int
	hidden bool
}

func captureTerminalCursorState(emu mux.TerminalEmulator) terminalCursorState {
	col, row := emu.CursorPosition()
	return terminalCursorState{
		col:    col,
		row:    row,
		hidden: emu.CursorHidden(),
	}
}

func (r *Renderer) HandlePaneOutputInfo(paneID uint32, data []byte, trackCursor bool) paneOutputRenderInfo {
	return withRendererActorValue(r, func(st *rendererActorState) paneOutputRenderInfo {
		if _, ok := st.snapshot.paneInfo[paneID]; !ok {
			return paneOutputRenderInfo{}
		}
		if !st.snapshot.paneVisible(paneID) {
			st.bufferPaneOutput(paneID, data)
			return paneOutputRenderInfo{}
		}
		emu := st.ensurePaneEmulator(paneID)
		if emu == nil {
			return paneOutputRenderInfo{}
		}
		st.warmPaneOutput(paneID, st.emulators)

		var before terminalCursorState
		if trackCursor {
			before = captureTerminalCursorState(emu)
		}

		_, _ = emu.Write(data)

		info := paneOutputRenderInfo{
			paneVisible: true,
		}
		info.screenChanged = emu.DrainScreenChanges()
		if trackCursor {
			info.cursorChanged = before != captureTerminalCursorState(emu)
		}
		return info
	})
}

// Resize updates the client's terminal dimensions.
func (r *Renderer) Resize(width, height int) {
	r.withActor(func(st *rendererActorState) {
		prev := st.snapshot
		next := *prev
		next.width = width
		next.height = height
		if prev.layout != nil {
			next.layout = mux.CloneLayout(prev.layout)
			layoutH := height - render.GlobalBarHeight
			next.layout.ResizeAll(width, layoutH)
			next.visiblePaneIDs = next.visiblePaneSet(layoutH)
		} else {
			next.visiblePaneIDs = nil
		}
		st.compositor.Resize(width, height)
		r.resizeSnapshotEmulators(&next, st.emulators)
		st.snapshot = &next
		r.publishSnapshot(&next)
	})
}

// SetCapabilities stores the negotiated attach capabilities for this client.
func (r *Renderer) SetCapabilities(caps proto.ClientCapabilities) {
	r.withActor(func(st *rendererActorState) {
		next := *st.snapshot
		next.capabilities = caps
		st.snapshot = &next
		r.publishSnapshot(&next)
	})
}

// Capabilities returns the negotiated attach capabilities for this client.
func (r *Renderer) Capabilities() proto.ClientCapabilities {
	return r.loadSnapshot().capabilities
}

// ClearPrevGrid forces a full repaint on the next RenderDiff call.
func (r *Renderer) ClearPrevGrid() {
	r.withActor(func(st *rendererActorState) {
		st.compositor.ClearPrevGrid()
	})
}

func (r *Renderer) RenderFullWithOverlayStats(paneLookup func(*rendererActorState, uint32) render.PaneData, overlay render.OverlayState, clearScreen ...bool) (string, render.RenderStats) {
	type result struct {
		out   string
		stats render.RenderStats
	}
	res := withRendererActorValue(r, func(st *rendererActorState) result {
		snap := st.snapshot
		if snap.layout == nil {
			return result{}
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		overlay = r.mergeOverlay(snap, overlay)
		out, stats := st.compositor.RenderFullWithOverlayStats(root, activePaneID, func(paneID uint32) render.PaneData {
			return paneLookup(st, paneID)
		}, overlay, clearScreen...)
		r.drainVisiblePaneScreenChanges(st, root)
		return result{out: out, stats: stats}
	})
	return res.out, res.stats
}

func (r *Renderer) RenderDiffWithOverlayDirtyStats(
	paneLookup func(*rendererActorState, uint32) render.PaneData,
	overlay render.OverlayState,
	dirtyPanes map[uint32]struct{},
	fullRedraw bool,
) (string, render.RenderStats) {
	type result struct {
		out   string
		stats render.RenderStats
	}
	res := withRendererActorValue(r, func(st *rendererActorState) result {
		snap := st.snapshot
		if snap.layout == nil {
			return result{}
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		overlay = r.mergeOverlay(snap, overlay)
		out, stats := st.compositor.RenderDiffWithOverlayDirtyStats(root, activePaneID, func(paneID uint32) render.PaneData {
			return paneLookup(st, paneID)
		}, overlay, dirtyPanes, fullRedraw)
		r.drainVisiblePaneScreenChanges(st, root)
		return result{out: out, stats: stats}
	})
	return res.out, res.stats
}

func (r *Renderer) drainVisiblePaneScreenChanges(st *rendererActorState, root *mux.LayoutCell) {
	if root == nil {
		return
	}
	root.Walk(func(cell *mux.LayoutCell) {
		paneID := cell.CellPaneID()
		if paneID == 0 {
			return
		}
		if emu := st.emulators[paneID]; emu != nil {
			emu.DrainScreenChanges()
		}
	})
}

// Capture renders the full composited screen from client-side emulators.
// If stripANSI is true, returns a plain-text grid preserving visual layout.
func (r *Renderer) Capture(stripANSI bool) string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		snap := st.snapshot
		if snap.layout == nil {
			return ""
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		raw := st.compositor.RenderFullWithOverlay(root, activePaneID, func(paneID uint32) render.PaneData {
			return r.paneLookupSnapshot(st, snap, paneID)
		}, r.mergeOverlay(snap, render.OverlayState{}), true)
		if stripANSI {
			return render.MaterializeGrid(raw, snap.width, snap.height)
		}
		return raw
	})
}

// CaptureDisplay returns what the diff renderer thinks the terminal displays.
// This reads the compositor's prevGrid rather than re-rendering via RenderFull,
// so a diff between Capture() and CaptureDisplay() reveals exactly where the
// diff renderer diverges from ground truth.
func (r *Renderer) CaptureDisplay() string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		return st.compositor.PrevGridText()
	})
}

// CaptureColorMap renders a color map from client-side emulators.
func (r *Renderer) CaptureColorMap() string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		snap := st.snapshot
		if snap.layout == nil {
			return ""
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		raw := st.compositor.RenderFullWithOverlay(root, activePaneID, func(paneID uint32) render.PaneData {
			return r.paneLookupSnapshot(st, snap, paneID)
		}, r.mergeOverlay(snap, render.OverlayState{}), true)
		return render.ExtractColorMap(raw, snap.width, snap.height) + "\n"
	})
}

func marshalIndented(v any) string {
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

// captureJSONValue builds the structured JSON capture payload.
// Returns false when no layout is available.
func (r *Renderer) captureJSONValue(agentStatus map[uint32]proto.PaneAgentStatus) (proto.CaptureJSON, bool) {
	return r.captureJSONValueWithHistory(agentStatus, nil, false)
}

func (r *Renderer) captureJSONValueWithHistory(agentStatus map[uint32]proto.PaneAgentStatus, baseHistory map[uint32][]proto.StyledLine, includeHistory bool) (proto.CaptureJSON, bool) {
	capture := proto.CaptureJSON{}
	ok := false
	r.withActor(func(st *rendererActorState) {
		snap := st.snapshot
		if snap.layout == nil {
			ok = false
			return
		}

		root, _ := snap.captureRoot(snap.height - render.GlobalBarHeight)

		capture = proto.CaptureJSON{
			Session: snap.sessionName,
			Width:   snap.width,
			Height:  snap.height,
			Notice:  snap.sessionNotice,
		}
		for _, ws := range snap.windows {
			if ws.ID == snap.activeWinID {
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
			cp, ok := r.buildCapturePane(st, snap, paneID, agentStatus, includeHistory, baseHistory[paneID])
			if !ok {
				return
			}
			cp.Position = &proto.CapturePos{
				X: c.X, Y: c.Y, Width: c.W, Height: c.H,
			}
			capture.Panes = append(capture.Panes, cp)
		})

		ok = true
	})
	return capture, ok
}

// CaptureJSON renders a structured JSON capture from client-side emulators.
// Agent status (idle, current_command) comes from the server.
func (r *Renderer) CaptureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	capture, ok := r.captureJSONValue(agentStatus)
	if !ok {
		return caputil.JSONErrorOutput(false, "state_unavailable", "capture state is unavailable because no layout is ready")
	}
	return marshalIndented(capture)
}

func (r *Renderer) CaptureJSONWithHistory(agentStatus map[uint32]proto.PaneAgentStatus, baseHistory map[uint32][]proto.StyledLine) string {
	capture, ok := r.captureJSONValueWithHistory(agentStatus, baseHistory, true)
	if !ok {
		return caputil.JSONErrorOutput(false, "state_unavailable", "capture state is unavailable because no layout is ready")
	}
	return marshalIndented(capture)
}

// CapturePaneText returns a single pane's content from client-side emulators.
func (r *Renderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		snap := st.snapshot
		emu := st.ensurePaneEmulator(paneID)
		if emu == nil {
			return ""
		}
		st.warmPaneOutput(paneID, st.emulators)
		if includeANSI {
			return filterRenderedANSI(emu.Render(), snap.capabilities)
		}
		return strings.Join(caputil.TrimOuterBlankRows(mux.EmulatorContentLines(emu)), "\n")
	})
}

// capturePaneValue builds the structured JSON payload for a single pane.
// Returns false when the pane is not found.
func (r *Renderer) capturePaneValue(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) (proto.CapturePane, bool) {
	pane := proto.CapturePane{}
	ok := false
	r.withActor(func(st *rendererActorState) {
		pane, ok = r.buildCapturePane(st, st.snapshot, paneID, agentStatus, false, nil)
	})
	return pane, ok
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (r *Renderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	cp, ok := r.capturePaneValue(paneID, agentStatus)
	if !ok {
		return caputil.JSONErrorOutput(true, "state_unavailable", "pane capture state is unavailable")
	}
	return marshalIndented(cp)
}

// ResolvePaneID resolves a pane reference to an ID from client-side state.
func (r *Renderer) ResolvePaneID(ref string) (uint32, error) {
	snap := r.loadSnapshot()

	candidates := make([]mux.PaneRefCandidate, 0, len(snap.paneOrder))
	for _, paneID := range snap.paneOrder {
		info, ok := snap.paneInfo[paneID]
		if !ok {
			continue
		}
		candidates = append(candidates, mux.PaneRefCandidate{ID: info.ID, Name: info.Name})
	}
	return mux.ResolvePaneRef(ref, candidates)
}

// ActivePaneID returns the active pane ID. Thread-safe.
func (r *Renderer) ActivePaneID() uint32 {
	return r.loadSnapshot().activePaneID
}

// ActivePaneName returns the active pane's name, or "" if unknown. Thread-safe.
func (r *Renderer) ActivePaneName() string {
	snap := r.loadSnapshot()
	if info, ok := snap.paneInfo[snap.activePaneID]; ok {
		return info.Name
	}
	return ""
}

// Layout returns the current layout tree. Thread-safe.
func (r *Renderer) Layout() *mux.LayoutCell {
	return r.loadSnapshot().layout
}

// VisibleLayout returns the layout tree currently visible to the user.
// In zoom mode, this is a synthetic single-pane root for the zoomed pane.
func (r *Renderer) VisibleLayout() *mux.LayoutCell {
	snap := r.loadSnapshot()
	return snap.visibleLayout(snap.height - render.GlobalBarHeight)
}

// WindowSnapshots returns a copy of the current window snapshots and active
// window ID from the latest layout.
func (r *Renderer) WindowSnapshots() ([]proto.WindowSnapshot, uint32) {
	snap := r.loadSnapshot()
	return cloneWindowSnapshots(snap.windows), snap.activeWinID
}

func (r *Renderer) buildCapturePane(st *rendererActorState, snap *rendererSnapshot, paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus, includeHistory bool, baseHistory []proto.StyledLine) (proto.CapturePane, bool) {
	emu := st.ensurePaneEmulator(paneID)
	if emu == nil {
		return proto.CapturePane{}, false
	}
	st.warmPaneOutput(paneID, st.emulators)
	info, ok := snap.paneInfo[paneID]
	if !ok {
		return proto.CapturePane{}, false
	}
	state := emu.TerminalState()
	col, row := emu.CursorPosition()
	content := mux.EmulatorContentLines(emu)
	if includeHistory {
		buffer := capturePaneBufferSnapshotStyled(emu, proto.CloneStyledLines(baseHistory), snap.scrollbackLines)
		content = make([]string, 0, len(buffer.scrollback)+len(buffer.screen))
		for _, line := range buffer.scrollback {
			content = append(content, line.text)
		}
		for _, line := range buffer.screen {
			content = append(content, line.text)
		}
	}
	cp := caputil.BuildPane(caputil.PaneInput{
		ID:            info.ID,
		Name:          info.Name,
		Active:        info.ID == snap.activePaneID,
		Lead:          info.ID == snap.leadPaneID,
		Zoomed:        info.ID == snap.zoomedPaneID,
		Host:          info.Host,
		Task:          info.Task,
		Color:         info.Color,
		ColumnIndex:   info.ColumnIndex,
		ConnStatus:    info.ConnStatus,
		GitBranch:     info.GitBranch,
		PR:            info.PR,
		KV:            info.KV,
		TrackedPRs:    info.TrackedPRs,
		TrackedIssues: info.TrackedIssues,
		Cursor:        caputil.CursorFromState(col, row, emu.CursorHidden(), state),
		Terminal:      caputil.TerminalFromState(state),
		Content:       content,
	}, agentStatus)
	return cp, true
}

func (r *Renderer) paneLookupSnapshot(st *rendererActorState, snap *rendererSnapshot, paneID uint32) render.PaneData {
	emu, ok := st.emulators[paneID]
	if !ok {
		return nil
	}
	info, ok := snap.paneInfo[paneID]
	if !ok {
		return nil
	}
	return &clientPaneData{emu: emu, info: info, caps: snap.capabilities}
}

func (r *Renderer) mergeOverlay(snap *rendererSnapshot, overlay render.OverlayState) render.OverlayState {
	if overlay.Message == "" {
		overlay.Message = snap.sessionNotice
	}
	return overlay
}

func (r *Renderer) resizeSnapshotEmulators(next *rendererSnapshot, emulators map[uint32]mux.TerminalEmulator) {
	if next.layout != nil {
		next.layout.Walk(func(cell *mux.LayoutCell) {
			emu := emulators[cell.PaneID]
			if emu == nil {
				return
			}
			if cell.PaneID == next.zoomedPaneID {
				return
			}
			contentH := mux.PaneContentHeight(cell.H)
			emu.Resize(cell.W, contentH)
			if r.OnPaneResize != nil {
				r.OnPaneResize(cell.PaneID, cell.W, contentH)
			}
		})
	}
	if next.zoomedPaneID != 0 {
		if emu := emulators[next.zoomedPaneID]; emu != nil {
			layoutH := next.height - render.GlobalBarHeight
			contentH := mux.PaneContentHeight(layoutH)
			emu.Resize(next.width, contentH)
			if r.OnPaneResize != nil {
				r.OnPaneResize(next.zoomedPaneID, next.width, contentH)
			}
		}
	}
}

// HandleCaptureRequest processes capture args and returns a proto.Message
// with the rendered output. This is the shared implementation used by both
// the live client (main package) and the headless test client.
func (r *Renderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
	if isDebugFramesClientQuery(args) {
		return clientFrameStatsResponse(clientFrameStats{})
	}
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateScreenRequest(req); err != nil {
		return &proto.Message{Type: proto.MsgTypeCaptureResponse,
			CmdErr: err.Error()}
	}

	if req.DisplayMode {
		out := r.CaptureDisplay()
		if out == "" {
			out = "(no previous grid — diff renderer has not run yet)"
		}
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out + "\n"}
	}

	if req.PaneRef != "" {
		if req.ColorMap {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse,
				CmdErr: "--colors is only supported for full screen capture"}
		}
		paneID, err := r.ResolvePaneID(req.PaneRef)
		if err != nil {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse,
				CmdErr: err.Error()}
		}
		var out string
		if req.FormatJSON {
			out = r.CapturePaneJSON(paneID, agentStatus)
		} else {
			out = r.CapturePaneText(paneID, req.IncludeANSI)
		}
		return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out + "\n"}
	}

	var out string
	if req.FormatJSON {
		if req.HistoryMode {
			out = r.CaptureJSONWithHistory(agentStatus, nil) + "\n"
		} else {
			out = r.CaptureJSON(agentStatus) + "\n"
		}
	} else if req.ColorMap {
		out = r.CaptureColorMap()
	} else {
		out = r.Capture(!req.IncludeANSI)
	}
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out}
}
