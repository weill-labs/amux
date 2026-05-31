package server

import (
	"errors"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
)

const (
	maxWatchdogRecoveryAttempts = 3
	watchdogRecoveryWindow      = 10 * time.Minute

	watchdogRecoveryAttemptsEnv = "AMUX_WATCHDOG_RECOVERY_ATTEMPTS"
	watchdogRecoveryFirstEnv    = "AMUX_WATCHDOG_RECOVERY_FIRST_UNIX"
)

var errNoWatchdogRecoveryWindow = errors.New("no window to checkpoint")

type watchdogRecoveryState struct {
	attempt int
	first   time.Time
	backoff time.Duration
}

func (s *Session) captureWatchdogRecoveryCheckpoint() {
	if s == nil {
		return
	}
	cp, err := s.buildWatchdogRecoveryCheckpoint()
	if err != nil {
		if errors.Is(err, errNoWatchdogRecoveryWindow) {
			return
		}
		if s.logger != nil {
			s.logger.Warn("watchdog recovery checkpoint unavailable",
				"event", "watchdog_recovery_snapshot",
				"session", s.Name,
				"error", err,
			)
		}
		return
	}
	s.watchdogRecoveryCheckpoint = cp
}

func (s *Session) buildWatchdogRecoveryCheckpoint() (*checkpoint.ServerCheckpoint, error) {
	if s == nil || len(s.Windows) == 0 {
		return nil, errNoWatchdogRecoveryWindow
	}

	idleSnap := s.snapshotIdleState()
	snap := s.snapshotLayout(idleSnap)
	cp := &checkpoint.ServerCheckpoint{
		Version:       checkpoint.ServerCheckpointVersion,
		SessionName:   s.Name,
		StartedAt:     s.startedAt,
		Counter:       s.counter.Load(),
		WindowCounter: s.windowCounter.Load(),
		Generation:    s.generation.Load(),
		Layout:        *snap,
		Mailbox:       mailboxCheckpointSnapshot(s.mailbox),
		MailboxSeq:    s.mailboxEventSeq,
	}

	cp.Panes = make([]checkpoint.PaneCheckpoint, len(s.Panes))
	for i, p := range s.Panes {
		pc := checkpoint.PaneCheckpoint{
			ID:           p.ID,
			Meta:         p.Meta,
			ManualBranch: p.MetaManualBranch(),
			CreatedAt:    p.CreatedAt(),
			IsProxy:      p.IsProxy(),
		}
		if p.IsProxy() {
			pc.PtmxFd = -1
			if s.mirror != nil {
				if ref, ok := s.mirror.RemoteRef(p.ID); ok {
					pc.RemoteRef = ref
				}
			}
		} else {
			pc.PtmxFd = p.PtmxFd()
			pc.PID = p.ProcessPid()
		}
		for _, w := range s.Windows {
			if cell := w.Root.FindPane(p.ID); cell != nil {
				pc.Cols = cell.W
				pc.Rows = mux.PaneContentHeight(cell.H)
				break
			}
		}
		cp.Panes[i] = pc
	}

	return cp, nil
}

func (s *Server) recoverFromWatchdogTimeout(sess *Session, info eventloop.WatchdogTimeoutInfo) {
	if s == nil || sess == nil {
		return
	}
	s.emitWatchdogGoroutineDump(sess, info)

	state, ok := nextWatchdogRecoveryState(time.Now(), os.Getenv(watchdogRecoveryAttemptsEnv), os.Getenv(watchdogRecoveryFirstEnv))
	if !ok {
		if sess.logger != nil {
			sess.logger.Error("watchdog recovery attempts exhausted",
				"event", "event_loop_watchdog_recovery",
				"session", info.StateName,
				"max_attempts", maxWatchdogRecoveryAttempts,
				"window", watchdogRecoveryWindow.String(),
			)
		}
		return
	}

	if state.backoff > 0 {
		s.sleepBeforeWatchdogRecovery(state.backoff)
	}

	execPath, err := s.resolveWatchdogRecoveryExecPath()
	if err != nil {
		s.logWatchdogRecoveryError(sess, info, "resolving executable for watchdog recovery failed", err)
		return
	}

	cp := cloneServerCheckpoint(sess.watchdogRecoveryCheckpoint)
	if cp == nil {
		s.reexecWatchdogCrashFallback(sess, info, execPath, state, fmt.Errorf("no last-known-good checkpoint"))
		return
	}
	if s.listener == nil {
		s.reexecWatchdogCrashFallback(sess, info, execPath, state, fmt.Errorf("listener unavailable"))
		return
	}
	lnFd, err := listenerFd(s.listener)
	if err != nil {
		s.reexecWatchdogCrashFallback(sess, info, execPath, state, fmt.Errorf("getting listener fd: %w", err))
		return
	}
	cp.ListenerFd = lnFd
	if s.sessionLock != nil {
		cp.SessionLockFd = int(s.sessionLock.Fd())
	}
	if err := s.writeWatchdogCrashFallback(sess, cp); err != nil {
		s.logWatchdogRecoveryError(sess, info, "writing watchdog crash fallback failed", err)
	}

	cpPath, err := checkpoint.Write(cp)
	if err != nil {
		s.logWatchdogRecoveryError(sess, info, "writing watchdog recovery checkpoint failed", err)
		return
	}
	if err := clearWatchdogRecoveryCloexec(cp); err != nil {
		_ = os.Remove(cpPath)
		s.logWatchdogRecoveryError(sess, info, "clearing close-on-exec for watchdog recovery failed", err)
		return
	}

	env := watchdogRecoveryEnv(os.Environ(), s.Env.Export(), cpPath, state)
	if sess.logger != nil {
		sess.logger.Error("watchdog recovery re-execing server",
			"event", "event_loop_watchdog_recovery",
			"session", info.StateName,
			"command_type", info.CommandType,
			"checkpoint", cpPath,
			"attempt", state.attempt,
			"max_attempts", maxWatchdogRecoveryAttempts,
			"backoff", state.backoff.String(),
		)
	}
	if err := s.execWatchdogRecovery(execPath, os.Args, env); err != nil {
		_ = os.Remove(cpPath)
		s.logWatchdogRecoveryError(sess, info, "watchdog recovery exec failed", err)
	}
}

