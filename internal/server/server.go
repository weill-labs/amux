package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/coverage"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
	"github.com/weill-labs/amux/internal/render"
)

// Default terminal dimensions when the client doesn't report a size.
const (
	DefaultTermCols = 80
	DefaultTermRows = 24
)

// DefaultIdleTimeout is how long a pane must be quiet before firing on-idle.
const DefaultIdleTimeout = 2 * time.Second

// DefaultOutputLines is how many lines `amux output` shows by default.
const DefaultOutputLines = 50

// WindowNameFormat is the default name for auto-created windows.
const WindowNameFormat = "window-%d"

// Session holds the state for one amux session.
type Session struct {
	Name           string
	Windows        []*mux.Window // ordered list of windows
	ActiveWindowID uint32        // which window is displayed
	Panes          []*mux.Pane   // flat list of ALL panes across all windows
	clients        []*ClientConn
	counter        atomic.Uint32 // pane ID counter
	windowCounter  atomic.Uint32 // window ID counter
	mu             sync.Mutex
	shutdown       atomic.Bool

	// Layout generation counter — incremented on every broadcastLayout.
	// Used by wait-layout to block until a layout change occurs.
	generation     atomic.Uint64
	generationMu   sync.Mutex
	generationCond *sync.Cond

	// Per-pane output subscribers — used by wait-for to block until
	// a substring appears in a pane's screen content.
	paneOutputSubs map[uint32][]chan struct{}
	paneOutputMu   sync.Mutex

	// Clipboard generation counter — incremented on every OSC 52 clipboard
	// event. Used by wait-clipboard to block until a clipboard write occurs.
	clipboardGen     atomic.Uint64
	clipboardMu      sync.Mutex
	clipboardCond    *sync.Cond
	lastClipboardB64 string // last clipboard payload (base64), protected by clipboardMu

	// Hook system — session-level, not checkpointed.
	Hooks       *hooks.Registry
	idleTimers  map[uint32]*time.Timer // per-pane idle timers, protected by idleTimerMu
	idleState   map[uint32]bool        // true = idle, protected by idleTimerMu
	idleTimerMu sync.Mutex

	// Event stream subscribers — used by `amux events` for push-based notifications.
	eventSubs   []*eventSub
	eventSubsMu sync.Mutex

	// Remote pane management — manages SSH connections to remote hosts.
	// Nil when no config is loaded or no remote hosts are defined.
	RemoteManager *remote.Manager

	// SSH takeover tracking — pane IDs that have already been taken over.
	// Prevents duplicate takeover if the remote emits the sequence twice.
	// Protected by s.mu.
	takenOverPanes map[uint32]bool

	// Capture forwarding — routes capture requests through the attached
	// interactive client so the result reflects client-side emulator state.
	// captureMu serializes capture requests; captureResult is the one-shot
	// response channel for the in-flight request (protected by s.mu).
	captureMu     sync.Mutex
	captureResult chan *Message
}

// ActiveWindow returns the currently active window, or nil.
func (s *Session) ActiveWindow() *mux.Window {
	for _, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			return w
		}
	}
	if len(s.Windows) > 0 {
		return s.Windows[0]
	}
	return nil
}

// FindWindowByPaneID returns the window containing the given pane, or nil.
func (s *Session) FindWindowByPaneID(paneID uint32) *mux.Window {
	for _, w := range s.Windows {
		if w.Root.FindPane(paneID) != nil {
			return w
		}
	}
	return nil
}

// RemoveWindow removes a window from the list by ID.
func (s *Session) RemoveWindow(windowID uint32) {
	for i, w := range s.Windows {
		if w.ID == windowID {
			s.Windows = append(s.Windows[:i], s.Windows[i+1:]...)
			return
		}
	}
}

// NextWindow switches to the next window (wrapping).
func (s *Session) NextWindow() {
	if len(s.Windows) <= 1 {
		return
	}
	for i, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			s.ActiveWindowID = s.Windows[(i+1)%len(s.Windows)].ID
			return
		}
	}
}

// PrevWindow switches to the previous window (wrapping).
func (s *Session) PrevWindow() {
	if len(s.Windows) <= 1 {
		return
	}
	for i, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			prev := (i - 1 + len(s.Windows)) % len(s.Windows)
			s.ActiveWindowID = s.Windows[prev].ID
			return
		}
	}
}

// ResolveWindow finds a window by 1-based index, exact name, or name prefix.
// Caller must hold s.mu.
func (s *Session) ResolveWindow(ref string) *mux.Window {
	// Try as 1-based index
	if idx, err := strconv.Atoi(ref); err == nil {
		if idx >= 1 && idx <= len(s.Windows) {
			return s.Windows[idx-1]
		}
		return nil
	}
	// Try exact name match
	for _, w := range s.Windows {
		if w.Name == ref {
			return w
		}
	}
	// Try prefix match
	for _, w := range s.Windows {
		if len(ref) > 0 && strings.HasPrefix(w.Name, ref) {
			return w
		}
	}
	return nil
}

// closePaneInWindow removes a pane from its window's layout. If the pane
// is the last one in the window, the window itself is destroyed and focus
// moves to the first remaining window. Caller must hold s.mu.
func (s *Session) closePaneInWindow(paneID uint32) {
	w := s.FindWindowByPaneID(paneID)
	if w == nil {
		return
	}
	if w.PaneCount() <= 1 {
		wasActive := w.ID == s.ActiveWindowID
		s.RemoveWindow(w.ID)
		if wasActive && len(s.Windows) > 0 {
			s.ActiveWindowID = s.Windows[0].ID
		}
	} else {
		w.ClosePane(paneID)
	}
}

