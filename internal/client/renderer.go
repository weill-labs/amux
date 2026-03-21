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

	// OnPaneResize is called during HandleLayout for each non-minimized pane
	// after its emulator is resized. The main package uses this to resize
	// copy mode instances. May be nil.
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

// HandleLayout processes a layout snapshot from the server. Creates/removes
// emulators as panes appear/disappear, rebuilds the local layout tree, and
// resizes emulators to match their cells. Returns true if the layout structure
// changed (panes moved/resized/added/removed), false for metadata-only updates
// like focus changes.
func (r *Renderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	return withRendererActorValue(r, func(st *rendererActorState) bool {
		prev := st.snapshot
		oldFP := prev.layoutFingerprint()

		next := &rendererSnapshot{
			emulators:       make(map[uint32]mux.TerminalEmulator),
			paneInfo:        make(map[uint32]proto.PaneSnapshot),
			sessionName:     snap.SessionName,
			sessionNotice:   snap.Notice,
			capabilities:    prev.capabilities,
			activePaneID:    snap.ActivePaneID,
			zoomedPaneID:    snap.ZoomedPaneID,
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

		for _, ps := range allPanes {
			next.paneInfo[ps.ID] = ps
			emu := prev.emulators[ps.ID]
			if emu == nil {
				var w, h int
				if ps.Minimized && ps.EmuWidth > 0 && ps.EmuHeight > 0 {
					w, h = ps.EmuWidth, ps.EmuHeight
				} else {
					w, h = proto.FindPaneDimensions(snap, activeRoot, ps.ID, mux.PaneContentHeight)
				}
				emu = mux.NewVTEmulatorWithDrainAndScrollback(w, h, prev.scrollbackLines)
			}
			next.emulators[ps.ID] = emu
		}

		next.layout = mux.RebuildLayout(activeRoot)
		clientLayoutH := next.height - render.GlobalBarHeight
		if next.layout != nil && (snap.Width != next.width || snap.Height != clientLayoutH) {
			// Rescale the client-side layout before resizing emulators so wrap
			// and cursor metadata match this client's window, not the server's
			// max-size snapshot.
			next.layout.ResizeAll(next.width, clientLayoutH)
		}

		if next.layout != nil {
			next.layout.Walk(func(cell *mux.LayoutCell) {
				emu := next.emulators[cell.PaneID]
				if emu == nil {
					return
				}
				if info, ok := next.paneInfo[cell.PaneID]; ok && info.Minimized {
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
			if emu := next.emulators[next.zoomedPaneID]; emu != nil {
				layoutH := st.compositor.LayoutHeight()
				emu.Resize(next.width, mux.PaneContentHeight(layoutH))
			}
		}

		st.snapshot = next
		r.publishSnapshot(next)
		return next.layoutFingerprint() != oldFP
	})
}

// HandlePaneOutput feeds raw PTY data into a pane's local emulator.
func (r *Renderer) HandlePaneOutput(paneID uint32, data []byte) {
	r.withActor(func(st *rendererActorState) {
		if emu := st.snapshot.emulators[paneID]; emu != nil {
			emu.Write(data)
		}
	})
}

// Resize updates the client's terminal dimensions.
func (r *Renderer) Resize(width, height int) {
	r.withActor(func(st *rendererActorState) {
		next := *st.snapshot
		next.width = width
		next.height = height
		st.compositor.Resize(width, height)
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

// RenderFullWithOverlay produces ANSI output compositing all panes plus
// optional client-local overlays. The paneLookup function maps pane IDs to
// PaneData — the caller provides this so it can inject copy-mode overlays or
// other per-pane customization. Returns empty string if no layout is available.
//
// The lock is released before calling into the compositor so the paneLookup
// callback may safely call Emulator/PaneInfo without deadlocking. Callers must
// ensure render and layout mutation are not concurrent; in practice the
// interactive client renders from a single goroutine and the headless client is
// sequential.
func (r *Renderer) RenderFullWithOverlay(paneLookup func(uint32) render.PaneData, overlay render.OverlayState, clearScreen ...bool) string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		snap := st.snapshot
		if snap.layout == nil {
			return ""
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		overlay = r.mergeOverlay(snap, overlay)
		return st.compositor.RenderFullWithOverlay(root, activePaneID, paneLookup, overlay, clearScreen...)
	})
}

// RenderDiffWithOverlay produces minimal ANSI output by diffing against the
// previous frame, plus optional client-local overlays. Returns empty string if
// no layout is available.
func (r *Renderer) RenderDiffWithOverlay(paneLookup func(uint32) render.PaneData, overlay render.OverlayState) string {
	return withRendererActorValue(r, func(st *rendererActorState) string {
		snap := st.snapshot
		if snap.layout == nil {
			return ""
		}
		root, activePaneID := snap.captureRoot(st.compositor.LayoutHeight())
		overlay = r.mergeOverlay(snap, overlay)
		return st.compositor.RenderDiffWithOverlay(root, activePaneID, paneLookup, overlay)
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
			return r.paneLookupSnapshot(snap, paneID)
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
			return r.paneLookupSnapshot(snap, paneID)
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
	snap := r.loadSnapshot()
	if snap.layout == nil {
		return proto.CaptureJSON{}, false
	}

	root, _ := snap.captureRoot(snap.height - render.GlobalBarHeight)

	capture := proto.CaptureJSON{
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
		cp, ok := r.buildCapturePane(snap, paneID, agentStatus)
		if !ok {
			return
		}
		cp.Position = &proto.CapturePos{
			X: c.X, Y: c.Y, Width: c.W, Height: c.H,
		}
		capture.Panes = append(capture.Panes, cp)
	})

	return capture, true
}

// CaptureJSON renders a structured JSON capture from client-side emulators.
// Agent status (idle, current_command, child_pids) comes from the server.
func (r *Renderer) CaptureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	capture, ok := r.captureJSONValue(agentStatus)
	if !ok {
		return "{}"
	}
	return marshalIndented(capture)
}

// CapturePaneText returns a single pane's content from client-side emulators.
func (r *Renderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	snap := r.loadSnapshot()
	emu, ok := snap.emulators[paneID]
	if !ok {
		return ""
	}
	if includeANSI {
		return filterRenderedANSI(emu.Render(), snap.capabilities)
	}
	return strings.Join(mux.EmulatorContentLines(emu), "\n")
}

// capturePaneValue builds the structured JSON payload for a single pane.
// Returns false when the pane is not found.
func (r *Renderer) capturePaneValue(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) (proto.CapturePane, bool) {
	return r.buildCapturePane(r.loadSnapshot(), paneID, agentStatus)
}

// CapturePaneJSON returns a single pane's JSON from client-side emulators.
func (r *Renderer) CapturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	cp, ok := r.capturePaneValue(paneID, agentStatus)
	if !ok {
		return "{}"
	}
	return marshalIndented(cp)
}

// ResolvePaneID resolves a pane reference to an ID from client-side state.
func (r *Renderer) ResolvePaneID(ref string) uint32 {
	snap := r.loadSnapshot()

	// Try numeric ID
	if id, err := strconv.ParseUint(ref, 10, 32); err == nil {
		if _, ok := snap.paneInfo[uint32(id)]; ok {
			return uint32(id)
		}
	}
	// Try name or prefix match
	var prefixMatch uint32
	for _, info := range snap.paneInfo {
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
	return r.loadSnapshot().activePaneID
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

// Emulator returns the terminal emulator for the given pane. Thread-safe.
func (r *Renderer) Emulator(paneID uint32) (mux.TerminalEmulator, bool) {
	emu, ok := r.loadSnapshot().emulators[paneID]
	return emu, ok
}

// PaneInfo returns the pane snapshot for the given pane. Thread-safe.
func (r *Renderer) PaneInfo(paneID uint32) (proto.PaneSnapshot, bool) {
	info, ok := r.loadSnapshot().paneInfo[paneID]
	return info, ok
}

func (r *Renderer) buildCapturePane(snap *rendererSnapshot, paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) (proto.CapturePane, bool) {
	emu, ok := snap.emulators[paneID]
	if !ok {
		return proto.CapturePane{}, false
	}
	info, ok := snap.paneInfo[paneID]
	if !ok {
		return proto.CapturePane{}, false
	}
	col, row := emu.CursorPosition()
	cp := caputil.BuildPane(caputil.PaneInput{
		ID:         info.ID,
		Name:       info.Name,
		Active:     info.ID == snap.activePaneID,
		Minimized:  info.Minimized,
		Zoomed:     info.ID == snap.zoomedPaneID,
		Host:       info.Host,
		Task:       info.Task,
		Color:      info.Color,
		ConnStatus: info.ConnStatus,
		Cursor: proto.CaptureCursor{
			Col:    col,
			Row:    row,
			Hidden: emu.CursorHidden(),
		},
		Content: mux.EmulatorContentLines(emu),
	}, agentStatus)
	return cp, true
}

func (r *Renderer) paneLookupSnapshot(snap *rendererSnapshot, paneID uint32) render.PaneData {
	emu, ok := snap.emulators[paneID]
	if !ok {
		return nil
	}
	info, ok := snap.paneInfo[paneID]
	if !ok {
		return nil
	}
	return &PaneData{Emu: emu, Info: info, Capabilities: snap.capabilities}
}

func (r *Renderer) mergeOverlay(snap *rendererSnapshot, overlay render.OverlayState) render.OverlayState {
	if overlay.Message == "" {
		overlay.Message = snap.sessionNotice
	}
	return overlay
}

// HandleCaptureRequest processes capture args and returns a proto.Message
// with the rendered output. This is the shared implementation used by both
// the live client (main package) and the headless test client.
func (r *Renderer) HandleCaptureRequest(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *proto.Message {
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
		paneID := r.ResolvePaneID(req.PaneRef)
		if paneID == 0 {
			return &proto.Message{Type: proto.MsgTypeCaptureResponse,
				CmdErr: fmt.Sprintf("pane %q not found", req.PaneRef)}
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
		out = r.CaptureJSON(agentStatus) + "\n"
	} else if req.ColorMap {
		out = r.CaptureColorMap()
	} else {
		out = r.Capture(!req.IncludeANSI)
	}
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: out}
}
