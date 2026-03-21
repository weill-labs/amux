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
	"github.com/weill-labs/amux/internal/remote"
)

// Reload checkpoints the server state and exec's the new binary.
// On success, this function never returns (the process image is replaced).
// On failure, the old server continues running.
func (s *Server) Reload(execPath string) error {
	sess := s.firstSession()

	if sess == nil {
		return fmt.Errorf("no session to reload")
	}

	// Broadcast reload notice to clients
	sess.broadcast(&Message{Type: MsgTypeServerReload})

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
			SessionName:   sess.Name,
			Counter:       sess.counter.Load(),
			WindowCounter: sess.windowCounter.Load(),
			Generation:    sess.generation.Load(),
			Layout:        *snap,
		}

		for _, p := range sess.Panes {
			history, screen, _ := p.HistoryScreenSnapshot()
			pc := checkpoint.PaneCheckpoint{
				ID:        p.ID,
				Meta:      p.Meta,
				History:   history,
				Screen:    screen,
				CreatedAt: p.CreatedAt(),
				IsProxy:   p.IsProxy(),
			}
			if p.IsProxy() {
				pc.PtmxFd = -1
				pc.PID = 0
			} else {
				pc.PtmxFd = p.PtmxFd()
				pc.PID = p.ProcessPid()
			}
			if p.Meta.Minimized {
				pc.Cols, pc.Rows = p.EmulatorSize()
			} else {
				for _, w := range sess.Windows {
					if cell := w.Root.FindPane(p.ID); cell != nil {
						pc.Cols = cell.W
						pc.Rows = mux.PaneContentHeight(cell.H)
						break
					}
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

// NewServerFromCheckpointWithScrollback restores a server from a checkpoint
// using an explicit retained scrollback limit for restored panes.
func NewServerFromCheckpointWithScrollback(cp *checkpoint.ServerCheckpoint, scrollbackLines int) (*Server, error) {
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

	sess := newSessionWithScrollback(cp.SessionName, scrollbackLines)
	sess.counter.Store(cp.Counter)
	sess.windowCounter.Store(cp.WindowCounter)
	sess.generation.Store(cp.Generation)

	s := &Server{
		listener: listener,
		sessions: map[string]*Session{cp.SessionName: sess},
		sockPath: SocketPath(cp.SessionName),
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
			meta.Remote = string(remote.Reconnecting)
			pane = mux.NewProxyPaneWithScrollback(pc.ID, meta, pc.Cols, pc.Rows, sess.scrollbackLines,
				onOutput, onExit,
				func(data []byte) (int, error) {
					// writeOverride will be reconnected by the remote manager
					if sess.RemoteManager != nil {
						return len(data), sess.RemoteManager.SendInput(pc.ID, data)
					}
					return len(data), nil // drop input until reconnected
				},
			)
		} else {
			var restoreErr error
			pane, restoreErr = mux.RestorePaneWithScrollback(pc.ID, pc.Meta, pc.PtmxFd, pc.PID, pc.Cols, pc.Rows, sess.scrollbackLines,
				onOutput, onExit,
			)
			if restoreErr != nil {
				continue // Skip pane on restore failure
			}
		}

		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnTakeover(sess.takeoverCallback(s))

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

	// Start PTY read loops for all restored panes (skip proxy panes)
	for _, p := range sess.Panes {
		if !p.IsProxy() {
			p.Start()
		}
	}

	// Save screen data for minimized panes so we can re-replay after the
	// SIGWINCH loop. Between Start() and the SIGWINCH delay, the readLoop
	// may consume buffered PTY output (e.g. a shell prompt produced during
	// the exec gap) that overwrites the replayed emulator content. Visible
	// panes recover via the SIGWINCH-triggered redraw; minimized panes need
	// an explicit re-replay.
	minimizedScreens := make(map[uint32]string)
	for _, pc := range cp.Panes {
		if pc.Meta.Minimized {
			minimizedScreens[pc.ID] = pc.Screen
		}
	}

	// Force TUI apps to do a full screen redraw via SIGWINCH.
	// Skip minimized panes and proxy panes (no PTY to SIGWINCH).
	go func() {
		type resizeTarget struct {
			pane *mux.Pane
			cols int
			rows int
		}
		type replayTarget struct {
			pane *mux.Pane
			data string
		}

		resizeVisible := func(heightAdj int) bool {
			targets, err := enqueueSessionQuery(sess, func(sess *Session) ([]resizeTarget, error) {
				var targets []resizeTarget
				for _, w := range sess.Windows {
					for _, p := range sess.Panes {
						if p.Meta.Minimized || p.IsProxy() {
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

		// Re-replay saved screen data for minimized panes. The readLoop
		// may have fed buffered PTY output into their emulators, garbling
		// the content that was replayed during restore. Clear the screen
		// first so the replay starts from a known state.
		// Also broadcast the replay to clients so their emulators stay
		// in sync with the server.
		replays, err := enqueueSessionQuery(sess, func(sess *Session) ([]replayTarget, error) {
			var replays []replayTarget
			for _, p := range sess.Panes {
				if screen, ok := minimizedScreens[p.ID]; ok {
					replays = append(replays, replayTarget{
						pane: p,
						data: "\033[H\033[2J" + screen,
					})
				}
			}
			return replays, nil
		})
		if err != nil {
			return
		}
		for _, replay := range replays {
			replay.pane.ReplayScreen(replay.data)
			sess.broadcastPaneOutput(replay.pane.ID, []byte(replay.data), 0)
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
