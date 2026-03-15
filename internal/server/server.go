package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

// Default terminal dimensions when the client doesn't report a size.
const (
	DefaultTermCols = 80
	DefaultTermRows = 24
)

// DefaultOutputLines is how many lines `amux output` shows by default.
const DefaultOutputLines = 50

// Session holds the state for one amux session.
type Session struct {
	Name    string
	Window  *mux.Window
	Panes   []*mux.Pane // flat list for quick lookup
	clients []*ClientConn
	counter atomic.Uint32
	mu      sync.Mutex
	shutdown atomic.Bool
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
// It forwards OSC 52 clipboard sequences to all connected clients.
func (s *Session) clipboardCallback() func(paneID uint32, data []byte) {
	return func(paneID uint32, data []byte) {
		if s.shutdown.Load() {
			return
		}
		s.broadcast(&Message{Type: MsgTypeClipboard, PaneID: paneID, PaneData: data})
	}
}

// broadcastPaneOutput sends raw PTY output for one pane to all clients.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte) {
	s.broadcast(&Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data})
}

// broadcastLayout sends the current layout snapshot to all clients.
func (s *Session) broadcastLayout() {
	s.mu.Lock()
	if s.Window == nil {
		s.mu.Unlock()
		return
	}
	snap := s.Window.SnapshotLayout(s.Name)
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	msg := &Message{Type: MsgTypeLayout, Layout: snap}
	for _, c := range clients {
		c.Send(msg)
	}
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
			if s.Window != nil {
				s.Window.ClosePane(paneID)
			}
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

	if s.Window == nil {
		return ""
	}

	paneMap := make(map[uint32]render.PaneData, len(s.Panes))
	for _, p := range s.Panes {
		paneMap[p.ID] = &serverPaneData{p: p}
	}

	totalH := s.Window.Height + render.GlobalBarHeight
	comp := render.NewCompositor(s.Window.Width, totalH, s.Name)

	var activePaneID uint32
	if s.Window.ActivePane != nil {
		activePaneID = s.Window.ActivePane.ID
	}

	root := s.Window.Root
	if s.Window.ZoomedPaneID != 0 {
		root = mux.NewLeafByID(s.Window.ZoomedPaneID, 0, 0, s.Window.Width, s.Window.Height)
	}

	raw := string(comp.RenderFull(root, activePaneID, func(id uint32) render.PaneData {
		return paneMap[id]
	}))

	if stripANSI {
		return render.MaterializeGrid(raw, s.Window.Width, totalH)
	}

	return raw
}

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
	if sess.Window == nil {
		sess.mu.Unlock()
		sess.shutdown.Store(false)
		return fmt.Errorf("no window to checkpoint")
	}

	cp := &checkpoint.ServerCheckpoint{
		SessionName: sess.Name,
		Counter:     sess.counter.Load(),
		Layout:      *sess.Window.SnapshotLayout(sess.Name),
	}

	for _, p := range sess.Panes {
		pc := checkpoint.PaneCheckpoint{
			ID:     p.ID,
			Meta:   p.Meta,
			PtmxFd: p.PtmxFd(),
			PID:    p.ProcessPid(),
			Screen: p.RenderScreen(),
		}
		cell := sess.Window.Root.FindPane(p.ID)
		if cell != nil {
			pc.Cols = cell.W
			pc.Rows = mux.PaneContentHeight(cell.H)
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
	sess.counter.Store(cp.Counter)

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
				if sess.Window != nil {
					sess.Window.ClosePane(paneID)
				}
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

	// Rebuild window from layout snapshot
	sess.Window = mux.RebuildFromSnapshot(cp.Layout, paneMap)

	// Start PTY read loops for all restored panes
	for _, p := range sess.Panes {
		p.Start()
	}

	// Force TUI apps (Claude Code, vim, etc.) to do a full screen redraw.
	// Without this, incremental updates buffered during the reload window
	// corrupt the display because they assume the pre-reload screen state.
	// We resize each PTY (shrink then restore) to trigger SIGWINCH. The
	// delay lets readLoops drain buffered PTY output first; the gap between
	// resizes prevents SIGWINCH coalescing.
	go func() {
		time.Sleep(500 * time.Millisecond)
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if sess.Window == nil {
			return
		}
		// First pass: shrink by 1 row
		for _, p := range sess.Panes {
			cell := sess.Window.Root.FindPane(p.ID)
			if cell != nil {
				p.Resize(cell.W, mux.PaneContentHeight(cell.H)-1)
			}
		}
		// Let TUI apps process the first SIGWINCH
		sess.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		sess.mu.Lock()
		// Second pass: restore original size
		for _, p := range sess.Panes {
			cell := sess.Window.Root.FindPane(p.ID)
			if cell != nil {
				p.Resize(cell.W, mux.PaneContentHeight(cell.H))
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

	sess.mu.Lock()

	// Create the first pane and window if none exist
	var newPane *mux.Pane
	if sess.Window == nil {
		// Reserve 1 row for global status bar, 1 for per-pane status
		layoutH := rows - 1 // global bar
		paneH := mux.PaneContentHeight(layoutH)
		pane, err := sess.createPane(s, cols, paneH)
		if err != nil {
			sess.mu.Unlock()
			conn.Close()
			return
		}
		sess.Window = mux.NewWindow(pane, cols, layoutH)
		newPane = pane
	}

	// Send layout snapshot so client can build its rendering state
	snap := sess.Window.SnapshotLayout(sess.Name)
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
