package server

import (
	"fmt"
	"net"
	"os"
	"runtime/coverage"
	"syscall"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
)

// Reload checkpoints the server state and exec's the new binary.
// On success, this function never returns (the process image is replaced).
// On failure, the old server continues running.
func (s *Server) Reload(execPath string) error {
	sess := s.firstSession()

	if sess == nil {
		return fmt.Errorf("no session to reload")
	}
	if s.logger == nil {
		s.logger = auditlog.Discard()
	}
	if sess.logger == nil {
		sess.logger = s.logger.With("session", sess.Name)
	}
	reloadStarted := time.Now()
	sess.logger.Info("hot reload requested",
		"event", "hot_reload",
		"exec_path", execPath,
	)

	clients, err := enqueueSessionQueryOnState(sess.context(), sess, func(sess *Session) ([]*clientConn, error) {
		return sess.ensureClientManager().snapshotClients(), nil
	})
	if err != nil {
		return err
	}
	// Stop PTY read broadcasts
	sess.shutdown.Store(true)

	// Build checkpoint
	cp, err := sess.buildReloadCheckpoint()
	if err != nil {
		sess.shutdown.Store(false)
		return err
	}

	// Get listener FD
	lnFd, err := listenerFd(s.listener)
	if err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("getting listener FD: %w", err)
	}
	cp.ListenerFd = lnFd
	if s.sessionLock != nil {
		cp.SessionLockFd = int(s.sessionLock.Fd())
	}

	// Do not exec without a durable crash checkpoint. If the new binary rejects
	// the reload checkpoint after a version bump, crash recovery is the only
	// remaining path that preserves panes across the exec boundary.
	if _, err := sess.writeCrashCheckpointNow(); err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("writing crash checkpoint: %w", err)
	}

	// Write checkpoint to temp file
	cpPath, err := checkpoint.Write(cp)
	if err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("writing checkpoint: %w", err)
	}
	sess.logCheckpointWrite("reload", cpPath, time.Since(reloadStarted), nil)

	// Clear FD_CLOEXEC on inherited FDs (skip proxy panes — they have no PTY)
	if err := clearCloexec(uintptr(cp.ListenerFd)); err != nil {
		sess.shutdown.Store(false)
		return fmt.Errorf("clearing close-on-exec on listener: %w", err)
	}
	if cp.SessionLockFd > 0 {
		if err := clearCloexec(uintptr(cp.SessionLockFd)); err != nil {
			sess.shutdown.Store(false)
			return fmt.Errorf("clearing close-on-exec on session lock: %w", err)
		}
	}
	for _, pc := range cp.Panes {
		if !pc.IsProxy && pc.PtmxFd >= 0 {
			if err := clearCloexec(uintptr(pc.PtmxFd)); err != nil {
				sess.shutdown.Store(false)
				return fmt.Errorf("clearing close-on-exec on pane %d PTY: %w", pc.ID, err)
			}
		}
	}

	// Deliver the reload notice only after checkpointing succeeds so a failed
	// checkpoint doesn't disrupt attached clients.
	for _, c := range clients {
		c.sendBroadcastSync(&Message{Type: MsgTypeServerReload, Text: BuildVersion})
	}
	if _, err := enqueueSessionQueryOnState(sess.context(), sess, func(sess *Session) (struct{}, error) {
		sess.disconnectClientsForReload(clients)
		return struct{}{}, nil
	}); err != nil {
		sess.shutdown.Store(false)
		os.Remove(cpPath)
		return err
	}

	// Flush coverage data before exec (which replaces the process image
	// without running atexit handlers). No-op if not built with -cover.
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		_ = coverage.WriteCountersDir(dir)
	}

	// Replace process image with new binary.
	// Re-export server-only env vars (unsetenv'd at startup to prevent
	// child shells from inheriting them, but must survive exec).
	env := append(os.Environ(), "AMUX_CHECKPOINT="+cpPath)
	env = append(env, s.Env.Export()...)
	execErr := syscall.Exec(execPath, os.Args, env)

	// If we get here, the exec call failed — undo changes
	sess.shutdown.Store(false)
	os.Remove(cpPath)
	sess.logger.Error("hot reload failed",
		"event", "hot_reload",
		"exec_path", execPath,
		"duration", durationField(time.Since(reloadStarted)),
		"error", execErr,
	)
	return fmt.Errorf("server exec: %w", execErr)
}

func (s *Session) buildReloadCheckpoint() (*checkpoint.ServerCheckpoint, error) {
	return s.buildReloadCheckpointWithSnapshot((*mux.Pane).HistoryScreenSnapshot)
}

