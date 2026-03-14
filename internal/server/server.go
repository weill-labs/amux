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

// paneAccents from session.go — Catppuccin Mocha subset for pane colors.
var paneAccents = []string{
	"f38ba8", "fab387", "f9e2af", "a6e3a1",
	"94e2d5", "89b4fa", "b4befe", "cba6f7",
}

// Session holds the state for one amux session.
type Session struct {
	Name    string
	Panes   []*mux.Pane
	clients []*ClientConn
	counter uint32
	mu      sync.Mutex
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

// Server listens on a Unix socket and manages sessions.
type Server struct {
	listener net.Listener
	sessions map[string]*Session
	sockPath string
	mu       sync.Mutex
}

// SocketDir returns the directory for amux Unix sockets.
// Uses /tmp/ (not os.TempDir()) for short stable paths, matching tmux convention.
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
			// Dead socket — remove it
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

	// Create the first pane if none exist
	if len(sess.Panes) == 0 {
		cols, rows := msg.Cols, msg.Rows
		if cols <= 0 {
			cols = 80
		}
		if rows <= 0 {
			rows = 24
		}

		sess.counter++
		meta := mux.PaneMeta{
			Name:  fmt.Sprintf("pane-%d", sess.counter),
			Host:  "local",
			Color: paneAccents[(sess.counter-1)%uint32(len(paneAccents))],
		}

		pane, err := mux.NewPane(sess.counter, meta, cols, rows,
			func(paneID uint32, data []byte) {
				// Forward raw PTY output to all clients
				sess.broadcast(&Message{Type: MsgTypeRender, RenderData: data})
			},
			func(paneID uint32) {
				// Shell exited — notify clients
				sess.broadcast(&Message{Type: MsgTypeExit})
				s.Shutdown()
			},
		)
		if err != nil {
			sess.mu.Unlock()
			conn.Close()
			return
		}
		sess.Panes = append(sess.Panes, pane)
	}

	// Send current screen state to the new client (enables reattach)
	if len(sess.Panes) > 0 {
		screen := render.ClearScreen()
		rendered := sess.Panes[0].RenderScreen()
		screen = append(screen, []byte(rendered)...)
		cc.Send(&Message{Type: MsgTypeRender, RenderData: screen})
	}

	sess.clients = append(sess.clients, cc)
	sess.mu.Unlock()

	// Blocks until client disconnects or detaches
	cc.readLoop(sess)
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

	cc.handleCommand(sess, msg)
}
