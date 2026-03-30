package proto

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type daemonFns struct {
	socketAlive   func(string) bool
	startDaemon   func(string) error
	waitForSocket func(string, time.Duration) error
}

func (fns daemonFns) ensureDaemon(sessionName string, timeout time.Duration) error {
	socketAlive := fns.socketAlive
	if socketAlive == nil {
		socketAlive = SocketAlive
	}
	startDaemon := fns.startDaemon
	if startDaemon == nil {
		startDaemon = StartDaemon
	}
	waitForSocket := fns.waitForSocket
	if waitForSocket == nil {
		waitForSocket = WaitForSocket
	}

	sockPath := SocketPath(sessionName)
	return withSessionStartupLock(sessionName, func() error {
		if socketAlive(sockPath) {
			return nil
		}
		if err := startDaemon(sessionName); err != nil {
			if socketAlive(sockPath) {
				return nil
			}
			return fmt.Errorf("starting server: %w", err)
		}
		return waitForSocket(sockPath, timeout)
	})
}

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	logDir := SocketDir()
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}
	logPath := filepath.Join(logDir, sessionName+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}

	cmd := exec.Command(exe, "_server", sessionName)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from controlling terminal
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()

	// Release the child process so it runs independently.
	return cmd.Process.Release()
}

// EnsureDaemon starts the server for a session if needed. Concurrent callers
// for the same session are serialized so only one daemon is spawned.
func EnsureDaemon(sessionName string, timeout time.Duration) error {
	return daemonFns{}.ensureDaemon(sessionName, timeout)
}

// SocketAlive checks if a socket exists and a server is listening on it.
func SocketAlive(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// WaitForSocket polls until the socket becomes available.
func WaitForSocket(sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if SocketAlive(sockPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not start within %v", timeout)
}

func withSessionStartupLock(sessionName string, fn func() error) error {
	if err := os.MkdirAll(SocketDir(), 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	lockPath := filepath.Join(SocketDir(), sessionName+".start.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening startup lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking startup lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	return fn()
}
