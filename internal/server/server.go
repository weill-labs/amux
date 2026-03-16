package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/coverage"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// Default terminal dimensions when the client doesn't report a size.
const (
	DefaultTermCols = 80
	DefaultTermRows = 24
)

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

// broadcastPaneOutput sends raw PTY output for one pane to all clients,
// and notifies any wait-for subscribers for that pane.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte) {
	s.broadcast(&Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data})
	s.notifyPaneOutputSubs(paneID)
}

// broadcastLayout sends the current layout snapshot to all clients
// and increments the layout generation counter.
func (s *Session) broadcastLayout() {
	s.mu.Lock()
	snap := s.snapshotLayoutLocked()
	if snap == nil {
		s.mu.Unlock()
		return
	}
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	// Increment generation and wake any wait-layout waiters.
	s.generationMu.Lock()
	s.generation.Add(1)
	s.generationCond.Broadcast()
	s.generationMu.Unlock()

	msg := &Message{Type: MsgTypeLayout, Layout: snap}
	for _, c := range clients {
		c.Send(msg)
	}
}

// snapshotLayoutLocked builds a LayoutSnapshot with multi-window data.
// Caller must hold s.mu.
func (s *Session) snapshotLayoutLocked() *proto.LayoutSnapshot {
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

// removePane removes a pane from the flat list by ID.
func (s *Session) removePane(id uint32) {
	for i, p := range s.Panes {
		if p.ID == id {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			return
		}
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
		func(paneID uint32, data []byte) {
			if s.shutdown.Load() {
				return
			}
			// Send raw PTY output to all clients (client does rendering)
			s.broadcastPaneOutput(paneID, data)
		},
		func(paneID uint32) {
			if s.shutdown.Load() {
				return
			}

			s.mu.Lock()
			if !s.hasPane(paneID) {
				s.mu.Unlock()
				return
			}

			remaining := len(s.Panes)
			if remaining <= 1 {
				s.mu.Unlock()
				s.broadcast(&Message{Type: MsgTypeExit})
				srv.Shutdown()
				return
			}

			s.removePane(paneID)
			s.closePaneInWindow(paneID)
			s.mu.Unlock()

			s.broadcastLayout()
		},
	)
	if err != nil {
		return nil, err
	}

	pane.SetOnClipboard(s.clipboardCallback())

	s.Panes = append(s.Panes, pane)
	return pane, nil
}

// serverPaneData adapts *mux.Pane to the render.PaneData interface.
type serverPaneData struct{ p *mux.Pane }

func (s *serverPaneData) RenderScreen(active bool) string {
	if !active {
		return s.p.RenderWithoutCursorBlock()
	}
	return s.p.Render()
}
func (s *serverPaneData) CursorPos() (int, int) { return s.p.CursorPos() }
func (s *serverPaneData) CursorHidden() bool    { return s.p.CursorHidden() }
func (s *serverPaneData) HasCursorBlock() bool  { return s.p.HasCursorBlock() }
func (s *serverPaneData) ID() uint32            { return s.p.ID }
func (s *serverPaneData) Name() string          { return s.p.Meta.Name }
func (s *serverPaneData) Host() string          { return s.p.Meta.Host }
func (s *serverPaneData) Task() string          { return s.p.Meta.Task }
func (s *serverPaneData) Color() string         { return s.p.Meta.Color }
func (s *serverPaneData) Minimized() bool       { return s.p.Meta.Minimized }
func (s *serverPaneData) InCopyMode() bool      { return false }

// renderCapture renders the full composited screen server-side.
// If stripANSI is true, the ANSI stream is materialized into a plain-text
// 2D grid that preserves the visual layout.
//
// Note: pane emulator reads here race with concurrent PTY writes. This is
// the same best-effort pattern used by handleAttach's reattach snapshot.
func (s *Session) renderCapture(stripANSI bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	w := s.ActiveWindow()
	if w == nil {
		return ""
	}

	paneMap := make(map[uint32]render.PaneData, len(s.Panes))
	for _, p := range s.Panes {
		paneMap[p.ID] = &serverPaneData{p: p}
	}

	totalH := w.Height + render.GlobalBarHeight
	comp := render.NewCompositor(w.Width, totalH, s.Name)
	comp.SetWindows(s.windowInfoLocked())

	var activePaneID uint32
	if w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}

	root := w.Root
	if w.ZoomedPaneID != 0 {
		root = mux.NewLeafByID(w.ZoomedPaneID, 0, 0, w.Width, w.Height)
	}

	raw := string(comp.RenderFull(root, activePaneID, func(id uint32) render.PaneData {
		return paneMap[id]
	}))

	if stripANSI {
		return render.MaterializeGrid(raw, w.Width, totalH)
	}

	return raw
}

// windowInfoLocked returns window info for rendering. Caller must hold s.mu.
func (s *Session) windowInfoLocked() []render.WindowInfo {
	infos := make([]render.WindowInfo, len(s.Windows))
	for i, w := range s.Windows {
		infos[i] = render.WindowInfo{
			Index:    i + 1,
			Name:     w.Name,
			IsActive: w.ID == s.ActiveWindowID,
			Panes:    w.PaneCount(),
		}
	}
	return infos
}

