package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/remote"
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
	clientCounter  atomic.Uint32
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
	// Only modified inside the session event loop (no mutex needed).
	paneOutputSubs map[uint32][]chan struct{}

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
	// Only accessed from the session event loop (no mutex needed).
	eventSubs []*eventSub

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

	// Crash checkpoint — non-blocking trigger from broadcastLayout().
	// The crashCheckpointLoop goroutine debounces and writes periodically.
	crashCheckpointTrigger chan struct{}
	crashCheckpointStop    chan struct{}
	crashCheckpointDone    chan struct{} // closed when loop exits

	// Async session event loop — phase 1 serializes callback-driven writes.
	sessionEvents    chan sessionEvent
	sessionEventStop chan struct{}
	sessionEventDone chan struct{}

	// Exit-unattached: server exits when all clients disconnect after
	// at least one has connected. Used by test harness to avoid orphans.
	hadClient  bool    // true after first interactive client attaches
	exitServer *Server // back-reference to trigger shutdown
}

// buildCrashCheckpoint builds a crash checkpoint from the current session state.
// Unlike the hot-reload checkpoint, this omits FDs/PIDs (they can't survive a crash)
// and captures screen content and cwd for each pane.
func (s *Session) buildCrashCheckpoint() *checkpoint.CrashCheckpoint {
	s.mu.Lock()
	if len(s.Windows) == 0 {
		s.mu.Unlock()
		return nil
	}

	idleSnap := make(map[uint32]bool) // empty — crash checkpoint doesn't need idle state
	snap := s.snapshotLayoutLocked(idleSnap)
	cp := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   s.Name,
		Counter:       s.counter.Load(),
		WindowCounter: s.windowCounter.Load(),
		Generation:    s.generation.Load(),
		Layout:        *snap,
		Timestamp:     time.Now(),
	}

	// Collect pane state and PIDs under the lock (fast).
	// Cwd resolution (lsof on macOS) happens after releasing the lock
	// to avoid blocking session operations for hundreds of milliseconds.
	type pidEntry struct {
		index int
		pid   int
	}
	var cwdWork []pidEntry

	for _, p := range s.Panes {
		ps := checkpoint.CrashPaneState{
			ID:        p.ID,
			Meta:      p.Meta,
			Screen:    p.RenderScreen(),
			CreatedAt: p.CreatedAt(),
			IsProxy:   p.IsProxy(),
		}

		if p.Meta.Minimized {
			ps.Cols, ps.Rows = p.EmulatorSize()
		} else {
			for _, w := range s.Windows {
				if cell := w.Root.FindPane(p.ID); cell != nil {
					ps.Cols = cell.W
					ps.Rows = mux.PaneContentHeight(cell.H)
					break
				}
			}
		}

		if !p.IsProxy() {
			cwdWork = append(cwdWork, pidEntry{index: len(cp.PaneStates), pid: p.ProcessPid()})
		}

		cp.PaneStates = append(cp.PaneStates, ps)
	}
	s.mu.Unlock()

	// Resolve cwds outside the lock (lsof can be slow on macOS)
	for _, w := range cwdWork {
		cp.PaneStates[w.index].Cwd = mux.PaneCwd(w.pid)
	}

	return cp
}

// startCrashCheckpointLoop starts the background goroutine that writes crash
// checkpoints on layout changes (debounced 500ms) and periodic screen content
// snapshots (every 30s).
func (s *Session) startCrashCheckpointLoop() {
	s.crashCheckpointTrigger = make(chan struct{}, 1)
	s.crashCheckpointStop = make(chan struct{})
	s.crashCheckpointDone = make(chan struct{})
	go s.crashCheckpointLoop()
}

// crashCheckpointLoop debounces layout-change triggers and writes crash
// checkpoints periodically. Runs until crashCheckpointStop is closed.
func (s *Session) crashCheckpointLoop() {
	defer close(s.crashCheckpointDone)

	const debounce = 500 * time.Millisecond
	const periodic = 30 * time.Second

	debounceTimer := time.NewTimer(debounce)
	debounceTimer.Stop()
	periodicTicker := time.NewTicker(periodic)
	defer periodicTicker.Stop()

	for {
		select {
		case <-s.crashCheckpointStop:
			debounceTimer.Stop()
			return

		case <-s.crashCheckpointTrigger:
			// Layout changed — debounce before writing
			debounceTimer.Reset(debounce)

		case <-debounceTimer.C:
			s.writeCrashCheckpoint()

		case <-periodicTicker.C:
			s.writeCrashCheckpoint()
		}
	}
}

// writeCrashCheckpoint builds and writes a crash checkpoint to disk.
// Skips writing if the session is shutting down (clean shutdown removes the checkpoint).
func (s *Session) writeCrashCheckpoint() {
	if s.shutdown.Load() {
		return
	}
	cp := s.buildCrashCheckpoint()
	if cp == nil {
		return
	}
	if err := checkpoint.WriteCrash(cp, s.Name); err != nil {
		fmt.Fprintf(os.Stderr, "amux: crash checkpoint write: %v\n", err)
	}
}

// removeClient removes a client from the session and recalculates
// the session size in case the largest client disconnected.
func (s *Session) removeClient(cc *ClientConn) {
	s.mu.Lock()
	for i, c := range s.clients {
		if c == cc {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			break
		}
	}
	shouldExit := s.exitServer != nil && s.exitServer.Env.ExitUnattached && s.hadClient && len(s.clients) == 0 && !s.shutdown.Load()
	s.recalcSizeLocked()
	s.mu.Unlock()
	s.broadcastLayout()
	if shouldExit {
		// Async: removeClient may run inside the session event loop;
		// calling Shutdown synchronously would deadlock because
		// Shutdown waits for the event loop to finish.
		go s.exitServer.Shutdown()
	}
}