// broadcast sends a message to all connected clients.
func (s *Session) broadcast(msg *Message) {
	s.mu.Lock()
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	for _, c := range clients {
		c.Send(msg)
	}
}

// clipboardCallback returns the onClipboard callback for panes in this session.
// It forwards OSC 52 clipboard sequences to all connected clients and
// increments the clipboard generation counter for wait-clipboard.
func (s *Session) clipboardCallback() func(paneID uint32, data []byte) {
	return func(paneID uint32, data []byte) {
		if s.shutdown.Load() {
			return
		}
		s.broadcast(&Message{Type: MsgTypeClipboard, PaneID: paneID, PaneData: data})

		s.clipboardMu.Lock()
		s.lastClipboardB64 = string(data)
		s.clipboardGen.Add(1)
		s.clipboardCond.Broadcast()
		s.clipboardMu.Unlock()
	}
}

// forwardCapture sends a capture request to the first attached interactive
// client and waits for its response. The client renders from its own
// emulators — the rendering source of truth. For JSON captures, the server
// gathers agent status (one pgrep call per pane) and includes it in the
// request. Serialized via captureMu so concurrent callers don't clobber.
func (s *Session) forwardCapture(args []string) *Message {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()

	s.mu.Lock()
	if len(s.clients) == 0 {
		s.mu.Unlock()
		return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
	}
	// Use the first attached client. In practice there's one interactive
	// client at a time; if multiple attach, the first is authoritative.
	client := s.clients[0]

	ch := make(chan *Message, 1)
	s.captureResult = ch

	// For JSON captures, snapshot pane list while holding the lock.
	var statusPanes []*mux.Pane
	if slices.Contains(args, "json") {
		statusPanes = make([]*mux.Pane, len(s.Panes))
		copy(statusPanes, s.Panes)
	}
	s.mu.Unlock()

	// Gather agent status outside the lock (spawns pgrep subprocesses).
	// One call per pane — the result is included in the capture request
	// so the client doesn't need its own pgrep.
	var agentStatus map[uint32]proto.PaneAgentStatus
	if len(statusPanes) > 0 {
		agentStatus = make(map[uint32]proto.PaneAgentStatus, len(statusPanes))
		for _, p := range statusPanes {
			st := p.AgentStatus()
			agentStatus[p.ID] = proto.PaneAgentStatus{
				Idle:           st.Idle,
				IdleSince:      formatIdleSince(st.IdleSince),
				CurrentCommand: st.CurrentCommand,
				ChildPIDs:      nonNilPIDs(st.ChildPIDs),
			}
		}
	}

	client.Send(&Message{
		Type:        MsgTypeCaptureRequest,
		CmdArgs:     args,
		AgentStatus: agentStatus,
	})

	defer func() {
		s.mu.Lock()
		s.captureResult = nil
		s.mu.Unlock()
	}()

	select {
	case resp := <-ch:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: resp.CmdOutput, CmdErr: resp.CmdErr}
	case <-time.After(3 * time.Second):
		return &Message{Type: MsgTypeCmdResult, CmdErr: "capture timed out (client unresponsive)"}
	}
}

// routeCaptureResponse delivers a capture response from the interactive client
// to the waiting forwardCapture caller. Thread-safe.
func (s *Session) routeCaptureResponse(msg *Message) {
	s.mu.Lock()
	ch := s.captureResult
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// formatIdleSince returns an RFC3339 string for a non-zero time, or "".
func formatIdleSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// nonNilPIDs ensures a nil slice becomes an empty slice for JSON marshaling.
func nonNilPIDs(pids []int) []int {
	if pids == nil {
		return []int{}
	}
	return pids
}

// broadcastPaneOutput sends raw PTY output for one pane to all clients,
// notifies any wait-for subscribers, and tracks pane activity for hooks.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte) {
	s.broadcast(&Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data})
	s.notifyPaneOutputSubs(paneID)
	s.trackPaneActivity(paneID)

	// Emit output event for event stream subscribers.
	s.mu.Lock()
	var paneName, host string
	if p := s.findPaneLocked(paneID); p != nil {
		paneName = p.Meta.Name
		host = p.Meta.Host
	}
	s.mu.Unlock()
	s.emitEvent(Event{Type: EventOutput, PaneID: paneID, PaneName: paneName, Host: host})
}

// broadcastPaneOutputLocked sends raw PTY output to all clients.
// Caller must hold s.mu.
func (s *Session) broadcastPaneOutputLocked(paneID uint32, data []byte) {
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	msg := &Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data}
	for _, c := range clients {
		c.Send(msg)
	}
}

// broadcastLayout sends the current layout snapshot to all clients
// and increments the layout generation counter.
func (s *Session) broadcastLayout() {
	idleSnap := s.snapshotIdleState()
	s.mu.Lock()
	snap := s.snapshotLayoutLocked(idleSnap)
	if snap == nil {
		s.mu.Unlock()
		return
	}
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	// Increment generation and wake any wait-layout waiters.
	s.generationMu.Lock()
	gen := s.generation.Add(1)
	s.generationCond.Broadcast()
	s.generationMu.Unlock()

	msg := &Message{Type: MsgTypeLayout, Layout: snap}
	for _, c := range clients {
		c.Send(msg)
	}

	// Emit layout event for event stream subscribers.
	activePaneName := ""
	if snap.ActivePaneID != 0 {
		for _, p := range snap.Panes {
			if p.ID == snap.ActivePaneID {
				activePaneName = p.Name
				break
			}
		}
	}
	s.emitEvent(Event{Type: EventLayout, Generation: gen, ActivePane: activePaneName})
}

