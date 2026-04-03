package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/remote"
	"github.com/weill-labs/amux/internal/server"
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/sys/unix"
)

func newBootstrapLogger() *charmlog.Logger {
	return auditlog.New(os.Stderr, auditlog.Options{
		Format:          auditlog.FormatAuto,
		Level:           charmlog.InfoLevel,
		Prefix:          "amux",
		ReportTimestamp: true,
	})
}

func exitBootstrapError(logger *charmlog.Logger, sessionName, msg string, err error) {
	logger.Error(msg,
		"event", "server_bootstrap",
		"session", sessionName,
		"error", err,
	)
	os.Exit(1)
}

func openSignalFD(envVar, name string) *os.File {
	fdStr := os.Getenv(envVar)
	if fdStr == "" {
		return nil
	}
	os.Unsetenv(envVar)
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil
	}
	return os.NewFile(uintptr(fd), name)
}

func writeSignalFD(f **os.File, msg string) {
	if *f == nil {
		return
	}
	if msg != "" {
		_, _ = (*f).Write([]byte(msg))
	}
	(*f).Close()
	*f = nil
}

func RestoreServerFromReloadCheckpoint(sessionName, cpPath string, scrollbackLines int) (*server.Server, error) {
	return restoreServerFromReloadCheckpointLogger(sessionName, cpPath, scrollbackLines, newBootstrapLogger())
}

func restoreServerFromReloadCheckpointLogger(sessionName, cpPath string, scrollbackLines int, logger *charmlog.Logger) (*server.Server, error) {
	cp, err := checkpoint.Read(cpPath)
	if err == nil {
		return server.NewServerFromCheckpointWithScrollbackLogger(cp, scrollbackLines, logger)
	}

	var versionErr checkpoint.UnsupportedServerCheckpointVersionError
	if !errors.As(err, &versionErr) {
		return nil, err
	}

	restoreSessionName := sessionName
	if cp.SessionName != "" {
		restoreSessionName = cp.SessionName
	}

	crashPaths := checkpoint.FindCrashCheckpoints(restoreSessionName)
	if len(crashPaths) == 0 {
		return nil, fmt.Errorf("%w; no crash checkpoint fallback found", err)
	}

	crashPath := crashPaths[0]
	crashCP, crashErr := checkpoint.ReadCrash(crashPath)
	if crashErr != nil {
		return nil, fmt.Errorf("%w; crash fallback %s: %v", err, crashPath, crashErr)
	}
	if cp.ListenerFd <= 0 {
		return nil, fmt.Errorf("%w; invalid listener fd %d in reload checkpoint", err, cp.ListenerFd)
	}

	logger.Warn("reload checkpoint incompatible; falling back to crash checkpoint",
		"event", "checkpoint_restore_fallback",
		"session", restoreSessionName,
		"checkpoint_kind", "reload",
		"fallback_kind", "crash",
		"path", crashPath,
		"error", err,
	)
	return server.NewServerFromCrashCheckpointWithListenerFdLogger(restoreSessionName, cp.ListenerFd, crashCP, crashPath, scrollbackLines, logger)
}

