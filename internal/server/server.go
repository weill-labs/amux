package server

import (
	"context"
	"errors"
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

	idle *IdleTracker

	// Event stream — used by `amux events` for push-based notifications.
	// Only accessed from the session event loop (no mutex needed).
	eventSubs []*eventSub
	// Latest emitted pane terminal metadata snapshot, used to suppress
	// duplicate terminal events.
	terminalEventState map[uint32]paneTerminalEventState

	undo *UndoManager

	// Configurable timing — zero values use defaults. Tests inject short durations.
	VTIdleSettle time.Duration // default: 2s
	Clock        Clock         // nil uses RealClock
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
	remoteSessions  map[string]*RemoteSession

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

	// Crash checkpoint coordination owns debounce/periodic scheduling and disk writes.
	checkpointCoordinator crashCheckpointCoordinator

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

func (s *Session) ensureIdleTracker() *IdleTracker {
	if s.idle == nil {
		s.idle = NewIdleTracker(s.clock)
	}
	return s.idle
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

		paneSnapshots := snapshotPaneHistoryScreens(s.Panes, (*mux.Pane).HistoryScreenSnapshot)
		cp.PaneStates = make([]checkpoint.CrashPaneState, len(s.Panes))
		var cwdWork []pidEntry
		for i, p := range s.Panes {
			snapshot := paneSnapshots[i]
			ps := checkpoint.CrashPaneState{
				ID:           p.ID,
				Meta:         p.Meta,
				ManualBranch: p.MetaManualBranch(),
				History:      snapshot.history,
				Screen:       snapshot.screen,
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
				cwdWork = append(cwdWork, pidEntry{index: i, pane: p, pid: p.ProcessPid()})
			}

			cp.PaneStates[i] = ps
		}

		return crashSnapshot{cp: cp, cwdWork: cwdWork}, nil
	})
	if err != nil || snap.cp == nil {
		return nil
	}

	// Resolve cwds and agent status outside the lock concurrently —
	// both PaneCwd (lsof) and AgentStatus (pgrep/ps) fork subprocesses.
	type cwdResult struct {
		index   int
		cwd     string
		wasIdle bool
		command string
	}
	ch := make(chan cwdResult, len(snap.cwdWork))
	for _, w := range snap.cwdWork {
		go func(w pidEntry) {
			jobState := w.pane.ForegroundJobState()
			status := w.pane.AgentStatus()
			ch <- cwdResult{
				index:   w.index,
				cwd:     mux.PaneCwd(w.pid),
				wasIdle: jobState.Idle,
				command: status.CurrentCommand,
			}
		}(w)
	}
	for range snap.cwdWork {
		r := <-ch
		snap.cp.PaneStates[r.index].Cwd = r.cwd
		snap.cp.PaneStates[r.index].WasIdle = r.wasIdle
		snap.cp.PaneStates[r.index].Command = r.command
	}

	return snap.cp
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
	Env         ServerEnv
	listener    net.Listener
	sessions    map[string]*Session
	sockPath    string
	sessionLock *os.File
	pprof       *pprofEndpoint
	logger      *charmlog.Logger

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

	// shutdownCheckpointTimeout bounds the final crash checkpoint write during
	// shutdown. Zero uses the default timeout.
	shutdownCheckpointTimeout time.Duration
	// shutdownPaneCloseTimeout bounds each pane close wait during shutdown.
	// Zero uses the default timeout.
	shutdownPaneCloseTimeout time.Duration
}

const defaultShutdownCheckpointTimeout = 2 * time.Second
const defaultShutdownPaneCloseTimeout = 3 * time.Second

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