// snapshotLayoutLocked builds a LayoutSnapshot with multi-window data.
// Caller must hold s.mu.
// snapshotIdleState returns a copy of the session's idle state map.
// Must be called before acquiring s.mu to maintain lock ordering:
// trackPaneActivity holds idleTimerMu then acquires s.mu (via buildPaneEnv),
// so callers must acquire idleTimerMu before s.mu.
func (s *Session) snapshotIdleState() map[uint32]bool {
	s.idleTimerMu.Lock()
	defer s.idleTimerMu.Unlock()
	snap := make(map[uint32]bool, len(s.idleState))
	for id, idle := range s.idleState {
		snap[id] = idle
	}
	return snap
}

func (s *Session) snapshotLayoutLocked(idleSnap map[uint32]bool) *proto.LayoutSnapshot {
	w := s.ActiveWindow()
	if w == nil {
		return nil
	}

	// Build legacy single-window fields for the active window
	snap := w.SnapshotLayout(s.Name)
	snap.ActiveWindowID = s.ActiveWindowID

	// Build multi-window snapshots
	for i, win := range s.Windows {
		snap.Windows = append(snap.Windows, win.SnapshotWindow(i+1))
	}

	// Stamp idle state from the pre-acquired snapshot.
	for i := range snap.Panes {
		snap.Panes[i].Idle = idleSnap[snap.Panes[i].ID]
	}
	for wi := range snap.Windows {
		for pi := range snap.Windows[wi].Panes {
			snap.Windows[wi].Panes[pi].Idle = idleSnap[snap.Windows[wi].Panes[pi].ID]
		}
	}

	return snap
}

// removeClient removes a client from the session.
func (s *Session) removeClient(cc *ClientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.clients {
		if c == cc {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

// hasPane checks if a pane ID is still in the session's pane list.
func (s *Session) hasPane(id uint32) bool {
	for _, p := range s.Panes {
		if p.ID == id {
			return true
		}
	}
	return false
}

// removePane removes a pane from the flat list by ID and cleans up its idle timer.
func (s *Session) removePane(id uint32) {
	for i, p := range s.Panes {
		if p.ID == id {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			break
		}
	}
	// Async to avoid deadlock: removePane is called with s.mu held, and
	// stopPaneIdleTimer acquires idleTimerMu. The idle timer callback holds
	// idleTimerMu and acquires s.mu (via buildPaneEnv), so synchronous
	// acquisition here would invert the lock order.
	go s.stopPaneIdleTimer(id)
}

// paneOutputCallback returns the standard onOutput callback for panes.
func (s *Session) paneOutputCallback() func(uint32, []byte) {
	return func(paneID uint32, data []byte) {
		if s.shutdown.Load() {
			return
		}
		s.broadcastPaneOutput(paneID, data)
	}
}

// paneExitCallback returns the standard onExit callback for panes.
// When the last pane exits, the session sends MsgTypeExit and shuts down.
func (s *Session) paneExitCallback(srv *Server) func(uint32) {
	return func(paneID uint32) {
		if s.shutdown.Load() {
			return
		}
		s.mu.Lock()
		if !s.hasPane(paneID) {
			s.mu.Unlock()
			return
		}
		if len(s.Panes) <= 1 {
			s.mu.Unlock()
			s.broadcast(&Message{Type: MsgTypeExit})
			srv.Shutdown()
			return
		}
		s.removePane(paneID)
		s.closePaneInWindow(paneID)
		s.mu.Unlock()
		s.broadcastLayout()
	}
}

// createPane creates a new pane with auto-assigned metadata.
func (s *Session) createPane(srv *Server, cols, rows int) (*mux.Pane, error) {
	cnt := s.counter.Load()
	meta := mux.PaneMeta{
		Name:  fmt.Sprintf(mux.PaneNameFormat, cnt+1),
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[cnt%uint32(len(config.CatppuccinMocha))],
	}
	return s.createPaneWithMeta(srv, meta, cols, rows)
}

// createPaneWithMeta creates a new pane with explicit metadata (for spawn).
func (s *Session) createPaneWithMeta(srv *Server, meta mux.PaneMeta, cols, rows int) (*mux.Pane, error) {
	id := s.counter.Add(1)
	if meta.Color == "" {
		meta.Color = config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))]
	}

	pane, err := mux.NewPane(id, meta, cols, rows,
		s.paneOutputCallback(),
		s.paneExitCallback(srv),
	)
	if err != nil {
		return nil, err
	}

	pane.SetOnClipboard(s.clipboardCallback())
	pane.SetOnTakeover(s.takeoverCallback(srv))

	s.Panes = append(s.Panes, pane)
	return pane, nil
}

// createRemotePane creates a proxy pane that routes I/O to a remote host.
// Caller must NOT hold s.mu (the remote manager needs to make SSH calls).
func (s *Session) createRemotePane(srv *Server, hostName string, cols, rows int) (*mux.Pane, error) {
	if s.RemoteManager == nil {
		return nil, fmt.Errorf("no remote hosts configured")
	}

	id := s.counter.Add(1)
	meta := mux.PaneMeta{
		Name:   fmt.Sprintf(mux.PaneNameFormat, id),
		Host:   hostName,
		Color:  s.RemoteManager.Config().HostColor(hostName),
		Remote: string(remote.Connected), // initial state
	}

	// Create the proxy pane with a writeOverride that routes to the remote manager
	pane := mux.NewProxyPane(id, meta, cols, rows,
		s.paneOutputCallback(),
		s.paneExitCallback(srv),
		func(data []byte) (int, error) {
			return len(data), s.RemoteManager.SendInput(id, data)
		},
	)

	s.mu.Lock()
	s.Panes = append(s.Panes, pane)
	s.mu.Unlock()

	// Create the corresponding pane on the remote server
	_, err := s.RemoteManager.CreatePane(hostName, id, s.Name)
	if err != nil {
		// Roll back: remove the pane we just added
		s.mu.Lock()
		s.removePane(id)
		s.mu.Unlock()
		return nil, err
	}

	return pane, nil
}