func (s *Session) buildReloadCheckpointWithSnapshot(snapshot paneHistoryScreenSnapshotFunc) (*checkpoint.ServerCheckpoint, error) {
	work, err := enqueueSessionQueryOnState(s.context(), s, func(sess *Session) (reloadCheckpointWork, error) {
		if len(sess.Windows) == 0 {
			return reloadCheckpointWork{}, fmt.Errorf("no window to checkpoint")
		}

		idleSnap := sess.snapshotIdleState()
		snap := sess.snapshotLayout(idleSnap)
		cp := &checkpoint.ServerCheckpoint{
			Version:       checkpoint.ServerCheckpointVersion,
			SessionName:   sess.Name,
			StartedAt:     sess.startedAt,
			Counter:       sess.counter.Load(),
			WindowCounter: sess.windowCounter.Load(),
			Generation:    sess.generation.Load(),
			Layout:        *snap,
			MailboxSeq:    sess.mailboxEventSeq,
		}

		return reloadCheckpointWork{
			cp:      cp,
			panes:   sess.collectCheckpointPaneWork(),
			mailbox: mailboxCheckpointSnapshotSource(sess.mailbox),
		}, nil
	})
	if err != nil {
		return nil, err
	}

	work.cp.Mailbox = materializeMailboxCheckpointSnapshot(work.mailbox)
	paneSnapshots := snapshotPaneHistoryScreens(checkpointPaneRefs(work.panes), snapshot)
	work.cp.Panes = make([]checkpoint.PaneCheckpoint, len(work.panes))
	for i, pane := range work.panes {
		snapshot := paneSnapshots[i]
		pc := checkpoint.PaneCheckpoint{
			ID:           pane.id,
			Meta:         pane.meta,
			ManualBranch: pane.manualBranch,
			PtmxFd:       pane.ptmxFd,
			PID:          pane.pid,
			Cols:         pane.cols,
			Rows:         pane.rows,
			History:      snapshot.history,
			Screen:       snapshot.screen,
			CreatedAt:    pane.createdAt,
			IsProxy:      pane.isProxy,
			RemoteRef:    pane.remoteRef,
		}
		work.cp.Panes[i] = pc
	}

	return work.cp, nil
}

func mailboxCheckpointSnapshot(store *mailbox.Store) *mailbox.Snapshot {
	return materializeMailboxCheckpointSnapshot(mailboxCheckpointSnapshotSource(store))
}

func (s *Session) restoreMailbox(snapshot *mailbox.Snapshot, eventSeq uint64) error {
	if s == nil || snapshot == nil {
		return nil
	}
	store, err := mailbox.RestoreSnapshot(*snapshot, mailbox.Options{Now: func() time.Time { return s.clock().Now() }})
	if err != nil {
		return err
	}
	s.mailbox = store
	s.mailboxEventSeq = eventSeq
	if maxSeq := store.MaxLastEventSeq(); maxSeq > s.mailboxEventSeq {
		s.mailboxEventSeq = maxSeq
	}
	return nil
}

func restorePaneRuntimeState(pane *mux.Pane, manualBranch bool) {
	pane.SetMetaManualBranch(manualBranch)
}

func restoreListenerFromFD(listenerFD int) (net.Listener, error) {
	listenerFile := os.NewFile(uintptr(listenerFD), "listener")
	if listenerFile == nil {
		return nil, fmt.Errorf("invalid listener FD %d", listenerFD)
	}
	listener, err := net.FileListener(listenerFile)
	listenerFile.Close() // FileListener dups the FD
	if err != nil {
		return nil, err
	}
	return listener, nil
}

// NewServerFromCheckpointWithScrollback restores a server from a checkpoint
// using an explicit retained scrollback limit for restored panes.
func NewServerFromCheckpointWithScrollback(cp *checkpoint.ServerCheckpoint, scrollbackLines int) (*Server, error) {
	return NewServerFromCheckpointWithScrollbackLogger(cp, scrollbackLines, nil)
}

func NewServerFromCheckpointWithScrollbackLogger(cp *checkpoint.ServerCheckpoint, scrollbackLines int, logger *charmlog.Logger) (*Server, error) {
	return NewServerFromCheckpointWithScrollbackConfigLogger(cp, NewScrollbackConfig(scrollbackLines, nil), logger)
}

