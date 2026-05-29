package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
	"github.com/weill-labs/amux/internal/terminfo"
)

func newBootstrapLogger() *charmlog.Logger {
	return newBootstrapLoggerWithWriter(os.Stderr)
}

func newBootstrapLoggerWithWriter(w io.Writer) *charmlog.Logger {
	if w == nil {
		w = os.Stderr
	}
	return auditlog.New(w, auditlog.Options{
		Format:          auditlog.FormatAuto,
		Level:           charmlog.InfoLevel,
		Prefix:          "amux",
		ReportTimestamp: true,
	})
}

func installServerLogRotation(sessionName string) (io.Writer, func(), error) {
	logDir := os.Getenv("AMUX_LOG_DIR")
	if logDir == "" {
		logDir = server.SocketDir()
	}
	logPath := filepath.Join(logDir, sessionName+".log")
	return auditlog.InstallProcessLogRotation(logPath, auditlog.DefaultRotationOptions())
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
	return restoreServerFromReloadCheckpointWithScrollbackConfigLogger(sessionName, cpPath, server.NewScrollbackConfig(scrollbackLines, nil), logger)
}

func restoreServerFromReloadCheckpointWithScrollbackConfigLogger(sessionName, cpPath string, scrollback server.ScrollbackConfig, logger *charmlog.Logger) (*server.Server, error) {
	cp, err := checkpoint.Read(cpPath)
	if err == nil {
		return server.NewServerFromCheckpointWithScrollbackConfigLogger(cp, scrollback, logger)
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
		return nil, fmt.Errorf("%w; crash fallback %s: %w", err, crashPath, crashErr)
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
	return server.NewServerFromCrashCheckpointWithScrollbackConfigListenerAndLockFdLogger(restoreSessionName, cp.ListenerFd, cp.SessionLockFd, crashCP, crashPath, scrollback, logger)
}

func RunServer(sessionName string, managedTakeover bool, buildVersion string) {
	server.BuildVersion = buildVersion
	logWriter, cleanupLogRotation, logRotationErr := installServerLogRotation(sessionName)
	if cleanupLogRotation != nil {
		defer cleanupLogRotation()
	}
	logger := newBootstrapLoggerWithWriter(logWriter)
	if logRotationErr != nil {
		logger.Warn("log rotation unavailable; using inherited stderr",
			"event", "log_rotation",
			"session", sessionName,
			"error", logRotationErr,
		)
	}

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
		cfg = &config.Config{}
	}
	scrollback := server.NewScrollbackConfig(cfg.EffectiveScrollbackLines(), nil)

	if cpPath := os.Getenv("AMUX_CHECKPOINT"); cpPath != "" {
		os.Unsetenv("AMUX_CHECKPOINT")
		s, err = restoreServerFromReloadCheckpointWithScrollbackConfigLogger(sessionName, cpPath, scrollback, logger)
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
			s, err = server.NewServerWithScrollbackConfigLogger(sessionName, scrollback, logger)
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
			s, err = server.NewServerFromCrashCheckpointWithScrollbackConfigLogger(sessionName, crashCP, crashPath, scrollback, logger)
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
		s, err = server.NewServerWithScrollbackConfigLogger(sessionName, scrollback, logger)
		if err != nil {
			exitBootstrapError(logger, sessionName, "creating server failed", err)
		}
	}
	s.ConfigureMirrors(cfg.Remote.Hosts, nil)

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

func TryTakeover(_ string, _ string) bool {
	return false
}