// SetupRemoteManager initializes the remote manager with callbacks.
func (s *Session) SetupRemoteManager(cfg *config.Config) {
	mgr := remote.NewManager(cfg)
	mgr.SetCallbacks(
		// onPaneOutput: feed remote output into the proxy pane's emulator
		func(localPaneID uint32, data []byte) {
			s.mu.Lock()
			pane := s.findPaneLocked(localPaneID)
			s.mu.Unlock()
			if pane != nil {
				pane.FeedOutput(data)
			}
		},
		// onPaneExit: clean up when a remote pane exits
		func(localPaneID uint32) {
			if s.shutdown.Load() {
				return
			}
			s.mu.Lock()
			if !s.hasPane(localPaneID) {
				s.mu.Unlock()
				return
			}
			s.removePane(localPaneID)
			s.closePaneInWindow(localPaneID)
			s.mu.Unlock()
			s.broadcastLayout()
		},
		// onStateChange: update pane metadata when connection state changes
		func(hostName string, state remote.ConnState) {
			s.mu.Lock()
			for _, p := range s.Panes {
				if p.Meta.Host == hostName && p.IsProxy() {
					p.Meta.Remote = string(state)
				}
			}
			s.mu.Unlock()
			s.broadcastLayout()
		},
	)
	s.RemoteManager = mgr
}

// takeoverCallback returns the onTakeover callback for panes in this session.
// When a nested amux emits a takeover sequence through the PTY, this handler
// sends the ack, builds proxy panes that route I/O through the existing SSH
// PTY, and splices them into the layout tree — replacing the SSH pane.
func (s *Session) takeoverCallback(srv *Server) func(paneID uint32, req mux.TakeoverRequest) {
	return func(paneID uint32, req mux.TakeoverRequest) {
		go s.handleTakeover(srv, paneID, req)
	}
}

// handleTakeover processes a takeover request from a nested amux.
// It runs asynchronously (called via goroutine from the readLoop callback).
func (s *Session) handleTakeover(srv *Server, sshPaneID uint32, req mux.TakeoverRequest) {
	s.mu.Lock()

	// Guard against duplicate takeover for the same pane (e.g., the remote
	// emits the sequence twice during reconnect).
	if s.takenOverPanes[sshPaneID] {
		s.mu.Unlock()
		return
	}
	s.takenOverPanes[sshPaneID] = true

	sshPane := s.findPaneLocked(sshPaneID)
	if sshPane == nil {
		s.mu.Unlock()
		return
	}

	// Verify the SSH pane is still in a window's layout
	w := s.FindWindowByPaneID(sshPaneID)
	if w == nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Send ack through the SSH PTY's stdin — this tells the remote amux
	// to enter managed mode instead of launching its own TUI.
	sshPane.Write([]byte(mux.TakeoverAck))

	// Wait briefly for the remote server to start
	time.Sleep(500 * time.Millisecond)

	hostname := req.Host
	if hostname == "" {
		hostname = "remote"
	}

	// Re-acquire lock and read fresh cell dimensions (may have changed
	// during the unlocked period due to resize events).
	s.mu.Lock()
	w = s.FindWindowByPaneID(sshPaneID)
	if w == nil {
		s.mu.Unlock()
		return
	}
	cell := w.Root.FindPane(sshPaneID)
	if cell == nil {
		s.mu.Unlock()
		return
	}
	cols, cellH := cell.W, cell.H

	// Build proxy panes for the remote session. If the request has no
	// panes (remote just started), create one default pane.
	remotePanes := req.Panes
	if len(remotePanes) == 0 {
		remotePanes = []mux.TakeoverPane{
			{ID: 1, Name: "pane-1", Cols: cols, Rows: mux.PaneContentHeight(cellH)},
		}
	}

	var proxyPanes []*mux.Pane
	for _, rp := range remotePanes {
		id := s.counter.Add(1)
		meta := mux.PaneMeta{
			Name:   fmt.Sprintf("%s@%s", rp.Name, hostname),
			Host:   hostname,
			Color:  config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
			Remote: string(remote.Connected),
		}

		// The writeOverride routes input through the original SSH PTY.
		// All proxy panes share the same SSH connection (the original PTY),
		// but the remote amux server demuxes by pane ID.
		proxyPane := mux.NewProxyPane(id, meta, cols, mux.PaneContentHeight(cellH),
			s.paneOutputCallback(),
			s.paneExitCallback(srv),
			func(data []byte) (int, error) {
				return sshPane.Write(data)
			},
		)
		proxyPanes = append(proxyPanes, proxyPane)
	}

	// Splice the proxy panes into the layout, replacing the SSH pane
	for _, pp := range proxyPanes {
		s.Panes = append(s.Panes, pp)
	}
	_, spliceErr := w.SplicePane(sshPaneID, proxyPanes)
	if spliceErr != nil {
		for _, pp := range proxyPanes {
			s.removePane(pp.ID)
		}
		s.mu.Unlock()
		return
	}

	// The SSH pane stays in the panes list (dormant) — its PTY maintains
	// the SSH connection for unsplice fallback.
	s.mu.Unlock()

	s.broadcastLayout()
}

