package server

import (
	"fmt"
	"net"
	"os"
	"runtime/coverage"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// Reload checkpoints the server state and exec's the new binary.
// On success, this function never returns (the process image is replaced).
// On failure, the old server continues running.
func (s *Server) Reload(execPath string) error {
	sess := s.firstSession()

	if sess == nil {
		return fmt.Errorf("no session to reload")
	}

	clients, err := enqueueSessionQuery(sess, func(sess *Session) ([]*clientConn, error) {
		return sess.ensureClientManager().snapshotClients(), nil
	})
	if err != nil {
		return err
	}
	// Stop PTY read broadcasts
	sess.shutdown.Store(true)

	// Build checkpoint
	cp, err := enqueueSessionQuery(sess, func(sess *Session) (*checkpoint.ServerCheckpoint, error) {
		if len(sess.Windows) == 0 {
			return nil, fmt.Errorf("no window to checkpoint")
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
		}

		for _, p := range sess.Panes {
			history, screen, _ := p.HistoryScreenSnapshot()
			pc := checkpoint.PaneCheckpoint{
				ID:           p.ID,
				Meta:         p.Meta,
				ManualBranch: p.MetaManualBranch(),
				History:      history,
				Screen:       screen,
				CreatedAt:    p.CreatedAt(),
				IsProxy:      p.IsProxy(),
			}
			if p.IsProxy() {
				pc.PtmxFd = -1
				pc.PID = 0
			} else {
				pc.PtmxFd = p.PtmxFd()
				pc.PID = p.ProcessPid()
			}
			for _, w := range sess.Windows {
				if cell := w.Root.FindPane(p.ID); cell != nil {
					pc.Cols = cell.W
					pc.Rows = mux.PaneContentHeight(cell.H)
					break
				}
			}
			cp.Panes = append(cp.Panes, pc)
		}

		return cp, nil
	})
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

	// Clear FD_CLOEXEC on inherited FDs (skip proxy panes — they have no PTY)
	clearCloexec(uintptr(cp.ListenerFd))
	for _, pc := range cp.Panes {
		if !pc.IsProxy && pc.PtmxFd >= 0 {
			clearCloexec(uintptr(pc.PtmxFd))
		}
	}

	// Deliver the reload notice only after checkpointing succeeds so a failed
	// checkpoint doesn't disrupt attached clients.
	for _, c := range clients {
		c.sendBroadcastSync(&Message{Type: MsgTypeServerReload})
	}
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
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
	return fmt.Errorf("server exec: %w", execErr)
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
	// Reconstruct listener from inherited FD
	listener, err := restoreListenerFromFD(cp.ListenerFd)
	if err != nil {
		return nil, fmt.Errorf("restoring listener: %w", err)
	}

	sess := newSessionWithScrollback(cp.SessionName, scrollbackLines)
	if !cp.StartedAt.IsZero() {
		sess.startedAt = cp.StartedAt
	}
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)

	s := &Server{
		listener:     listener,
		sessions:     map[string]*Session{cp.SessionName: sess},
		sockPath:     SocketPath(cp.SessionName),
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
			// Restore proxy pane with frozen content, mark as reconnecting.
			// The remote manager will re-establish the SSH connection.
			meta := pc.Meta
			meta.Remote = string(proto.Reconnecting)
			pane = sess.ownPane(mux.NewProxyPaneWithScrollback(pc.ID, meta, pc.Cols, pc.Rows, sess.scrollbackLines,
				onOutput, onExit,
				sess.remoteWriteOverride(pc.ID),
			))
		} else {
			var restoreErr error
			pane, restoreErr = mux.RestorePaneWithScrollback(pc.ID, pc.Meta, pc.PtmxFd, pc.PID, pc.Cols, pc.Rows, sess.scrollbackLines,
				onOutput, onExit,
			)
			if restoreErr != nil {
				continue // Skip pane on restore failure
			}
			pane = sess.ownPane(pane)
		}

		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnTakeover(sess.takeoverCallback(s))
		pane.SetOnMetaUpdate(sess.metaCallback())
		restorePaneRuntimeState(pane, pc.ManualBranch)

		if !pc.CreatedAt.IsZero() {
			pane.SetCreatedAt(pc.CreatedAt)
		}
		pane.SetRetainedHistory(pc.History)
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
			targets, err := enqueueSessionQuery(sess, func(sess *Session) ([]resizeTarget, error) {
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
				target.pane.Resize(target.cols, target.rows)
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
