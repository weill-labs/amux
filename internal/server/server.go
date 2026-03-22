package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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
	startedAt      time.Time
	Windows        []*mux.Window // ordered list of windows
	ActiveWindowID uint32        // which window is displayed
	Panes          []*mux.Pane   // flat list of ALL panes across all windows
	clients        []*clientConn
	connectionLog  *ConnectionLog
	sizeClient     atomic.Pointer[clientConn] // latest active client whose terminal size owns the session
	clientCounter  atomic.Uint32
	counter        atomic.Uint32 // pane ID counter
	windowCounter  atomic.Uint32 // window ID counter
	shutdown       atomic.Bool
	inputTarget    atomic.Pointer[mux.Pane] // cached active pane for low-latency input writes

	// Layout generation counter — incremented on every broadcastLayout.
	// Used by wait-layout to block until a layout change occurs.
	generation    atomic.Uint64
	waiterCounter atomic.Uint64
	layoutWaiters map[uint64]layoutWaiter

	// Per-pane output subscribers — used by wait-for to block until
	// a substring appears in a pane's screen content.
	// Only modified inside the session event loop (no mutex needed).
	paneOutputSubs map[uint32][]chan struct{}

	// Clipboard generation counter — incremented on every OSC 52 clipboard
	// event. Used by wait-clipboard to block until a clipboard write occurs.
	clipboardGen     atomic.Uint64
	lastClipboardB64 string // actor-owned clipboard payload (base64)
	clipboardWaiters map[uint64]clipboardWaiter

	// Hook completion history — incremented on every hook result.
	// Used by hook-gen and wait-hook to block until matching hook work finishes.
	hookGen     atomic.Uint64
	hookResults []hookResultRecord
	hookWaiters map[uint64]hookWaiter

	// Hook system — session-level, not checkpointed.
	Hooks  *hooks.Registry
	idle   *idleTracker
	vtIdle *VTIdleTracker

	// Event stream — used by `amux events` for push-based notifications.
	// Only accessed from the session event loop (no mutex needed).
	eventSubs []*eventSub

	// Per-pane paced input queues serialize delayed send-keys batches.
	// Only accessed from the session event loop (no mutex needed).
	pacedPanes map[uint32]*pacedInputQueue
	// Pending cleanup kills waiting to escalate from SIGTERM to SIGKILL.
	// Only accessed from the session event loop.
	pendingKillCleanups map[uint32]*time.Timer

	// Remote pane management — manages SSH connections to remote hosts.
	// Nil when no config is loaded or no remote hosts are defined.
	RemoteManager *remote.Manager

	// SSH takeover tracking — pane IDs that have already been taken over.
	// Prevents duplicate takeover if the remote emits the sequence twice.
	// Only accessed from the session event loop.
	takenOverPanes map[uint32]bool

	// Session notice — server-owned transient message shown in the global bar.
	// Used for async failures such as SSH takeover attach errors.
	notice      string
	noticeToken uint64

	// Capture forwarding — routes capture requests through the attached
	// interactive client so the result reflects client-side emulator state.
	// The event loop owns the single in-flight request and queued requests.
	captureCounter atomic.Uint64
	captureCurrent *captureRequest
	captureQueue   []*captureRequest

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

	// wantShutdown is set by event handlers that want the server to exit
	// (last client disconnected, last pane exited). The event loop checks
	// this after each event and triggers shutdown asynchronously — never
	// from inside the handler, which would deadlock.
	wantShutdown    bool
	scrollbackLines int
}