// findPaneLocked finds a pane by ID. Caller must hold s.mu.
func (s *Session) findPaneLocked(id uint32) *mux.Pane {
	for _, p := range s.Panes {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// subscribePaneOutput registers a channel to receive notifications when
// PTY output arrives for the given pane. Returns the channel.
func (s *Session) subscribePaneOutput(paneID uint32) chan struct{} {
	ch := make(chan struct{}, 1)
	s.paneOutputMu.Lock()
	if s.paneOutputSubs == nil {
		s.paneOutputSubs = make(map[uint32][]chan struct{})
	}
	s.paneOutputSubs[paneID] = append(s.paneOutputSubs[paneID], ch)
	s.paneOutputMu.Unlock()
	return ch
}

// unsubscribePaneOutput removes a previously registered subscriber channel.
func (s *Session) unsubscribePaneOutput(paneID uint32, ch chan struct{}) {
	s.paneOutputMu.Lock()
	subs := s.paneOutputSubs[paneID]
	for i, sub := range subs {
		if sub == ch {
			s.paneOutputSubs[paneID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	s.paneOutputMu.Unlock()
}

// notifyPaneOutputSubs wakes all wait-for subscribers for the given pane.
func (s *Session) notifyPaneOutputSubs(paneID uint32) {
	s.paneOutputMu.Lock()
	subs := make([]chan struct{}, len(s.paneOutputSubs[paneID]))
	copy(subs, s.paneOutputSubs[paneID])
	s.paneOutputMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// paneIsBusy checks whether the given pane has child processes (i.e., a
// command is running). Thread-safe: looks up the pane under s.mu, then
// inspects the process tree outside the lock.
func (s *Session) paneIsBusy(paneID uint32) bool {
	s.mu.Lock()
	pane := s.findPaneLocked(paneID)
	s.mu.Unlock()
	if pane == nil {
		return false
	}
	return !pane.AgentStatus().Idle
}

// paneIsIdle checks whether the given pane has no child processes (shell is
// at prompt). Thread-safe: looks up the pane under s.mu, then inspects the
// process tree outside the lock.
func (s *Session) paneIsIdle(paneID uint32) bool {
	s.mu.Lock()
	pane := s.findPaneLocked(paneID)
	s.mu.Unlock()
	if pane == nil {
		return true
	}
	return pane.AgentStatus().Idle
}

// trackPaneActivity is called on every PTY output. It resets the idle timer
// and fires on-activity if the pane was previously idle. When the idle state
// transitions (idle↔busy), a layout broadcast is sent so clients see the
// updated PaneSnapshot.Idle (used for idle indicators in the status bar).
func (s *Session) trackPaneActivity(paneID uint32) {
	s.idleTimerMu.Lock()

	// If pane was idle, fire on-activity and emit busy event
	wasIdle := s.idleState[paneID]
	if wasIdle {
		s.idleState[paneID] = false
		env := s.buildPaneEnv(paneID, hooks.OnActivity)
		s.Hooks.Fire(hooks.OnActivity, env)
		s.emitEvent(Event{
			Type:     EventBusy,
			PaneID:   paneID,
			PaneName: env["AMUX_PANE_NAME"],
			Host:     env["AMUX_HOST"],
		})
	}

	// Reset or create idle timer
	if t, ok := s.idleTimers[paneID]; ok {
		t.Reset(DefaultIdleTimeout)
	} else {
		s.idleTimers[paneID] = time.AfterFunc(DefaultIdleTimeout, func() {
			s.idleTimerMu.Lock()
			s.idleState[paneID] = true
			env := s.buildPaneEnv(paneID, hooks.OnIdle)
			s.idleTimerMu.Unlock()
			s.Hooks.Fire(hooks.OnIdle, env)
			s.emitEvent(Event{
				Type:     EventIdle,
				PaneID:   paneID,
				PaneName: env["AMUX_PANE_NAME"],
				Host:     env["AMUX_HOST"],
			})
			s.broadcastLayout()
		})
	}
	s.idleTimerMu.Unlock()

	if wasIdle {
		s.broadcastLayout()
	}
}

// stopPaneIdleTimer cleans up the idle timer for a closed pane.
func (s *Session) stopPaneIdleTimer(paneID uint32) {
	s.idleTimerMu.Lock()
	defer s.idleTimerMu.Unlock()
	if t, ok := s.idleTimers[paneID]; ok {
		t.Stop()
		delete(s.idleTimers, paneID)
		delete(s.idleState, paneID)
	}
}

// buildPaneEnv builds the environment variable map for a hook invocation.
// Acquires s.mu internally to look up pane metadata.
func (s *Session) buildPaneEnv(paneID uint32, event hooks.Event) map[string]string {
	env := map[string]string{
		"AMUX_PANE_ID": fmt.Sprintf("%d", paneID),
		"AMUX_EVENT":   string(event),
	}

	// Look up pane metadata under session lock
	s.mu.Lock()
	for _, p := range s.Panes {
		if p.ID == paneID {
			env["AMUX_PANE_NAME"] = p.Meta.Name
			if p.Meta.Task != "" {
				env["AMUX_TASK"] = p.Meta.Task
			}
			if p.Meta.Host != "" {
				env["AMUX_HOST"] = p.Meta.Host
			}
			break
		}
	}
	s.mu.Unlock()

	return env
}

// paneScreenContains checks whether the rendered screen of the given pane
// contains the substring. Thread-safe: looks up the pane under s.mu, then
// calls Render() (thread-safe on the emulator) outside the lock.
func (s *Session) paneScreenContains(paneID uint32, substr string) bool {
	s.mu.Lock()
	pane := s.findPaneLocked(paneID)
	s.mu.Unlock()
	if pane == nil {
		return false
	}
	return strings.Contains(mux.StripANSI(pane.Render()), substr)
}

// waitGeneration blocks until the layout generation exceeds afterGen or
// timeout expires. Returns the current generation and whether it matched.
// All checks happen under generationMu to avoid TOCTOU races with Broadcast.
func (s *Session) waitGeneration(afterGen uint64, timeout time.Duration) (uint64, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		s.generationMu.Lock()
		s.generationCond.Broadcast()
		s.generationMu.Unlock()
	})
	defer timer.Stop()

	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	for {
		gen := s.generation.Load()
		if gen > afterGen {
			return gen, true
		}
		if time.Now().After(deadline) {
			return gen, false
		}
		s.generationCond.Wait()
	}
}

// waitClipboard blocks until the clipboard generation exceeds afterGen or
// timeout expires. Returns the last clipboard payload and whether it matched.
func (s *Session) waitClipboard(afterGen uint64, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		s.clipboardMu.Lock()
		s.clipboardCond.Broadcast()
		s.clipboardMu.Unlock()
	})
	defer timer.Stop()

	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	for {
		gen := s.clipboardGen.Load()
		if gen > afterGen {
			return s.lastClipboardB64, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		s.clipboardCond.Wait()
	}
}

// BuildVersion is set by main at startup for version reporting in status.
var BuildVersion string

// Server listens on a Unix socket and manages sessions.
type Server struct {
	listener net.Listener
	sessions map[string]*Session
	sockPath string
	mu       sync.Mutex
}

// SocketDir returns the directory for amux Unix sockets.
func SocketDir() string {
	return fmt.Sprintf("/tmp/amux-%d", os.Getuid())
}

// SocketPath returns the socket path for a session.
func SocketPath(session string) string {
	return filepath.Join(SocketDir(), session)
}

// newSession creates a Session with all fields initialized.
func newSession(name string) *Session {
	sess := &Session{Name: name}
	sess.generationCond = sync.NewCond(&sess.generationMu)
	sess.clipboardCond = sync.NewCond(&sess.clipboardMu)
	sess.Hooks = hooks.NewRegistry()
	sess.idleTimers = make(map[uint32]*time.Timer)
	sess.idleState = make(map[uint32]bool)
	sess.takenOverPanes = make(map[uint32]bool)
	return sess
}

// NewServer creates a new server listening on a Unix socket for the given session.
func NewServer(sessionName string) (*Server, error) {
	sockDir := SocketDir()
	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}

	sockPath := SocketPath(sessionName)

	if _, err := os.Stat(sockPath); err == nil {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			os.Remove(sockPath)
		} else {
			conn.Close()
			return nil, fmt.Errorf("server already running for session %q", sessionName)
		}
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}
	os.Chmod(sockPath, 0700)

	sess := newSession(sessionName)

	s := &Server{
		listener: listener,
		sessions: map[string]*Session{sessionName: sess},
		sockPath: sockPath,
	}

	return s, nil
}

// SetupRemoteManager initializes the remote manager for all sessions.
func (s *Server) SetupRemoteManager(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.SetupRemoteManager(cfg)
	}
}