// renderColorMap renders the ANSI capture and extracts a color map showing
// border colors as single-letter Catppuccin initials.
func (s *Session) renderColorMap() string {
	s.mu.Lock()
	w := s.ActiveWindow()
	if w == nil {
		s.mu.Unlock()
		return ""
	}
	width := w.Width
	h := w.Height + render.GlobalBarHeight
	s.mu.Unlock()
	ansi := s.renderCapture(false)
	return render.ExtractColorMap(ansi, width, h) + "\n"
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

// paneScreenContains checks whether the rendered screen of the given pane
// contains the substring. Thread-safe: looks up the pane under s.mu, then
// calls Render() (thread-safe on the emulator) outside the lock.
func (s *Session) paneScreenContains(paneID uint32, substr string) bool {
	s.mu.Lock()
	var pane *mux.Pane
	for _, p := range s.Panes {
		if p.ID == paneID {
			pane = p
			break
		}
	}
	s.mu.Unlock()
	if pane == nil {
		return false
	}
	plain := mux.StripANSI(pane.Render())
	return strings.Contains(plain, substr)
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

	sess := &Session{Name: sessionName}
	sess.generationCond = sync.NewCond(&sess.generationMu)
	sess.clipboardCond = sync.NewCond(&sess.clipboardMu)

	s := &Server{
		listener: listener,
		sessions: map[string]*Session{sessionName: sess},
		sockPath: sockPath,
	}

	return s, nil
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

// Shutdown cleans up the server socket and panes.
func (s *Server) Shutdown() {
	s.listener.Close()
	os.Remove(s.sockPath)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.shutdown.Store(true)
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
	sess.mu.Lock()
	if len(sess.Windows) == 0 {
		sess.mu.Unlock()
		sess.shutdown.Store(false)
		return fmt.Errorf("no window to checkpoint")
	}

	snap := sess.snapshotLayoutLocked()
	cp := &checkpoint.ServerCheckpoint{
		SessionName:   sess.Name,
		Counter:       sess.counter.Load(),
		WindowCounter: sess.windowCounter.Load(),
		Layout:        *snap,
	}

	for _, p := range sess.Panes {
		pc := checkpoint.PaneCheckpoint{
			ID:     p.ID,
			Meta:   p.Meta,
			PtmxFd: p.PtmxFd(),
			PID:    p.ProcessPid(),
			Screen: p.RenderScreen(),
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

	// Clear FD_CLOEXEC on inherited FDs
	clearCloexec(uintptr(cp.ListenerFd))
	for _, pc := range cp.Panes {
		clearCloexec(uintptr(pc.PtmxFd))
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

	sess := &Session{Name: cp.SessionName}
	sess.generationCond = sync.NewCond(&sess.generationMu)
	sess.clipboardCond = sync.NewCond(&sess.clipboardMu)
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
		pane, restoreErr := mux.RestorePane(pc.ID, pc.Meta, pc.PtmxFd, pc.PID, pc.Cols, pc.Rows,
			func(paneID uint32, data []byte) {
				if sess.shutdown.Load() {
					return
				}
				sess.broadcastPaneOutput(paneID, data)
			},
			func(paneID uint32) {
				if sess.shutdown.Load() {
					return
				}
				sess.mu.Lock()
				if !sess.hasPane(paneID) {
					sess.mu.Unlock()
					return
				}
				remaining := len(sess.Panes)
				if remaining <= 1 {
					sess.mu.Unlock()
					sess.broadcast(&Message{Type: MsgTypeExit})
					s.Shutdown()
					return
				}
				sess.removePane(paneID)
				sess.closePaneInWindow(paneID)
				sess.mu.Unlock()
				sess.broadcastLayout()
			},
		)
		if restoreErr != nil {
			continue // Skip pane on restore failure
		}

		pane.SetOnClipboard(sess.clipboardCallback())

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

	// Start PTY read loops for all restored panes
	for _, p := range sess.Panes {
		p.Start()
	}

	// Force TUI apps to do a full screen redraw via SIGWINCH.
	// Skip minimized panes — their PTYs stay at pre-minimize dimensions.
	go func() {
		resizeVisible := func(heightAdj int) {
			for _, w := range sess.Windows {
				for _, p := range sess.Panes {
					if p.Meta.Minimized {
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

	sess.mu.Lock()

	// Create the first pane and window if none exist
	var newPane *mux.Pane
	if len(sess.Windows) == 0 {
		// Reserve 1 row for global status bar, 1 for per-pane status
		layoutH := rows - 1 // global bar
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
	}

	// Send layout snapshot so client can build its rendering state
	snap := sess.snapshotLayoutLocked()
	cc.Send(&Message{Type: MsgTypeLayout, Layout: snap})

	// Send current screen state for each pane (enables reattach)
	for _, p := range sess.Panes {
		rendered := p.RenderScreen()
		cc.Send(&Message{Type: MsgTypePaneOutput, PaneID: p.ID, PaneData: []byte(rendered)})
	}

	sess.clients = append(sess.clients, cc)
	sess.mu.Unlock()

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
