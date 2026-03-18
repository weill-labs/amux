package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
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
	Hooks *hooks.Registry
	idle  *IdleTracker

	// Event stream — used by `amux events` for push-based notifications.
	events *EventBus

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

// BuildVersion is set by main at startup for version reporting in status.
var BuildVersion string

// Server listens on a Unix socket and manages sessions.
type Server struct {
	listener net.Listener
	sessions map[string]*Session
	sockPath string
	mu       sync.Mutex
}

// firstSession returns any session from the map, or nil.
// Caller must hold s.mu.
func (s *Server) firstSession() *Session {
	for _, sess := range s.sessions {
		return sess
	}
	return nil
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
	sess.idle = NewIdleTracker()
	sess.events = NewEventBus()
	sess.takenOverPanes = make(map[uint32]bool)
	return sess
}

// NewServer creates a new server listening on a Unix socket for the given session.
func NewServer(sessionName string) (*Server, error) {
	sockDir := SocketDir()
	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}

	CleanStaleSockets()

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
func (s *Server) SetupRemoteManager(cfg *config.Config, buildHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.SetupRemoteManager(cfg, buildHash)
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
	sess := s.firstSession()
	s.mu.Unlock()

	if sess == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return
	}

	cc.handleCommand(s, sess, msg)
}