// Run accepts client connections in a loop.
func (s *Server) Run() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

// Shutdown cleans up the server socket, remote connections, and panes.
func (s *Server) Shutdown() {
	s.listener.Close()
	os.Remove(s.sockPath)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.shutdown.Store(true)
		if sess.RemoteManager != nil {
			sess.RemoteManager.Shutdown()
		}
		sess.mu.Lock()
		panes := make([]*mux.Pane, len(sess.Panes))
		copy(panes, sess.Panes)
		sess.mu.Unlock()
		for _, p := range panes {
			p.Close()
		}
	}
}

// Reload checkpoints the server state and exec's the new binary.
// On success, this function never returns (the process image is replaced).
// On failure, the old server continues running.
func (s *Server) Reload(execPath string) error {
	s.mu.Lock()
	var sess *Session
	for _, sess = range s.sessions {
		break
	}
	s.mu.Unlock()

	if sess == nil {
		return fmt.Errorf("no session to reload")
	}

	// Broadcast reload notice to clients
	sess.broadcast(&Message{Type: MsgTypeServerReload})

	// Stop PTY read broadcasts
	sess.shutdown.Store(true)

	// Build checkpoint
	idleSnap := sess.snapshotIdleState()
	sess.mu.Lock()
	if len(sess.Windows) == 0 {
		sess.mu.Unlock()
		sess.shutdown.Store(false)
		return fmt.Errorf("no window to checkpoint")
	}

	snap := sess.snapshotLayoutLocked(idleSnap)
	cp := &checkpoint.ServerCheckpoint{
		SessionName:   sess.Name,
		Counter:       sess.counter.Load(),
		WindowCounter: sess.windowCounter.Load(),
		Layout:        *snap,
	}

	for _, p := range sess.Panes {
		pc := checkpoint.PaneCheckpoint{
			ID:        p.ID,
			Meta:      p.Meta,
			Screen:    p.RenderScreen(),
			CreatedAt: p.CreatedAt(),
			IsProxy:   p.IsProxy(),
		}
		if p.IsProxy() {
			// Proxy panes have no PTY or process to inherit
			pc.PtmxFd = -1
			pc.PID = 0
		} else {
			pc.PtmxFd = p.PtmxFd()
			pc.PID = p.ProcessPid()
		}
		// For minimized panes, save the emulator's actual dimensions
		// (pre-minimize size) so the emulator is restored at the correct
		// size after hot-reload. The cell dimensions are shrunk to just
		// the status line, which would garble output if used.
		if p.Meta.Minimized {
			pc.Cols, pc.Rows = p.EmulatorSize()
		} else {
			for _, w := range sess.Windows {
				if cell := w.Root.FindPane(p.ID); cell != nil {
					pc.Cols = cell.W
					pc.Rows = mux.PaneContentHeight(cell.H)
					break
				}
			}
		}
		cp.Panes = append(cp.Panes, pc)
	}
	sess.mu.Unlock()

	// Get listener FD
	lnFd, err := listenerFd(s.listener)
	if err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("getting listener FD: %w", err)
	}
	cp.ListenerFd = lnFd

	// Write checkpoint to temp file
	cpPath, err := checkpoint.Write(cp)
	if err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("writing checkpoint: %w", err)
	}

	// Clear FD_CLOEXEC on inherited FDs (skip proxy panes — they have no PTY)
	clearCloexec(uintptr(cp.ListenerFd))
	for _, pc := range cp.Panes {
		if !pc.IsProxy && pc.PtmxFd >= 0 {
			clearCloexec(uintptr(pc.PtmxFd))
		}
	}

	// Flush coverage data before exec (which replaces the process image
	// without running atexit handlers). No-op if not built with -cover.
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		_ = coverage.WriteCountersDir(dir)
	}

	// Replace process image with new binary
	env := append(os.Environ(), "AMUX_CHECKPOINT="+cpPath)
	execErr := syscall.Exec(execPath, os.Args, env)

	// If we get here, the exec call failed — undo changes
	sess.shutdown.Store(false)
	os.Remove(cpPath)
	return fmt.Errorf("server exec: %w", execErr)
}