// buildCrashCheckpoint builds a crash checkpoint from the current session state.
// Unlike the hot-reload checkpoint, this omits FDs/PIDs (they can't survive a crash)
// and captures retained history, screen content, and cwd for each pane.
func (s *Session) buildCrashCheckpoint() *checkpoint.CrashCheckpoint {
	type pidEntry struct {
		index int
		pane  *mux.Pane
		pid   int
	}
	type crashSnapshot struct {
		cp      *checkpoint.CrashCheckpoint
		cwdWork []pidEntry
	}

	snap, err := enqueueSessionQuery(s, func(s *Session) (crashSnapshot, error) {
		if len(s.Windows) == 0 {
			return crashSnapshot{}, nil
		}

		idleSnap := make(map[uint32]bool)
		layout := s.snapshotLayout(idleSnap)
		cp := &checkpoint.CrashCheckpoint{
			Version:       checkpoint.CrashVersion,
			SessionName:   s.Name,
			Counter:       s.counter.Load(),
			WindowCounter: s.windowCounter.Load(),
			Generation:    s.generation.Load(),
			Layout:        *layout,
			Timestamp:     time.Now(),
		}

		var cwdWork []pidEntry
		for _, p := range s.Panes {
			history, screen, _ := p.HistoryScreenSnapshot()
			ps := checkpoint.CrashPaneState{
				ID:           p.ID,
				Meta:         p.Meta,
				ManualBranch: p.MetaManualBranch(),
				History:      history,
				Screen:       screen,
				CreatedAt:    p.CreatedAt(),
				IsProxy:      p.IsProxy(),
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
				cwdWork = append(cwdWork, pidEntry{index: len(cp.PaneStates), pane: p, pid: p.ProcessPid()})
			}

			cp.PaneStates = append(cp.PaneStates, ps)
		}

		return crashSnapshot{cp: cp, cwdWork: cwdWork}, nil
	})
	if err != nil || snap.cp == nil {
		return nil
	}

	// Resolve cwds outside the lock (lsof can be slow on macOS)
	for _, w := range snap.cwdWork {
		snap.cp.PaneStates[w.index].Cwd = mux.PaneCwd(w.pid)
		status := w.pane.AgentStatus()
		snap.cp.PaneStates[w.index].WasIdle = status.Idle
		snap.cp.PaneStates[w.index].Command = status.CurrentCommand
	}

	return snap.cp
}

// startCrashCheckpointLoop starts the background goroutine that writes crash
// checkpoints on layout and pane-output changes (debounced 500ms) and periodic
// full snapshots (every 30s).
func (s *Session) startCrashCheckpointLoop() {
	// Ensure the checkpoint directory exists before tests or tooling watch it.
	// Writes still happen lazily on layout changes.
	if err := os.MkdirAll(checkpoint.CrashCheckpointDir(), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "amux: crash checkpoint dir: %v\n", err)
	}
	s.crashCheckpointTrigger = make(chan struct{}, 1)
	s.crashCheckpointStop = make(chan struct{})
	s.crashCheckpointDone = make(chan struct{})
	go s.crashCheckpointLoop()
}

// crashCheckpointLoop debounces checkpoint triggers and writes crash
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
	if err := checkpoint.WriteCrash(cp, s.Name, s.startedAt); err != nil {
		fmt.Fprintf(os.Stderr, "amux: crash checkpoint write: %v\n", err)
	}
}

func (s *Session) hasClient(cc *clientConn) bool {
	for _, c := range s.clients {
		if c == cc {
			return true
		}
	}
	return false
}

func (s *Session) currentSizeClient() *clientConn {
	return s.sizeClient.Load()
}

func (s *Session) noteClientActivity(cc *clientConn) bool {
	if cc == nil || !s.hasClient(cc) || s.currentSizeClient() == cc {
		return false
	}
	s.sizeClient.Store(cc)
	return true
}

func (s *Session) effectiveSizeClient() *clientConn {
	if cc := s.currentSizeClient(); cc != nil && s.hasClient(cc) {
		return cc
	}
	if len(s.clients) == 0 {
		s.sizeClient.Store(nil)
		return nil
	}
	// Fall back to the most recently attached remaining client.
	cc := s.clients[len(s.clients)-1]
	s.sizeClient.Store(cc)
	return cc
}

// removeClient removes a client from the session and recalculates
// the session size if the active size owner disconnected.
func (s *Session) removeClient(cc *clientConn) {
	s.removeClientWithLayout(cc, true)
}

func (s *Session) removeClientWithoutLayout(cc *clientConn) {
	s.removeClientWithLayout(cc, false)
}

