package server

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/debugowner"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/termprofile"
)

// Default terminal dimensions when the client doesn't report a size.
const (
	DefaultTermCols = proto.DefaultTermCols
	DefaultTermRows = proto.DefaultTermRows
)

// DefaultOutputLines is how many lines `amux output` shows by default.
const DefaultOutputLines = 50

// DefaultSessionName is the implicit session used when callers do not specify one.
const DefaultSessionName = "main"

// WindowNameFormat is the default name for auto-created windows.
const WindowNameFormat = "window-%d"

// Session holds the state for one amux session.
type Session struct {
	Name           string
	startedAt      time.Time
	Windows        []*mux.Window // ordered list of windows
	ActiveWindowID uint32        // which window is displayed
	// PreviousWindowID tracks the last active window for `last-window`.
	PreviousWindowID   uint32
	Panes              []*mux.Pane // flat list of ALL panes across all windows
	logger             *charmlog.Logger
	launchColorProfile string
	eventLoopOwner     debugowner.Checker
	clientState        *clientManager
	paneLog            *PaneLog
	counter            atomic.Uint32 // pane ID counter
	windowCounter      atomic.Uint32 // window ID counter
	shutdown           atomic.Bool
	input              *inputRouter // cached active pane and paced input queues

	// Layout generation counter — incremented on every broadcastLayout.
	// Used by wait-layout to block until a layout change occurs.
	generation atomic.Uint64
	waiters    *waiterManager

	idle   *idleTracker
	vtIdle *VTIdleTracker

	// Event stream — used by `amux events` for push-based notifications.
	// Only accessed from the session event loop (no mutex needed).
	eventSubs []*eventSub
	// Latest emitted pane terminal metadata snapshot, used to suppress
	// duplicate terminal events.
	terminalEventState map[uint32]paneTerminalEventState

	undo *undoManager

	// Configurable timing — zero values use defaults. Tests inject short durations.
	VTIdleSettle    time.Duration // default: 2s
	UndoGracePeriod time.Duration // default: 30s
	Clock           Clock         // nil uses RealClock
	// PaneMetaResolver refreshes live cwd/git metadata for a pane. Nil uses
	// the pane's DetectCwdBranch implementation.
	PaneMetaResolver func(*mux.Pane) (cwd, branch string)
	// DisablePaneMetaAutoRefresh skips background cwd/git refresh on idle
	// transitions.
	DisablePaneMetaAutoRefresh bool

	// Internal capture timing overrides. Zero values use defaults.
	// Tests inject short timings here instead of mutating package globals.
	captureTiming captureTimingConfig

	// Remote pane management — nil when no remote transport is configured.
	RemoteManager   proto.PaneTransport
	remoteTakeover  PaneTakeoverTransport
	remoteHostColor func(string) string

	// SSH takeover tracking — pane IDs that have already been taken over.
	// Prevents duplicate takeover if the remote emits the sequence twice.
	// Only accessed from the session event loop.
	takenOverPanes map[uint32]bool

	// Session notice — server-owned transient message shown in the global bar.
	// Used for async failures such as SSH takeover attach errors.
	notice      string
	noticeToken uint64

	// Capture forwarding — routes capture requests through an attached
	// client so the result reflects client-side emulator state.
	// The event loop owns the single in-flight request and queued requests.
	capture *captureForwarder

	// Crash checkpoint — non-blocking trigger from broadcastLayout().
	// The crashCheckpointLoop goroutine debounces and writes periodically.
	crashCheckpointTrigger chan struct{}
	crashCheckpointStop    chan struct{}
	crashCheckpointDone    chan struct{} // closed when loop exits

	// Async session event loop — phase 1 serializes callback-driven writes.
	sessionEvents    chan sessionEvent
	sessionEventStop chan struct{}
	sessionEventDone chan struct{}

	// paneCloser runs the blocking Pane.Close path. Tests stub it to verify the
	// session event loop never waits on pane shutdown directly.
	paneCloser func(*mux.Pane)
	// localPaneBuilder builds PTY-backed local panes. Tests stub it to control
	// pane creation timing without mutable package-level state.
	localPaneBuilder func(localPaneBuildRequest) (*mux.Pane, error)
	localPaneBuilds  sync.WaitGroup

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

type processEnviron struct{}

func (processEnviron) Environ() []string {
	return os.Environ()
}

func (processEnviron) Getenv(key string) string {
	return os.Getenv(key)
}

type sessionLaunchEnviron struct {
	base termenv.Environ
}

func ignoredLaunchEnvKey(key string) bool {
	return key == "NO_COLOR" || key == "CODEX_CI"
}

func filterLaunchEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && ignoredLaunchEnvKey(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (e sessionLaunchEnviron) Environ() []string {
	if e.base == nil {
		return filterLaunchEnv(processEnviron{}.Environ())
	}
	return filterLaunchEnv(e.base.Environ())
}

func (e sessionLaunchEnviron) Getenv(key string) string {
	if ignoredLaunchEnvKey(key) {
		return ""
	}
	if e.base == nil {
		return processEnviron{}.Getenv(key)
	}
	return e.base.Getenv(key)
}

func sessionLaunchColorProfile(environ termenv.Environ) string {
	return termprofile.Format(termprofile.DetectFromEnvironment(sessionLaunchEnviron{base: environ}))
}

func defaultSessionLaunchColorProfile() string {
	return sessionLaunchColorProfile(processEnviron{})
}

func (s *Session) clock() Clock {
	if s.Clock != nil {
		return s.Clock
	}
	return RealClock{}
}

func (s *Session) vtIdleSettle() time.Duration {
	if s.VTIdleSettle != 0 {
		return s.VTIdleSettle
	}
	return config.VTIdleSettle
}

func (s *Session) detectPaneCwdBranch(pane *mux.Pane) (cwd, branch string) {
	if pane == nil {
		return "", ""
	}
	if s.PaneMetaResolver != nil {
		return s.PaneMetaResolver(pane)
	}
	return pane.DetectCwdBranch()
}

func (s *Session) undoGracePeriod() time.Duration {
	if s.UndoGracePeriod != 0 {
		return s.UndoGracePeriod
	}
	return config.UndoGracePeriod
}

type captureTimingConfig struct {
	attachMaxRetries int
	attachRetryDelay time.Duration
	responseTimeout  time.Duration
}

func (s *Session) captureAttachMaxRetries() int {
	if s.captureTiming.attachMaxRetries != 0 {
		return s.captureTiming.attachMaxRetries
	}
	return defaultCaptureAttachMaxRetries
}

func (s *Session) captureAttachRetryDelay() time.Duration {
	if s.captureTiming.attachRetryDelay != 0 {
		return s.captureTiming.attachRetryDelay
	}
	return defaultCaptureAttachRetryDelay
}

func (s *Session) captureResponseTimeout() time.Duration {
	if s.captureTiming.responseTimeout != 0 {
		return s.captureTiming.responseTimeout
	}
	return defaultCaptureResponseTimeout
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

			for _, w := range s.Windows {
				if cell := w.Root.FindPane(p.ID); cell != nil {
					ps.Cols = cell.W
					ps.Rows = mux.PaneContentHeight(cell.H)
					break
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
		s.logger.Warn("crash checkpoint dir unavailable",
			"event", "checkpoint_write",
			"checkpoint_kind", "crash",
			"error", err,
		)
	}
	s.crashCheckpointTrigger = make(chan struct{}, 1)
	s.crashCheckpointStop = make(chan struct{})
	s.crashCheckpointDone = make(chan struct{})
	go s.crashCheckpointLoop()
}

func (s *Session) stopCrashCheckpointLoop() {
	if s.crashCheckpointStop == nil || s.crashCheckpointDone == nil {
		return
	}
	select {
	case <-s.crashCheckpointDone:
		return
	default:
	}
	close(s.crashCheckpointStop)
	<-s.crashCheckpointDone
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

func (s *Session) writeCrashCheckpointNow() (string, error) {
	started := time.Now()
	cp := s.buildCrashCheckpoint()
	if cp == nil {
		return "", nil
	}
	path := checkpoint.CrashCheckpointPathTimestamped(s.Name, s.startedAt)
	if err := checkpoint.WriteCrash(cp, s.Name, s.startedAt); err != nil {
		s.logCheckpointWrite("crash", path, time.Since(started), err)
		return "", err
	}
	s.enqueueEvent(crashCheckpointWrittenEvent{path: path})
	s.logCheckpointWrite("crash", path, time.Since(started), nil)
	return path, nil
}

// writeCrashCheckpoint builds and writes a crash checkpoint to disk.
// Skips writing if the session is shutting down (clean shutdown removes the checkpoint).
func (s *Session) writeCrashCheckpoint() {
	if s.shutdown.Load() {
		return
	}
	_, _ = s.writeCrashCheckpointNow()
}

func (s *Session) hasClient(cc *clientConn) bool {
	return s.ensureClientManager().hasClient(cc)
}

func (s *Session) currentSizeClient() *clientConn {
	return s.ensureClientManager().currentSizeClient()
}

func (s *Session) noteClientActivity(cc *clientConn) bool {
	return s.ensureClientManager().noteClientActivity(cc)
}

func (s *Session) effectiveSizeClient() *clientConn {
	return s.ensureClientManager().effectiveSizeClient()
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
	remainingClients := s.ensureClientManager().removeClient(cc)
	if !broadcastLayout {
		return
	}
	shouldExit := s.exitServer != nil && s.exitServer.Env.ExitUnattached && s.hadClient && remainingClients == 0 && !s.shutdown.Load()
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
	pprof    *pprofEndpoint
	logger   *charmlog.Logger

	// Shutdown is serialized with atomics so concurrent callers all observe
	// one cleanup pass and later callers can wait for completion.
	shutdownState atomic.Uint32
	shutdownDone  chan struct{}

	// attachBootstrapHook is a test-only hook invoked after the initial
	// attach replay is sent but before bootstrap flushes queued messages.
	attachBootstrapHook func()

	// ResolveReloadExecPath resolves the executable path for server reload.
	// Defaults to reload.ResolveExecutable; tests inject stubs.
	ResolveReloadExecPath func() (string, error)

	// commands overrides the default commandRegistry for handler lookup.
	// When nil, handleCommand falls back to the package-level registry.
	commands map[string]CommandHandler
}

// lookupCommand returns the handler for the given command name, consulting the
// server-level override first, then the package-level commandRegistry.
func (s *Server) lookupCommand(name string) (CommandHandler, bool) {
	if s.commands != nil {
		h, ok := s.commands[name]
		return h, ok
	}
	h, ok := commandRegistry[name]
	return h, ok
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
	return proto.SocketDir()
}

// SocketPath returns the socket path for a session.
func SocketPath(session string) string {
	return proto.SocketPath(session)
}

func newSessionWithLogger(name string, scrollbackLines int, logger *charmlog.Logger) *Session {
	if logger == nil {
		logger = auditlog.Discard()
	}
	sess := &Session{
		Name:               name,
		startedAt:          time.Now(),
		scrollbackLines:    scrollbackLines,
		logger:             logger,
		launchColorProfile: defaultSessionLaunchColorProfile(),
		clientState:        newClientManager(),
		paneLog:            newPaneLog(defaultPaneLogSize),
	}
	sess.idle = newIdleTracker()
	sess.vtIdle = NewVTIdleTracker(sess.clock())
	sess.takenOverPanes = make(map[uint32]bool)
	sess.terminalEventState = make(map[uint32]paneTerminalEventState)
	sess.waiters = newWaiterManager()
	sess.capture = newCaptureForwarder()
	sess.input = newInputRouter()
	sess.undo = newUndoManager()
	sess.startCrashCheckpointLoop()
	sess.startEventLoop()
	return sess
}

func newSessionWithScrollback(name string, scrollbackLines int) *Session {
	return newSessionWithLogger(name, scrollbackLines, nil)
}

// NewServerWithScrollback creates a new server with an explicit retained
// scrollback limit for all panes in the session.
func newServerWithScrollbackLogger(sessionName string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	if logger == nil {
		logger = auditlog.Discard()
	}
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

	sess := newSessionWithLogger(sessionName, scrollbackLines, logger.With("session", sessionName))

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sessionName: sess},
		sockPath:     sockPath,
		logger:       logger,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = s

	return s, nil
}

func NewServerWithScrollback(sessionName string, scrollbackLines int) (*Server, error) {
	return NewServerWithScrollbackLogger(sessionName, scrollbackLines, nil)
}

func NewServerWithScrollbackLogger(sessionName string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	return newServerWithScrollbackLogger(sessionName, scrollbackLines, logger)
}

func newServerFromCrashCheckpointWithListenerLogger(sessionName string, listener net.Listener, sockPath string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	if logger == nil {
		logger = auditlog.Discard()
	}
	restoreStarted := time.Now()
	sess := newSessionWithLogger(sessionName, scrollbackLines, logger.With("session", sessionName))
	sess.startedAt = cp.Timestamp
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sessionName: sess},
		sockPath:     sockPath,
		logger:       logger,
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
			meta.Remote = string(proto.Reconnecting)
			pane = sess.ownPane(mux.NewProxyPaneWithScrollback(ps.ID, meta, ps.Cols, ps.Rows, sess.scrollbackLines,
				onOutput, onExit,
				sess.remoteWriteOverride(ps.ID),
			))
		} else {
			// Spawn fresh shell with restored cwd
			meta := ps.Meta
			meta.Dir = ps.Cwd // set cwd for the new shell
			var newErr error
			pane, newErr = mux.NewPaneWithScrollback(ps.ID, meta, ps.Cols, ps.Rows, sessionName, sess.scrollbackLines,
				onOutput, onExit,
			)
			if newErr != nil {
				sess.logger.Warn("crash recovery skipped pane",
					"event", "checkpoint_restore",
					"checkpoint_kind", "crash",
					"pane_id", ps.ID,
					"error", newErr,
				)
				continue
			}
			pane = sess.ownPane(pane)
			if sess.vtIdle != nil {
				sess.vtIdle.PrimeSettling(ps.ID, pane.CreatedAt())
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
		sess.PreviousWindowID = cp.Layout.PreviousWindowID
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
	if crashPath != "" {
		_ = checkpoint.RemoveCrashFile(crashPath)
	}
	sess.logCheckpointRestore("crash", crashPath, len(sess.Panes), len(sess.Windows), time.Since(restoreStarted))

	return s, nil
}

func newServerFromCrashCheckpointWithListener(sessionName string, listener net.Listener, sockPath string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int) (*Server, error) {
	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, sockPath, cp, crashPath, scrollbackLines, nil)
}

// NewServerFromCrashCheckpointWithScrollback restores a server from a crash
// checkpoint with an explicit retained scrollback limit.
func NewServerFromCrashCheckpointWithScrollback(sessionName string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int) (*Server, error) {
	return NewServerFromCrashCheckpointWithScrollbackLogger(sessionName, cp, crashPath, scrollbackLines, nil)
}

func NewServerFromCrashCheckpointWithScrollbackLogger(sessionName string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
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

	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, sockPath, cp, crashPath, scrollbackLines, logger)
}

// NewServerFromCrashCheckpointWithListenerFd restores crash state onto the
// listener inherited across a failed hot-reload restore.
func NewServerFromCrashCheckpointWithListenerFd(sessionName string, listenerFd int, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int) (*Server, error) {
	return NewServerFromCrashCheckpointWithListenerFdLogger(sessionName, listenerFd, cp, crashPath, scrollbackLines, nil)
}

func NewServerFromCrashCheckpointWithListenerFdLogger(sessionName string, listenerFd int, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	listener, err := restoreListenerFromFD(listenerFd)
	if err != nil {
		return nil, fmt.Errorf("restoring listener: %w", err)
	}
	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, SocketPath(sessionName), cp, crashPath, scrollbackLines, logger)
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
	if s.pprof != nil {
		s.pprof.close()
	}
	s.listener.Close()
	os.Remove(s.sockPath)

	for _, sess := range s.sessions {
		sess.shutdown.Store(true)

		sess.stopEventLoop()

		// Stop crash checkpoint loop and wait for it to exit.
		// The shutdown flag prevents any further writes.
		sess.stopCrashCheckpointLoop()

		// Clean shutdown: remove crash checkpoint (no recovery needed)
		_ = checkpoint.RemoveCrashFile(checkpoint.CrashCheckpointPathTimestamped(sess.Name, sess.startedAt))

		if sess.RemoteManager != nil {
			sess.RemoteManager.Shutdown()
		}
		panes := make([]*mux.Pane, len(sess.Panes))
		copy(panes, sess.Panes)
		var wg sync.WaitGroup
		for _, p := range panes {
			wg.Add(1)
			go func(p *mux.Pane) {
				defer wg.Done()
				_ = p.Close()
				_ = p.WaitClosed()
			}(p)
		}
		wg.Wait()
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

// handleAttach registers an attached client and starts its read loop.
func (s *Server) handleAttach(conn net.Conn, msg *Message) {
	sessionName := msg.Session
	if sessionName == "" {
		sessionName = DefaultSessionName
	}

	sess, ok := s.sessions[sessionName]

	if !ok {
		conn.Close()
		return
	}

	cc := newClientConn(conn)
	cc.ID = fmt.Sprintf("client-%d", sess.ensureClientManager().nextClientOrdinal())
	cc.logger = sess.logger.With("client_id", cc.ID)
	cc.nonInteractive = !msg.AttachMode.IsInteractive()
	cc.initTypeKeyQueue()
	cc.setNegotiatedCapabilities(proto.NegotiateClientCapabilities(msg.AttachCapabilities))
	cc.setColorProfile(msg.AttachColorProfile)
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
	if !res.layoutBroadcasted {
		// Broadcast layout to other clients so they see the new client.
		sess.broadcastLayout()
	}

	cc.readLoop(s, sess)
}

func (s *Server) handleOneShot(conn net.Conn, msg *Message) {
	cc := newClientConn(conn)
	defer func() {
		_ = cc.Flush()
		cc.Close()
	}()

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

	buildDone := (chan error)(nil)
	res := sess.enqueueCommandMutation(func(ctx *MutationContext) commandMutationResult {
		initRes, err := ctx.ensureInitialWindowLocked(s, cols, rows, nil)
		if err != nil {
			return commandMutationResult{err: err}
		}
		buildDone = initRes.buildDone
		if !initRes.layoutChanged {
			return commandMutationResult{}
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		return res.err
	}
	if buildDone != nil {
		if err := <-buildDone; err != nil {
			return err
		}
	}
	return nil
}