func listenForSession(sessionName string) (net.Listener, string, *os.File, error) {
	sockPath := SocketPath(sessionName)
	sessionLock, err := acquireSessionLock(sessionName)
	if err != nil {
		return nil, "", nil, err
	}

	_ = os.Remove(sockPath)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		closeSessionLock(sessionLock)
		return nil, "", nil, fmt.Errorf("listening: %w", err)
	}
	if err := os.Chmod(sockPath, 0700); err != nil {
		listener.Close()
		closeSessionLock(sessionLock)
		return nil, "", nil, fmt.Errorf("chmod socket: %w", err)
	}
	return listener, sockPath, sessionLock, nil
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
	sess.idle = NewIdleTracker(sess.clock)
	sess.takenOverPanes = make(map[uint32]bool)
	sess.remoteSessions = make(map[string]*RemoteSession)
	sess.terminalEventState = make(map[uint32]paneTerminalEventState)
	sess.waiters = newWaiterManager()
	sess.capture = newCaptureForwarder()
	sess.input = newInputRouter()
	sess.checkpointCoordinator = newSessionCheckpointCoordinator(sess)
	sess.undo = newUndoManager(undoManagerConfig{})
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

	listener, sockPath, sessionLock, err := listenForSession(sessionName)
	if err != nil {
		return nil, err
	}

	sess := newSessionWithLogger(sessionName, scrollbackLines, logger.With("session", sessionName))

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sessionName: sess},
		sockPath:     sockPath,
		sessionLock:  sessionLock,
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

func newServerFromCrashCheckpointWithListenerLogger(sessionName string, listener net.Listener, sockPath string, sessionLock *os.File, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
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
		sessionLock:  sessionLock,
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
			sess.ensureIdleTracker().PrimeSettling(ps.ID, pane.CreatedAt())
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
		closeSessionLock(sessionLock)
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
	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, sockPath, nil, cp, crashPath, scrollbackLines, nil)
}

// NewServerFromCrashCheckpointWithScrollback restores a server from a crash
// checkpoint with an explicit retained scrollback limit.
func NewServerFromCrashCheckpointWithScrollback(sessionName string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int) (*Server, error) {
	return NewServerFromCrashCheckpointWithScrollbackLogger(sessionName, cp, crashPath, scrollbackLines, nil)
}

func NewServerFromCrashCheckpointWithScrollbackLogger(sessionName string, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	listener, sockPath, sessionLock, err := listenForSession(sessionName)
	if err != nil {
		return nil, err
	}

	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, sockPath, sessionLock, cp, crashPath, scrollbackLines, logger)
}

func NewServerFromCrashCheckpointWithListenerFdLogger(sessionName string, listenerFd int, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	return NewServerFromCrashCheckpointWithListenerAndLockFdLogger(sessionName, listenerFd, 0, cp, crashPath, scrollbackLines, logger)
}

func NewServerFromCrashCheckpointWithListenerAndLockFdLogger(sessionName string, listenerFd, sessionLockFd int, cp *checkpoint.CrashCheckpoint, crashPath string, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	listener, err := restoreListenerFromFD(listenerFd)
	if err != nil {
		return nil, fmt.Errorf("restoring listener: %w", err)
	}
	sessionLock, err := restoreOrAcquireSessionLock(sessionName, sessionLockFd)
	if err != nil {
		listener.Close()
		return nil, err
	}
	return newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, SocketPath(sessionName), sessionLock, cp, crashPath, scrollbackLines, logger)
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
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.sockPath)

	for _, sess := range s.sessions {
		sess.shutdown.Store(true)
		timeout := s.shutdownCrashCheckpointTimeout()

		// Persist one final snapshot before the checkpoint coordinator stops so
		// the next start can restore the latest clean-shutdown state.
		checkpointDone := make(chan struct{})
		go func() {
			_, _ = sess.writeCrashCheckpointNow()
			close(checkpointDone)
		}()
		select {
		case <-checkpointDone:
		case <-time.After(timeout):
			if s.logger != nil {
				s.logger.Warn("timed out waiting for crash checkpoint during shutdown",
					"session", sess.Name,
					"timeout", timeout)
			}
		}

		sess.stopEventLoop()

		// Stop crash checkpoint loop and wait for it to exit.
		// The shutdown flag prevents any further writes.
		sess.stopCrashCheckpointLoop()

		if sess.RemoteManager != nil {
			sess.RemoteManager.Shutdown()
		}
		panes := make([]*mux.Pane, len(sess.Panes))
		copy(panes, sess.Panes)
		paneCloseTimeout := s.shutdownPaneCloseWaitTimeout()
		var wg sync.WaitGroup
		for _, p := range panes {
			wg.Add(1)
			go func(p *mux.Pane) {
				defer wg.Done()
				s.closePaneDuringShutdown(p, paneCloseTimeout)
			}(p)
		}
		wg.Wait()
	}
	closeSessionLock(s.sessionLock)
}