func (s *Session) removeClientWithLayout(cc *clientConn, broadcastLayout bool) {
	for i, c := range s.clients {
		if c == cc {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			break
		}
	}
	if s.currentSizeClient() == cc {
		s.sizeClient.Store(nil)
	}
	if !broadcastLayout {
		return
	}
	shouldExit := s.exitServer != nil && s.exitServer.Env.ExitUnattached && s.hadClient && len(s.clients) == 0 && !s.shutdown.Load()
	s.recalcSize()
	if shouldExit {
		s.wantShutdown = true
	}
	s.broadcastLayoutNow()
}

// BuildVersion is set by main at startup for version reporting in status.
var BuildVersion string

// Server listens on a Unix socket and manages sessions.
type Server struct {
	Env      ServerEnv
	listener net.Listener
	sessions map[string]*Session
	sockPath string

	// Shutdown is serialized with atomics so concurrent callers all observe
	// one cleanup pass and later callers can wait for completion.
	shutdownState atomic.Uint32
	shutdownDone  chan struct{}

	// attachBootstrapHook is a test-only hook invoked after the initial
	// attach replay is sent but before bootstrap flushes queued messages.
	attachBootstrapHook func()
}

// firstSession returns any session from the immutable session map, or nil.
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

func newSessionWithScrollback(name string, scrollbackLines int) *Session {
	sess := &Session{
		Name:            name,
		startedAt:       time.Now(),
		scrollbackLines: scrollbackLines,
		connectionLog:   newConnectionLog(defaultConnectionLogSize),
	}
	sess.Hooks = hooks.NewRegistry()
	sess.idle = newIdleTracker()
	sess.vtIdle = NewVTIdleTracker()
	sess.takenOverPanes = make(map[uint32]bool)
	sess.layoutWaiters = make(map[uint64]layoutWaiter)
	sess.clipboardWaiters = make(map[uint64]clipboardWaiter)
	sess.hookWaiters = make(map[uint64]hookWaiter)
	sess.startCrashCheckpointLoop()
	sess.startEventLoop()
	return sess
}

// NewServerWithScrollback creates a new server with an explicit retained
// scrollback limit for all panes in the session.
func NewServerWithScrollback(sessionName string, scrollbackLines int) (*Server, error) {
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

	sess := newSessionWithScrollback(sessionName, scrollbackLines)

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sessionName: sess},
		sockPath:     sockPath,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = s

	return s, nil
}

// NewServerFromCrashCheckpointWithScrollback restores a server from a crash
// checkpoint with an explicit retained scrollback limit.
func NewServerFromCrashCheckpointWithScrollback(sessionName string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int) (*Server, error) {
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

	sess := newSessionWithScrollback(sessionName, scrollbackLines)
	sess.startedAt = cp.Timestamp
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sessionName: sess},
		sockPath:     sockPath,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = s

	// Restore panes — spawn fresh shells (FDs/PIDs are lost on crash)
	paneMap := make(map[uint32]*mux.Pane, len(cp.PaneStates))
	for _, ps := range cp.PaneStates {
		var pane *mux.Pane

		onOutput := sess.paneOutputCallback()
		onExit := sess.paneExitCallback()

		if ps.IsProxy {
			// Restore proxy pane with frozen content, mark as reconnecting.
			// The remote manager will re-establish the SSH connection.
			meta := ps.Meta
			meta.Remote = string(remote.Reconnecting)
			pane = mux.NewProxyPaneWithScrollback(ps.ID, meta, ps.Cols, ps.Rows, sess.scrollbackLines,
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
			pane, newErr = mux.NewPaneWithScrollback(ps.ID, meta, ps.Cols, ps.Rows, sessionName, sess.scrollbackLines,
				onOutput, onExit,
			)
			if newErr != nil {
				fmt.Fprintf(os.Stderr, "amux: crash recovery: skipping pane %d: %v\n", ps.ID, newErr)
				continue
			}
		}

		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())
		restorePaneRuntimeState(pane, ps.ManualBranch)

		if !ps.CreatedAt.IsZero() {
			pane.SetCreatedAt(ps.CreatedAt)
		}
		pane.SetRetainedHistory(restoreCrashHistory(ps))
		replayCrashRecoveredScreen(pane, ps)

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
	sess.refreshInputTarget()

	// Start PTY read loops for all panes
	for _, p := range sess.Panes {
		if !p.IsProxy() {
			p.Start()
		}
	}

	// Remove crash checkpoint — recovery is complete
	_ = checkpoint.RemoveCrashFile(crashPath)

	return s, nil
}