// BuildVersion is set by main at startup for version reporting in status.
var BuildVersion string

// Server listens on a Unix socket and manages sessions.
type Server struct {
	Env      ServerEnv
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
	sess.takenOverPanes = make(map[uint32]bool)
	sess.startCrashCheckpointLoop()
	sess.startEventLoop()
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
	sess.exitServer = s

	return s, nil
}

// NewServerFromCrashCheckpoint restores a server from a crash checkpoint.
// Creates a new Unix socket, spawns fresh shells for each pane (with cwd
// restored), and replays last-known screen content. Proxy panes are recreated
// with "reconnecting" status.
func NewServerFromCrashCheckpoint(sessionName string, cp *checkpoint.CrashCheckpoint) (*Server, error) {
	sockDir := SocketDir()
	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}

	sockPath := SocketPath(sessionName)
	// Clean up any stale socket from the crashed server
	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}
	os.Chmod(sockPath, 0700)

	sess := newSession(sessionName)
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)

	s := &Server{
		listener: listener,
		sessions: map[string]*Session{sessionName: sess},
		sockPath: sockPath,
	}
	sess.exitServer = s

	// Restore panes — spawn fresh shells (FDs/PIDs are lost on crash)
	paneMap := make(map[uint32]*mux.Pane, len(cp.PaneStates))
	for _, ps := range cp.PaneStates {
		var pane *mux.Pane

		onOutput := sess.paneOutputCallback()
		onExit := sess.paneExitCallback(s)

		if ps.IsProxy {
			// Restore proxy pane with frozen content, mark as reconnecting.
			// The remote manager will re-establish the SSH connection.
			meta := ps.Meta
			meta.Remote = string(remote.Reconnecting)
			pane = mux.NewProxyPane(ps.ID, meta, ps.Cols, ps.Rows,
				onOutput, onExit,
				func(data []byte) (int, error) {
					if sess.RemoteManager != nil {
						return len(data), sess.RemoteManager.SendInput(ps.ID, data)
					}
					return len(data), nil // drop input until reconnected
				},
			)
		} else {
			// Spawn fresh shell with restored cwd
			meta := ps.Meta
			meta.Dir = ps.Cwd // set cwd for the new shell
			var newErr error
			pane, newErr = mux.NewPane(ps.ID, meta, ps.Cols, ps.Rows, sessionName,
				onOutput, onExit,
			)
			if newErr != nil {
				fmt.Fprintf(os.Stderr, "amux: crash recovery: skipping pane %d: %v\n", ps.ID, newErr)
				continue
			}
		}

		pane.SetOnClipboard(sess.clipboardCallback())

		if !ps.CreatedAt.IsZero() {
			pane.SetCreatedAt(ps.CreatedAt)
		}

		// Replay last-known screen content so user/agent sees where they left off
		if ps.Screen != "" {
			pane.ReplayScreen(ps.Screen)
		}

		paneMap[ps.ID] = pane
		sess.Panes = append(sess.Panes, pane)
	}

	if len(sess.Panes) == 0 {
		listener.Close()
		return nil, fmt.Errorf("no panes restored from crash checkpoint")
	}

	// Rebuild windows from snapshot
	if len(cp.Layout.Windows) > 0 {
		for _, ws := range cp.Layout.Windows {
			w := mux.RebuildWindowFromSnapshot(ws, cp.Layout.Width, cp.Layout.Height, paneMap)
			sess.Windows = append(sess.Windows, w)
		}
		sess.ActiveWindowID = cp.Layout.ActiveWindowID
	} else {
		// Fallback: single window from legacy root
		w := mux.RebuildFromSnapshot(cp.Layout, paneMap)
		winID := sess.windowCounter.Add(1)
		w.ID = winID
		w.Name = fmt.Sprintf(WindowNameFormat, winID)
		sess.Windows = append(sess.Windows, w)
		sess.ActiveWindowID = winID
	}

	// Start PTY read loops for all panes
	for _, p := range sess.Panes {
		if !p.IsProxy() {
			p.Start()
		}
	}

	// Remove crash checkpoint — recovery is complete
	checkpoint.RemoveCrash(sessionName)

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

		if sess.sessionEventStop != nil {
			close(sess.sessionEventStop)
			<-sess.sessionEventDone
			sess.sessionEventStop = nil
		}

		// Stop crash checkpoint loop and wait for it to exit.
		// The shutdown flag prevents any further writes.
		// Nil out after close so Shutdown() is safe to call twice
		// (exit-unattached can race with signal-based shutdown).
		if sess.crashCheckpointStop != nil {
			close(sess.crashCheckpointStop)
			<-sess.crashCheckpointDone
			sess.crashCheckpointStop = nil
		}

		// Clean shutdown: remove crash checkpoint (no recovery needed)
		checkpoint.RemoveCrash(sess.Name)

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
	cc.ID = fmt.Sprintf("client-%d", sess.clientCounter.Add(1))

	cols, rows := msg.Cols, msg.Rows
	if cols <= 0 {
		cols = DefaultTermCols
	}
	if rows <= 0 {
		rows = DefaultTermRows
	}
	res := sess.enqueueAttachClient(s, cc, cols, rows)
	if res.err != nil {
		conn.Close()
		return
	}

	cc.Send(&Message{Type: MsgTypeLayout, Layout: res.snap})
	for _, pr := range res.paneRenders {
		cc.Send(&Message{Type: MsgTypePaneOutput, PaneID: pr.paneID, PaneData: pr.data})
	}

	// Broadcast layout to other clients so they see the updated dimensions.
	sess.broadcastLayout()

	if res.newPane != nil {
		res.newPane.Start()
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
