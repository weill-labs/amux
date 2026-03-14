package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

// paneAccents — Catppuccin Mocha subset for pane colors.
var paneAccents = []string{
	"f38ba8", "fab387", "f9e2af", "a6e3a1",
	"94e2d5", "89b4fa", "b4befe", "cba6f7",
}

// Session holds the state for one amux session.
type Session struct {
	Name       string
	Window     *mux.Window
	Panes      []*mux.Pane // flat list for quick lookup
	clients    []*ClientConn
	compositor *render.Compositor
	counter    uint32
	mu         sync.Mutex
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

// renderAndBroadcast does a full composite render and sends to all clients.
func (s *Session) renderAndBroadcast() {
	s.mu.Lock()
	if s.Window == nil || len(s.clients) == 0 {
		s.mu.Unlock()
		return
	}
	data := s.compositor.RenderFull(s.Window.Root, s.Window.ActivePane)
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	msg := &Message{Type: MsgTypeRender, RenderData: data}
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

// createPane creates a new pane with auto-assigned metadata.
func (s *Session) createPane(srv *Server, cols, rows int) (*mux.Pane, error) {
	s.counter++
	meta := mux.PaneMeta{
		Name:  fmt.Sprintf("pane-%d", s.counter),
		Host:  "local",
		Color: paneAccents[(s.counter-1)%uint32(len(paneAccents))],
	}

	pane, err := mux.NewPane(s.counter, meta, cols, rows,
		func(paneID uint32, data []byte) {
			// Any pane output triggers a full re-render
			s.renderAndBroadcast()
		},
		func(paneID uint32) {
			s.mu.Lock()
			remaining := len(s.Panes)
			s.mu.Unlock()
			if remaining <= 1 {
				// Last pane exited
				s.broadcast(&Message{Type: MsgTypeExit})
				srv.Shutdown()
			}
		},
	)
	if err != nil {
		return nil, err
	}

	s.Panes = append(s.Panes, pane)
	return pane, nil
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

	// Clean up stale socket
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
		sess.mu.Lock()
		for _, p := range sess.Panes {
			p.Close()
		}
		sess.mu.Unlock()
	}
}

// handleConn reads the first message to determine if this is an interactive
// attach or a one-shot command.
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

	sess.mu.Lock()

	cols, rows := msg.Cols, msg.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	// Create the first pane and window if none exist
	if sess.Window == nil {
		pane, err := sess.createPane(s, cols, rows)
		if err != nil {
			sess.mu.Unlock()
			conn.Close()
			return
		}
		sess.Window = mux.NewWindow(pane, cols, rows)
		sess.compositor = render.NewCompositor(cols, rows)
	}

	// Send current screen state to the new client (enables reattach)
	var screen []byte
	screen = append(screen, []byte(fmt.Sprintf("\033]0;amux: %s\007", sess.Name))...)
	screen = append(screen, sess.compositor.RenderFull(sess.Window.Root, sess.Window.ActivePane)...)
	cc.Send(&Message{Type: MsgTypeRender, RenderData: screen})

	sess.clients = append(sess.clients, cc)
	sess.mu.Unlock()

	// Blocks until client disconnects or detaches
	cc.readLoop(s, sess)
}

// handleOneShot processes a single command and closes the connection.
func (s *Server) handleOneShot(conn net.Conn, msg *Message) {
	cc := NewClientConn(conn)
	defer cc.Close()

	sessionName := "default"
	s.mu.Lock()
	sess, ok := s.sessions[sessionName]
	s.mu.Unlock()

	if !ok {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return
	}

	cc.handleCommand(s, sess, msg)
}