func (s *Server) shutdownCrashCheckpointTimeout() time.Duration {
	if s == nil || s.shutdownCheckpointTimeout <= 0 {
		return defaultShutdownCheckpointTimeout
	}
	return s.shutdownCheckpointTimeout
}

func (s *Server) shutdownPaneCloseWaitTimeout() time.Duration {
	if s == nil || s.shutdownPaneCloseTimeout <= 0 {
		return defaultShutdownPaneCloseTimeout
	}
	return s.shutdownPaneCloseTimeout
}

func (s *Server) closePaneDuringShutdown(p *mux.Pane, timeout time.Duration) {
	_ = p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := p.WaitClosed(ctx); errors.Is(err, context.DeadlineExceeded) {
		s.logPaneShutdownCloseTimeout(p, timeout, err)
	}
}

func (s *Server) logPaneShutdownCloseTimeout(p *mux.Pane, timeout time.Duration, err error) {
	if s == nil || s.logger == nil {
		return
	}
	fields := append([]any{"event", "pane_shutdown_close_timeout"}, paneAuditFields(p)...)
	fields = append(fields, "timeout", timeout, "error", err)
	s.logger.Warn("timed out waiting for pane close during shutdown", fields...)
}

func (s *Server) handleConn(conn net.Conn) {
	cc := newClientConn(conn)
	msg, err := cc.reader.ReadMsg()
	if err != nil {
		cc.Close()
		return
	}

	switch msg.Type {
	case MsgTypeAttach:
		s.handleAttach(cc, msg)
	case MsgTypeCommand:
		s.handleOneShot(cc, msg)
	default:
		cc.Close()
	}
}

// handleAttach registers an attached client and starts its read loop.
func (s *Server) handleAttach(cc *clientConn, msg *Message) {
	sessionName := msg.Session
	if sessionName == "" {
		sessionName = DefaultSessionName
	}

	sess, ok := s.sessions[sessionName]

	if !ok {
		cc.Close()
		return
	}

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
		cc.Close()
		return
	}

	if err := cc.Send(&Message{Type: MsgTypeLayout, Layout: res.snap}); err != nil {
		cc.Close()
		return
	}
	bootstrapSeqs := make(map[uint32]uint64, len(res.paneSnapshots))
	for _, ps := range res.paneSnapshots {
		if len(ps.styledHistory) > 0 {
			messages, err := chunkPaneHistoryMessages(ps.paneID, ps.styledHistory, paneHistoryChunkThreshold, cc.capabilities.BinaryPaneHistory)
			if err != nil {
				cc.Close()
				return
			}
			for _, historyMsg := range messages {
				if err := cc.Send(historyMsg); err != nil {
					cc.Close()
					return
				}
			}
		}
		if err := cc.Send(&Message{Type: MsgTypePaneOutput, PaneID: ps.paneID, PaneData: ps.screen}); err != nil {
			cc.Close()
			return
		}
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

func (s *Server) handleOneShot(cc *clientConn, msg *Message) {
	defer func() {
		_ = cc.Flush()
		cc.Close()
	}()

	sess := s.firstSession()

	if sess == nil {
		_ = cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
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
