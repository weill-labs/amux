package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/sys/unix"
)

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

func restoreServerFromReloadCheckpoint(sessionName, cpPath string, scrollbackLines int) (*server.Server, error) {
	cp, err := checkpoint.Read(cpPath)
	if err == nil {
		return server.NewServerFromCheckpointWithScrollback(cp, scrollbackLines)
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

	fmt.Fprintf(os.Stderr, "amux server: reload checkpoint incompatible, falling back to crash checkpoint %s\n", crashPath)
	return server.NewServerFromCrashCheckpointWithListenerFd(restoreSessionName, cp.ListenerFd, crashCP, crashPath, scrollbackLines)
}

func runServer(sessionName string, managedTakeover bool) {
	server.BuildVersion = buildVersion()

	if err := terminfo.Install(); err != nil {
		fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
		os.Exit(1)
	}

	var s *server.Server
	var err error

	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "amux server: loading config: %v\n", cfgErr)
		cfg = &config.Config{Hosts: make(map[string]config.Host)}
	}
	scrollbackLines := cfg.EffectiveScrollbackLines()

	if cpPath := os.Getenv("AMUX_CHECKPOINT"); cpPath != "" {
		os.Unsetenv("AMUX_CHECKPOINT")
		s, err = restoreServerFromReloadCheckpoint(sessionName, cpPath, scrollbackLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: reading checkpoint: %v\n", err)
			os.Exit(1)
		}
	} else if crashPath := server.DetectCrashedSession(sessionName); crashPath != "" {
		crashCP, readErr := checkpoint.ReadCrash(crashPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "amux server: unreadable crash checkpoint, starting fresh: %v\n", readErr)
			_ = checkpoint.RemoveCrashFile(crashPath)
			s, err = server.NewServerWithScrollback(sessionName, scrollbackLines)
			if err != nil {
				fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "amux server: recovering crashed session %q\n", sessionName)
			s, err = server.NewServerFromCrashCheckpointWithScrollback(sessionName, crashCP, crashPath, scrollbackLines)
			if err != nil {
				fmt.Fprintf(os.Stderr, "amux server: crash recovery: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		s, err = server.NewServerWithScrollback(sessionName, scrollbackLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
			os.Exit(1)
		}
	}

	s.Env = server.ReadServerEnv()
	readySignal := openSignalFD("AMUX_READY_FD", "ready-signal")
	shutdownSignal := openSignalFD("AMUX_SHUTDOWN_FD", "shutdown-signal")
	defer writeSignalFD(&readySignal, "")
	defer writeSignalFD(&shutdownSignal, "")

	metaRefreshEnabled := os.Getenv("AMUX_DISABLE_META_REFRESH") != "1"
	s.SetPaneMetaAutoRefresh(metaRefreshEnabled)

	if managedTakeover {
		if err := s.EnsureInitialWindow(server.DefaultTermCols, server.DefaultTermRows); err != nil {
			fmt.Fprintf(os.Stderr, "amux server: initializing managed takeover session: %v\n", err)
			os.Exit(1)
		}
	}

	s.SetupRemoteManager(cfg, server.BuildVersion)

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
				if reloadErr := s.Reload(execPath); reloadErr != nil {
					fmt.Fprintf(os.Stderr, "amux server: reload failed: %v\n", reloadErr)
				}
			}
		}()
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run()
	}()

	writeSignalFD(&readySignal, "ready\n")

	runResult := <-runErr
	s.Shutdown()
	writeSignalFD(&shutdownSignal, "shutdown\n")

	if runResult != nil && !strings.Contains(runResult.Error(), "use of closed") {
		fmt.Fprintf(os.Stderr, "amux server: %v\n", runResult)
		os.Exit(1)
	}
}

func checkNesting(target string) {
	if envSession := os.Getenv("AMUX_SESSION"); envSession == target {
		fmt.Fprintf(os.Stderr, "amux: cannot attach to session %q from inside itself (recursive nesting)\n", target)
		fmt.Fprintln(os.Stderr, "  unset AMUX_SESSION to override")
		os.Exit(1)
	}
}

func shouldAttemptTakeover() bool {
	return os.Getenv("SSH_CONNECTION") != "" && os.Getenv("TERM") == "amux" && os.Getenv("AMUX_PANE") == ""
}

func tryTakeover(sessionName string) bool {
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
	fmt.Fprintln(os.Stderr, "amux: takeover acked, entering managed mode")
	runServer(session, true)
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