func (s *Server) reexecWatchdogCrashFallback(sess *Session, info eventloop.WatchdogTimeoutInfo, execPath string, state watchdogRecoveryState, cause error) {
	crashPaths := checkpoint.FindCrashCheckpoints(sess.Name)
	if len(crashPaths) == 0 {
		s.logWatchdogRecoveryError(sess, info, "watchdog recovery checkpoint missing and no crash fallback found", cause)
		return
	}
	env := watchdogRecoveryEnvWithoutCheckpoint(os.Environ(), s.Env.Export(), state)
	if sess.logger != nil {
		sess.logger.Error("watchdog recovery re-execing server with crash checkpoint fallback",
			"event", "event_loop_watchdog_recovery",
			"session", info.StateName,
			"command_type", info.CommandType,
			"checkpoint_kind", "crash",
			"checkpoint", crashPaths[0],
			"attempt", state.attempt,
			"max_attempts", maxWatchdogRecoveryAttempts,
			"backoff", state.backoff.String(),
			"cause", cause,
		)
	}
	if err := s.execWatchdogRecovery(execPath, os.Args, env); err != nil {
		s.logWatchdogRecoveryError(sess, info, "watchdog recovery crash fallback exec failed", err)
	}
}

func (s *Server) resolveWatchdogRecoveryExecPath() (string, error) {
	if s != nil && s.ResolveReloadExecPath != nil {
		return s.ResolveReloadExecPath()
	}
	return reload.ResolveExecutable()
}

func (s *Server) execWatchdogRecovery(execPath string, argv []string, env []string) error {
	if s != nil && s.watchdogRecoveryExec != nil {
		return s.watchdogRecoveryExec(execPath, argv, env)
	}
	return syscall.Exec(execPath, argv, env)
}

func (s *Server) sleepBeforeWatchdogRecovery(d time.Duration) {
	if d <= 0 {
		return
	}
	if s != nil && s.watchdogRecoverySleep != nil {
		s.watchdogRecoverySleep(d)
		return
	}
	time.Sleep(d)
}

func (s *Server) emitWatchdogGoroutineDump(sess *Session, info eventloop.WatchdogTimeoutInfo) {
	if sess != nil && sess.logger != nil {
		sess.logger.Error("emitting SIGQUIT-style goroutine dump for watchdog timeout",
			"event", "event_loop_watchdog_sigquit_dump",
			"session", info.StateName,
			"command_type", info.CommandType,
			"goroutine_id", info.GoroutineID,
		)
	}
	if s != nil && s.watchdogGoroutineDump != nil {
		_ = s.watchdogGoroutineDump()
		return
	}
	if dump := pprof.Lookup("goroutine"); dump != nil {
		_ = dump.WriteTo(os.Stderr, 2)
	}
}

func (s *Server) logWatchdogRecoveryError(sess *Session, info eventloop.WatchdogTimeoutInfo, msg string, err error) {
	if sess == nil || sess.logger == nil {
		return
	}
	sess.logger.Error(msg,
		"event", "event_loop_watchdog_recovery",
		"session", info.StateName,
		"command_type", info.CommandType,
		"error", err,
	)
}

func (s *Server) writeWatchdogCrashFallback(sess *Session, cp *checkpoint.ServerCheckpoint) error {
	if sess == nil || cp == nil {
		return nil
	}
	if paths := checkpoint.FindCrashCheckpoints(sess.Name); len(paths) > 0 {
		return nil
	}
	crashCP := crashCheckpointFromReloadCheckpoint(cp)
	return checkpoint.WriteCrash(crashCP, sess.Name, sess.startedAt)
}