func RunServer(sessionName string, managedTakeover bool, buildVersion string) {
	server.BuildVersion = buildVersion
	logger := newBootstrapLogger()

	if err := terminfo.Install(); err != nil {
		exitBootstrapError(logger, sessionName, "server bootstrap failed", err)
	}

	var s *server.Server
	var err error

	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		logger.Warn("loading config failed; using defaults",
			"event", "server_config",
			"session", sessionName,
			"error", cfgErr,
		)
		cfg = &config.Config{Hosts: make(map[string]config.Host)}
	}
	scrollbackLines := cfg.EffectiveScrollbackLines()

	if cpPath := os.Getenv("AMUX_CHECKPOINT"); cpPath != "" {
		os.Unsetenv("AMUX_CHECKPOINT")
		s, err = restoreServerFromReloadCheckpointLogger(sessionName, cpPath, scrollbackLines, logger)
		if err != nil {
			logger.Error("reading reload checkpoint failed",
				"event", "checkpoint_restore",
				"session", sessionName,
				"checkpoint_kind", "reload",
				"path", cpPath,
				"error", err,
			)
			os.Exit(1)
		}
	} else if crashPath := server.DetectCrashedSession(sessionName); crashPath != "" {
		crashCP, readErr := checkpoint.ReadCrash(crashPath)
		if readErr != nil {
			logger.Warn("unreadable crash checkpoint; starting fresh",
				"event", "checkpoint_restore",
				"session", sessionName,
				"checkpoint_kind", "crash",
				"path", crashPath,
				"error", readErr,
			)
			_ = checkpoint.RemoveCrashFile(crashPath)
			s, err = server.NewServerWithScrollbackLogger(sessionName, scrollbackLines, logger)
			if err != nil {
				exitBootstrapError(logger, sessionName, "creating server failed", err)
			}
		} else {
			logger.Info("recovering crashed session",
				"event", "checkpoint_restore",
				"session", sessionName,
				"checkpoint_kind", "crash",
				"path", crashPath,
			)
			s, err = server.NewServerFromCrashCheckpointWithScrollbackLogger(sessionName, crashCP, crashPath, scrollbackLines, logger)
			if err != nil {
				logger.Error("crash recovery failed",
					"event", "checkpoint_restore",
					"session", sessionName,
					"checkpoint_kind", "crash",
					"path", crashPath,
					"error", err,
				)
				os.Exit(1)
			}
		}
	} else {
		s, err = server.NewServerWithScrollbackLogger(sessionName, scrollbackLines, logger)
		if err != nil {
			exitBootstrapError(logger, sessionName, "creating server failed", err)
		}
	}

	// Must be set before event loops can observe Env (e.g., exit-unattached).
	s.Env = server.ReadServerEnv()
	if cfg.PprofEnabled() {
		if err := s.EnablePprof(); err != nil {
			exitBootstrapError(logger, sessionName, "enabling pprof debug endpoint failed", err)
		}
	}
	readySignal := openSignalFD("AMUX_READY_FD", "ready-signal")
	shutdownSignal := openSignalFD("AMUX_SHUTDOWN_FD", "shutdown-signal")
	defer writeSignalFD(&readySignal, "")
	defer writeSignalFD(&shutdownSignal, "")

	metaRefreshEnabled := os.Getenv("AMUX_DISABLE_META_REFRESH") != "1"
	s.SetPaneMetaAutoRefresh(metaRefreshEnabled)

	if managedTakeover {
		if err := s.EnsureInitialWindow(server.DefaultTermCols, server.DefaultTermRows); err != nil {
			exitBootstrapError(logger, sessionName, "initializing managed takeover session failed", err)
		}
	}

	hasRemoteHosts := false
	for _, host := range cfg.Hosts {
		if host.Type != "local" {
			hasRemoteHosts = true
			break
		}
	}
	newRemoteManager := func(hooks server.PaneTransportHooks) *remote.Manager {
		return remote.NewManager(cfg, server.BuildVersion, remote.ManagerDeps{
			NewHostConn:   remote.NewHostConn,
			OnPaneOutput:  hooks.OnPaneOutput,
			OnPaneExit:    hooks.OnPaneExit,
			OnStateChange: hooks.OnStateChange,
			Logger:        logger.With("component", "ssh"),
		})
	}
	if hasRemoteHosts {
		s.SetupPaneTransport(cfg.HostColor, func(hooks server.PaneTransportHooks) proto.PaneTransport {
			return newRemoteManager(hooks)
		})
	} else {
		s.SetupPaneTakeoverTransport(func(hooks server.PaneTransportHooks) server.PaneTakeoverTransport {
			return newRemoteManager(hooks)
		})
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.Shutdown()
	}()

	triggerReload := make(chan struct{}, 1)
	execPath, execErr := reload.ResolveExecutable()
	if execErr == nil && !s.Env.NoWatch {
		go reload.WatchBinary(execPath, triggerReload, nil)
		go func() {
			for range triggerReload {
				_ = s.Reload(execPath)
			}
		}()
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run()
	}()
	logger.Info("server started",
		"event", "server_start",
		"session", sessionName,
		"socket", server.SocketPath(sessionName),
		"managed_takeover", managedTakeover,
	)

	writeSignalFD(&readySignal, "ready\n")

	runResult := <-runErr
	s.Shutdown()
	writeSignalFD(&shutdownSignal, "shutdown\n")

	if runResult != nil && !strings.Contains(runResult.Error(), "use of closed") {
		logger.Error("server run failed",
			"event", "server_run",
			"session", sessionName,
			"error", runResult,
		)
		os.Exit(1)
	}
}

func CheckNesting(target string) {
	if envSession := os.Getenv("AMUX_SESSION"); envSession == target {
		logger := newBootstrapLogger()
		logger.Error("cannot attach to session from inside itself (recursive nesting)",
			"event", "nesting_check",
			"session", target,
			"hint", "unset AMUX_SESSION to override",
		)
		os.Exit(1)
	}
}

func ShouldAttemptTakeover() bool {
	return os.Getenv("SSH_CONNECTION") != "" && os.Getenv("TERM") == "amux" && os.Getenv("AMUX_PANE") == ""
}

func TryTakeover(sessionName string, buildVersion string) bool {
	hostname, _ := os.Hostname()

	req := mux.TakeoverRequest{
		Session: sessionName + "@" + hostname,
		Host:    hostname,
		UID:     fmt.Sprintf("%d", os.Getuid()),
		Panes:   []mux.TakeoverPane{},
	}

	if sshConn := os.Getenv("SSH_CONNECTION"); sshConn != "" {
		if parts := strings.Fields(sshConn); len(parts) >= 4 {
			req.SSHAddress = parts[2] + ":" + parts[3]
		}
	}
	if user := os.Getenv("USER"); user != "" {
		req.SSHUser = user
	}

	os.Stdout.Write(mux.FormatTakeoverSequence(req))

	session, ok := waitForTakeoverAck(os.Stdin, req.Session, 2*time.Second)
	if !ok {
		return false
	}
	newBootstrapLogger().Info("takeover acked, entering managed mode",
		"event", "ssh_takeover_ack",
		"session", session,
	)
	RunServer(session, true, buildVersion)
	return true
}

func waitForTakeoverAck(stdin *os.File, fallbackSession string, timeout time.Duration) (string, bool) {
	const maxTakeoverAckBuffer = 4 * 1024

	fd := int32(stdin.Fd())
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 256)

	for {
		if session, ok := mux.FindTakeoverAck(buf, fallbackSession); ok {
			return session, true
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", false
		}

		timeoutMS := int((remaining + time.Millisecond - 1) / time.Millisecond)
		n, err := unix.Poll([]unix.PollFd{{Fd: fd, Events: unix.POLLIN}}, timeoutMS)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return "", false
		}
		if n == 0 {
			return "", false
		}

		readN, readErr := stdin.Read(tmp)
		if readN > 0 {
			buf = append(buf, tmp[:readN]...)
			if len(buf) > maxTakeoverAckBuffer {
				buf = append(buf[:0], buf[len(buf)-maxTakeoverAckBuffer:]...)
			}
		}
		if readErr != nil {
			if session, ok := mux.FindTakeoverAck(buf, fallbackSession); ok {
				return session, true
			}
			return "", false
		}
	}
}