// SetupRemoteManager initializes the remote manager for all sessions.
func (s *Server) SetupRemoteManager(cfg *config.Config, buildHash string) {
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

func (s *Server) Shutdown() {
	if s == nil {
		return
	}
	for {
		switch s.shutdownState.Load() {
		case 0:
			if s.shutdownState.CompareAndSwap(0, 1) {
				s.shutdown()
				s.shutdownState.Store(2)
				if s.shutdownDone != nil {
					close(s.shutdownDone)
				}
				return
			}
		case 1:
			if s.shutdownDone != nil {
				<-s.shutdownDone
			}
			return
		case 2:
			return
		}
	}
}

// shutdown cleans up the server socket, remote connections, and panes.
func (s *Server) shutdown() {
	s.listener.Close()
	os.Remove(s.sockPath)

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
		_ = checkpoint.RemoveCrashFile(checkpoint.CrashCheckpointPathTimestamped(sess.Name, sess.startedAt))

		if sess.RemoteManager != nil {
			sess.RemoteManager.Shutdown()
		}
		panes := make([]*mux.Pane, len(sess.Panes))
		copy(panes, sess.Panes)
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

	sess, ok := s.sessions[sessionName]

	if !ok {
		conn.Close()
		return
	}

	cc := newClientConn(conn)
	cc.ID = fmt.Sprintf("client-%d", sess.clientCounter.Add(1))
	cc.initTypeKeyQueue()
	cc.setNegotiatedCapabilities(proto.NegotiateClientCapabilities(msg.AttachCapabilities))
	cc.startBootstrap()

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
	bootstrapSeqs := make(map[uint32]uint64, len(res.paneSnapshots))
	for _, ps := range res.paneSnapshots {
		if len(ps.history) > 0 {
			cc.Send(&Message{Type: MsgTypePaneHistory, PaneID: ps.paneID, History: ps.history})
		}
		cc.Send(&Message{Type: MsgTypePaneOutput, PaneID: ps.paneID, PaneData: ps.screen})
		bootstrapSeqs[ps.paneID] = ps.outputSeq
	}
	if s.attachBootstrapHook != nil {
		s.attachBootstrapHook()
	}
	cc.finishBootstrap(bootstrapSeqs)

	// Broadcast layout to other clients so they see the updated dimensions.
	sess.broadcastLayout()

	if res.newPane != nil {
		res.newPane.Start()
	}

	cc.readLoop(s, sess)
}

func (s *Server) handleOneShot(conn net.Conn, msg *Message) {
	cc := newClientConn(conn)
	defer cc.Close()

	sess := s.firstSession()

	if sess == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return
	}

	cc.handleCommand(s, sess, msg)
}

// EnsureInitialWindow creates the first window and pane for the first session
// if the session is currently empty. This is used by takeover-managed startup,
// where a remote server must be ready before any interactive client attaches.
func (s *Server) EnsureInitialWindow(cols, rows int) error {
	sess := s.firstSession()
	if sess == nil {
		return fmt.Errorf("no session")
	}

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		initRes, err := sess.ensureInitialWindowLocked(s, cols, rows)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if !initRes.layoutChanged {
			return commandMutationResult{}
		}
		res := commandMutationResult{broadcastLayout: true}
		if initRes.newPane != nil {
			res.startPanes = []*mux.Pane{initRes.newPane}
		}
		return res
	})
	if res.err != nil {
		return res.err
	}
	for _, pane := range res.startPanes {
		pane.Start()
	}
	return nil
}