func crashCheckpointFromReloadCheckpoint(cp *checkpoint.ServerCheckpoint) *checkpoint.CrashCheckpoint {
	crashCP := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   cp.SessionName,
		Counter:       cp.Counter,
		WindowCounter: cp.WindowCounter,
		Generation:    cp.Generation,
		Layout:        cp.Layout,
		Mailbox:       cp.Mailbox,
		MailboxSeq:    cp.MailboxSeq,
		Timestamp:     time.Now(),
		PaneStates:    make([]checkpoint.CrashPaneState, len(cp.Panes)),
	}
	for i, pane := range cp.Panes {
		crashCP.PaneStates[i] = checkpoint.CrashPaneState{
			ID:           pane.ID,
			Meta:         pane.Meta,
			ManualBranch: pane.ManualBranch,
			Cols:         pane.Cols,
			Rows:         pane.Rows,
			History:      pane.History,
			Screen:       pane.Screen,
			CreatedAt:    pane.CreatedAt,
			IsProxy:      pane.IsProxy,
			RemoteRef:    pane.RemoteRef,
		}
	}
	return crashCP
}

func clearWatchdogRecoveryCloexec(cp *checkpoint.ServerCheckpoint) error {
	if cp == nil {
		return nil
	}
	if cp.ListenerFd > 0 {
		if err := clearCloexec(uintptr(cp.ListenerFd)); err != nil {
			return fmt.Errorf("listener fd %d: %w", cp.ListenerFd, err)
		}
	}
	if cp.SessionLockFd > 0 {
		if err := clearCloexec(uintptr(cp.SessionLockFd)); err != nil {
			return fmt.Errorf("session lock fd %d: %w", cp.SessionLockFd, err)
		}
	}
	for _, pane := range cp.Panes {
		if pane.IsProxy || pane.PtmxFd < 0 {
			continue
		}
		if err := clearCloexec(uintptr(pane.PtmxFd)); err != nil {
			return fmt.Errorf("pane %d PTY fd %d: %w", pane.ID, pane.PtmxFd, err)
		}
	}
	return nil
}

func nextWatchdogRecoveryState(now time.Time, attemptsText, firstText string) (watchdogRecoveryState, bool) {
	attempts, _ := strconv.Atoi(strings.TrimSpace(attemptsText))
	firstUnix, _ := strconv.ParseInt(strings.TrimSpace(firstText), 10, 64)
	first := time.Unix(firstUnix, 0)
	if attempts < 0 || firstUnix <= 0 || now.Sub(first) > watchdogRecoveryWindow {
		attempts = 0
		first = now
	}
	if attempts >= maxWatchdogRecoveryAttempts {
		return watchdogRecoveryState{}, false
	}

	attempt := attempts + 1
	return watchdogRecoveryState{
		attempt: attempt,
		first:   first,
		backoff: watchdogRecoveryBackoff(attempt),
	}, true
}

func watchdogRecoveryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	return time.Duration(1<<(attempt-2)) * time.Second
}

func watchdogRecoveryEnv(base []string, serverEnv []string, cpPath string, state watchdogRecoveryState) []string {
	env := replaceEnv(base,
		"AMUX_CHECKPOINT="+cpPath,
		watchdogRecoveryAttemptsEnv+"="+strconv.Itoa(state.attempt),
		watchdogRecoveryFirstEnv+"="+strconv.FormatInt(state.first.Unix(), 10),
	)
	return replaceEnv(env, serverEnv...)
}

func watchdogRecoveryEnvWithoutCheckpoint(base []string, serverEnv []string, state watchdogRecoveryState) []string {
	env := removeEnv(base, "AMUX_CHECKPOINT")
	env = replaceEnv(env,
		watchdogRecoveryAttemptsEnv+"="+strconv.Itoa(state.attempt),
		watchdogRecoveryFirstEnv+"="+strconv.FormatInt(state.first.Unix(), 10),
	)
	return replaceEnv(env, serverEnv...)
}

func removeEnv(base []string, keys ...string) []string {
	remove := make(map[string]bool, len(keys))
	for _, key := range keys {
		remove[key] = true
	}
	out := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && remove[key] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func replaceEnv(base []string, values ...string) []string {
	replace := make(map[string]string, len(values))
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		replace[key] = value
	}
	out := make([]string, 0, len(base)+len(values))
	seen := make(map[string]bool, len(replace))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if value, exists := replace[key]; exists {
				out = append(out, value)
				seen[key] = true
				continue
			}
		}
		out = append(out, entry)
	}
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || seen[key] {
			continue
		}
		out = append(out, value)
		seen[key] = true
	}
	return out
}

func cloneServerCheckpoint(cp *checkpoint.ServerCheckpoint) *checkpoint.ServerCheckpoint {
	if cp == nil {
		return nil
	}
	clone := *cp
	clone.Panes = append([]checkpoint.PaneCheckpoint(nil), cp.Panes...)
	return &clone
}