func NewServerFromCheckpointWithScrollbackConfigLogger(cp *checkpoint.ServerCheckpoint, scrollback ScrollbackConfig, logger *charmlog.Logger) (*Server, error) {
	if logger == nil {
		logger = auditlog.Discard()
	}
	restoreStarted := time.Now()
	// Reconstruct listener from inherited FD
	listener, err := restoreListenerFromFD(cp.ListenerFd)
	if err != nil {
		return nil, fmt.Errorf("restoring listener: %w", err)
	}
	sessionLock, err := restoreOrAcquireSessionLock(cp.SessionName, cp.SessionLockFd)
	if err != nil {
		listener.Close()
		return nil, err
	}

	sess := newSessionWithScrollbackConfigLogger(cp.SessionName, scrollback, logger.With("session", cp.SessionName))
	if !cp.StartedAt.IsZero() {
		sess.startedAt = cp.StartedAt
	}
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)
	if err := sess.restoreMailbox(cp.Mailbox, cp.MailboxSeq); err != nil {
		listener.Close()
		closeSessionLock(sessionLock)
		return nil, fmt.Errorf("restoring mailbox: %w", err)
	}

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{cp.SessionName: sess},
		sockPath:     SocketPath(cp.SessionName),
		sessionLock:  sessionLock,
		logger:       logger,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = s

	// Restore panes
	paneMap := make(map[uint32]*mux.Pane, len(cp.Panes))
	for _, pc := range cp.Panes {
		var pane *mux.Pane

		onOutput := sess.paneOutputCallback()
		onExit := sess.paneExitCallback()

		if pc.IsProxy {
			writeOverride := func(data []byte) (int, error) { return len(data), nil }
			if pc.RemoteRef != nil {
				writeOverride = sess.mirrorWriteOverride(pc.ID)
			}
			pane = sess.ownPane(mux.NewProxyPaneWithScrollback(pc.ID, pc.Meta, pc.Cols, pc.Rows, sess.scrollbackLinesForHost(pc.Meta.Host),
				onOutput, onExit, writeOverride,
			))
		} else {
			var restoreErr error
			pane, restoreErr = mux.RestorePaneWithScrollback(pc.ID, pc.Meta, pc.PtmxFd, pc.PID, pc.Cols, pc.Rows, sess.scrollbackLinesForHost(pc.Meta.Host),
				onOutput, onExit,
			)
			if restoreErr != nil {
				continue // Skip pane on restore failure
			}
			pane = sess.ownPane(pane)
		}

		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())
		restorePaneRuntimeState(pane, pc.ManualBranch)

		if !pc.CreatedAt.IsZero() {
			pane.SetCreatedAt(pc.CreatedAt)
		}
		pane.SetRetainedHistory(pc.History)
		pane.ReplayScreen(pc.Screen)
		paneMap[pc.ID] = pane
		sess.Panes = append(sess.Panes, pane)
		if pc.RemoteRef != nil {
			if err := sess.trackMirrorPane(pane, *pc.RemoteRef); err != nil {
				sess.logger.Warn("restored mirror tracking failed",
					"event", "checkpoint_restore",
					"checkpoint_kind", "reload",
					"pane_id", pane.ID,
					"pane_name", pane.Meta.Name,
					"error", err,
				)
			}
		}
	}

	if len(sess.Panes) == 0 {
		listener.Close()
		closeSessionLock(sessionLock)
		return nil, fmt.Errorf("no panes restored from checkpoint")
	}

	// Rebuild windows from multi-window snapshot or legacy single-window
	if len(cp.Layout.Windows) > 0 {
		for _, ws := range cp.Layout.Windows {
			w := mux.RebuildWindowFromSnapshot(ws, cp.Layout.Width, cp.Layout.Height, paneMap)
			sess.Windows = append(sess.Windows, w)
		}
		sess.ActiveWindowID = cp.Layout.ActiveWindowID
		sess.PreviousWindowID = cp.Layout.PreviousWindowID
	} else {
		// Legacy single-window checkpoint
		w := mux.RebuildFromSnapshot(cp.Layout, paneMap)
		winID := sess.windowCounter.Add(1)
		w.ID = winID
		w.Name = fmt.Sprintf(WindowNameFormat, winID)
		sess.Windows = append(sess.Windows, w)
		sess.ActiveWindowID = winID
	}
	sess.refreshInputTarget()

	// Start PTY read loops for all restored panes (skip proxy panes)
	for _, p := range sess.Panes {
		if !p.IsProxy() {
			p.Start()
		}
	}

	// Force TUI apps to do a full screen redraw via SIGWINCH.
	// Skip proxy panes (no PTY to SIGWINCH).
	go func() {
		type resizeTarget struct {
			pane *mux.Pane
			cols int
			rows int
		}

		resizeVisible := func(heightAdj int) bool {
			targets, err := enqueueSessionQueryOnState(sess.context(), sess, func(sess *Session) ([]resizeTarget, error) {
				var targets []resizeTarget
				for _, w := range sess.Windows {
					for _, p := range sess.Panes {
						if p.IsProxy() {
							continue
						}
						if cell := w.Root.FindPane(p.ID); cell != nil {
							targets = append(targets, resizeTarget{
								pane: p,
								cols: cell.W,
								rows: mux.PaneContentHeight(cell.H) + heightAdj,
							})
						}
					}
				}
				return targets, nil
			})
			if err != nil {
				return false
			}
			for _, target := range targets {
				if err := target.pane.Resize(target.cols, target.rows); err != nil {
					return false
				}
			}
			return true
		}

		time.Sleep(500 * time.Millisecond)
		if !resizeVisible(-1) {
			return
		}
		time.Sleep(200 * time.Millisecond)
		if !resizeVisible(0) {
			return
		}
	}()

	sess.logCheckpointRestore("reload", "", len(sess.Panes), len(sess.Windows), time.Since(restoreStarted))

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
func clearCloexec(fd uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