// NewServerFromCheckpoint restores a server from a checkpoint after exec.
func NewServerFromCheckpoint(cp *checkpoint.ServerCheckpoint) (*Server, error) {
	// Reconstruct listener from inherited FD
	listenerFile := os.NewFile(uintptr(cp.ListenerFd), "listener")
	if listenerFile == nil {
		return nil, fmt.Errorf("invalid listener FD %d", cp.ListenerFd)
	}
	listener, err := net.FileListener(listenerFile)
	if err != nil {
		return nil, fmt.Errorf("restoring listener: %w", err)
	}
	listenerFile.Close() // FileListener dups the FD

	sess := newSession(cp.SessionName)
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)

	s := &Server{
		listener: listener,
		sessions: map[string]*Session{cp.SessionName: sess},
		sockPath: SocketPath(cp.SessionName),
	}

	// Restore panes
	paneMap := make(map[uint32]*mux.Pane, len(cp.Panes))
	for _, pc := range cp.Panes {
		var pane *mux.Pane

		onOutput := sess.paneOutputCallback()
		onExit := sess.paneExitCallback(s)

		if pc.IsProxy {
			// Restore proxy pane with frozen content, mark as reconnecting.
			// The remote manager will re-establish the SSH connection.
			meta := pc.Meta
			meta.Remote = string(remote.Reconnecting)
			pane = mux.NewProxyPane(pc.ID, meta, pc.Cols, pc.Rows,
				onOutput, onExit,
				func(data []byte) (int, error) {
					// writeOverride will be reconnected by the remote manager
					if sess.RemoteManager != nil {
						return len(data), sess.RemoteManager.SendInput(pc.ID, data)
					}
					return len(data), nil // drop input until reconnected
				},
			)
		} else {
			var restoreErr error
			pane, restoreErr = mux.RestorePane(pc.ID, pc.Meta, pc.PtmxFd, pc.PID, pc.Cols, pc.Rows,
				onOutput, onExit,
			)
			if restoreErr != nil {
				continue // Skip pane on restore failure
			}
		}

		pane.SetOnClipboard(sess.clipboardCallback())

		if !pc.CreatedAt.IsZero() {
			pane.SetCreatedAt(pc.CreatedAt)
		}
		pane.ReplayScreen(pc.Screen)
		paneMap[pc.ID] = pane
		sess.Panes = append(sess.Panes, pane)
	}

	if len(sess.Panes) == 0 {
		listener.Close()
		return nil, fmt.Errorf("no panes restored from checkpoint")
	}

	// Rebuild windows from multi-window snapshot or legacy single-window
	if len(cp.Layout.Windows) > 0 {
		for _, ws := range cp.Layout.Windows {
			w := mux.RebuildWindowFromSnapshot(ws, cp.Layout.Width, cp.Layout.Height, paneMap)
			sess.Windows = append(sess.Windows, w)
		}
		sess.ActiveWindowID = cp.Layout.ActiveWindowID
	} else {
		// Legacy single-window checkpoint
		w := mux.RebuildFromSnapshot(cp.Layout, paneMap)
		winID := sess.windowCounter.Add(1)
		w.ID = winID
		w.Name = fmt.Sprintf(WindowNameFormat, winID)
		sess.Windows = append(sess.Windows, w)
		sess.ActiveWindowID = winID
	}

	// Start PTY read loops for all restored panes (skip proxy panes)
	for _, p := range sess.Panes {
		if !p.IsProxy() {
			p.Start()
		}
	}

	// Save screen data for minimized panes so we can re-replay after the
	// SIGWINCH loop. Between Start() and the SIGWINCH delay, the readLoop
	// may consume buffered PTY output (e.g. a shell prompt produced during
	// the exec gap) that overwrites the replayed emulator content. Visible
	// panes recover via the SIGWINCH-triggered redraw; minimized panes need
	// an explicit re-replay.
	minimizedScreens := make(map[uint32]string)
	for _, pc := range cp.Panes {
		if pc.Meta.Minimized {
			minimizedScreens[pc.ID] = pc.Screen
		}
	}

	// Force TUI apps to do a full screen redraw via SIGWINCH.
	// Skip minimized panes and proxy panes (no PTY to SIGWINCH).
	go func() {
		resizeVisible := func(heightAdj int) {
			for _, w := range sess.Windows {
				for _, p := range sess.Panes {
					if p.Meta.Minimized || p.IsProxy() {
						continue
					}
					if cell := w.Root.FindPane(p.ID); cell != nil {
						p.Resize(cell.W, mux.PaneContentHeight(cell.H)+heightAdj)
					}
				}
			}
		}

		time.Sleep(500 * time.Millisecond)
		sess.mu.Lock()
		defer sess.mu.Unlock()

		resizeVisible(-1) // Shrink by 1 row to trigger SIGWINCH

		sess.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		sess.mu.Lock()

		resizeVisible(0) // Restore original size

		// Re-replay saved screen data for minimized panes. The readLoop
		// may have fed buffered PTY output into their emulators, garbling
		// the content that was replayed during restore. Clear the screen
		// first so the replay starts from a known state.
		// Also broadcast the replay to clients so their emulators stay
		// in sync with the server.
		for _, p := range sess.Panes {
			if screen, ok := minimizedScreens[p.ID]; ok {
				replayData := "\033[H\033[2J" + screen
				p.ReplayScreen(replayData)
				sess.broadcastPaneOutputLocked(p.ID, []byte(replayData))
			}
		}
	}()

	return s, nil
}

// listenerFd extracts the raw file descriptor from a net.Listener.
func listenerFd(ln net.Listener) (int, error) {
	type syscallConner interface {
		SyscallConn() (syscall.RawConn, error)
	}
	sc, ok := ln.(syscallConner)
	if !ok {
		return -1, fmt.Errorf("listener does not support SyscallConn")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var fd int
	if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
		return -1, err
	}
	return fd, nil
}

// clearCloexec clears the FD_CLOEXEC flag so the FD survives exec.
func clearCloexec(fd uintptr) {
	syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0)
}

func (s *Server) handleConn(conn net.Conn) {
	msg, err := ReadMsg(conn)
	if err != nil {
		conn.Close()
		return
	}

	switch msg.Type {
	case MsgTypeAttach:
		s.handleAttach(conn, msg)
	case MsgTypeCommand:
		s.handleOneShot(conn, msg)
	default:
		conn.Close()
	}
}

// handleAttach registers an interactive client and starts its read loop.
func (s *Server) handleAttach(conn net.Conn, msg *Message) {
	sessionName := msg.Session
	if sessionName == "" {
		sessionName = "default"
	}

	s.mu.Lock()
	sess, ok := s.sessions[sessionName]
	s.mu.Unlock()

	if !ok {
		conn.Close()
		return
	}

	cc := NewClientConn(conn)

	cols, rows := msg.Cols, msg.Rows
	if cols <= 0 {
		cols = DefaultTermCols
	}
	if rows <= 0 {
		rows = DefaultTermRows
	}

	idleSnap := sess.snapshotIdleState()
	sess.mu.Lock()

	// Reserve rows for the global status bar.
	layoutH := rows - render.GlobalBarHeight

	// Create the first pane and window if none exist.
	var newPane *mux.Pane
	var resized bool
	if len(sess.Windows) == 0 {
		paneH := mux.PaneContentHeight(layoutH)
		pane, err := sess.createPane(s, cols, paneH)
		if err != nil {
			sess.mu.Unlock()
			conn.Close()
			return
		}
		winID := sess.windowCounter.Add(1)
		w := mux.NewWindow(pane, cols, layoutH)
		w.ID = winID
		w.Name = fmt.Sprintf(WindowNameFormat, winID)
		sess.Windows = append(sess.Windows, w)
		sess.ActiveWindowID = winID
		newPane = pane
	} else {
		// Reattach: resize existing windows to match the new client's terminal.
		for _, w := range sess.Windows {
			w.Resize(cols, layoutH)
		}
		resized = true
	}

	// Send layout snapshot so client can build its rendering state
	snap := sess.snapshotLayoutLocked(idleSnap)
	cc.Send(&Message{Type: MsgTypeLayout, Layout: snap})

	// Send current screen state for each pane (enables reattach)
	for _, p := range sess.Panes {
		rendered := p.RenderScreen()
		cc.Send(&Message{Type: MsgTypePaneOutput, PaneID: p.ID, PaneData: []byte(rendered)})
	}

	sess.clients = append(sess.clients, cc)
	sess.mu.Unlock()

	if resized {
		sess.broadcastLayout()
	}

	if newPane != nil {
		newPane.Start()
	}

	cc.readLoop(s, sess)
}

func (s *Server) handleOneShot(conn net.Conn, msg *Message) {
	cc := NewClientConn(conn)
	defer cc.Close()

	s.mu.Lock()
	var sess *Session
	for _, sess = range s.sessions {
		break
	}
	s.mu.Unlock()

	if sess == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return
	}

	cc.handleCommand(s, sess, msg)
}
