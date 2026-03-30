package ipc

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

var (
	socketAliveFn   = SocketAlive
	startDaemonFn   = StartDaemon
	waitForSocketFn = WaitForSocket
)

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error {
	return startDaemonWithDeps(
		sessionName,
		os.Executable,
		os.MkdirAll,
		func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		},
		func(exe, sessionName string, logFile *os.File) error {
			return launchDaemonProcess(exec.Command, exe, sessionName, logFile)
		},
	)
}

func startDaemonWithDeps(
	sessionName string,
	executable func() (string, error),
	mkdirAll func(string, os.FileMode) error,
	openLog func(string) (*os.File, error),
	launch func(exe, sessionName string, logFile *os.File) error,
) error {
	exe, err := executable()
	if err != nil {
		return err
	}

	logDir := SocketDir()
	if err := mkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	logPath := filepath.Join(logDir, sessionName+".log")
	logFile, err := openLog(logPath)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer logFile.Close()

	if err := launch(exe, sessionName, logFile); err != nil {
		return err
	}
	return nil
}

func launchDaemonProcess(
	command func(string, ...string) *exec.Cmd,
	exe, sessionName string,
	logFile *os.File,
) error {
	cmd := command(exe, "_server", sessionName)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from controlling terminal
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	// Release the child process so it runs independently.
	cmd.Process.Release()
	return nil
}

// EnsureDaemon starts the server for a session if needed. Concurrent callers
// for the same session are serialized so only one daemon is spawned.
func EnsureDaemon(sessionName string, timeout time.Duration) error {
	sockPath := SocketPath(sessionName)
	return withSessionStartupLock(sessionName, func() error {
		if socketAliveFn(sockPath) {
			return nil
		}
		if err := startDaemonFn(sessionName); err != nil {
			if socketAliveFn(sockPath) {
				return nil
			}
			return fmt.Errorf("starting server: %w", err)
		}
		return waitForSocketFn(sockPath, timeout)
	})
}

// SocketAlive checks if a socket exists and a server is listening on it.
func SocketAlive(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false
	}
	conn.Close()
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
	return withSessionStartupLockWithDeps(
		sessionName,
		os.MkdirAll,
		func(path string, flag int, perm os.FileMode) (*os.File, error) {
			return os.OpenFile(path, flag, perm)
		},
		syscall.Flock,
		fn,
	)
}

func withSessionStartupLockWithDeps(
	sessionName string,
	mkdirAll func(string, os.FileMode) error,
	openFile func(string, int, os.FileMode) (*os.File, error),
	flock func(int, int) error,
	fn func() error,
) error {
	if err := mkdirAll(SocketDir(), 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	lockPath := filepath.Join(SocketDir(), sessionName+".start.lock")
	lockFile, err := openFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening startup lock: %w", err)
	}
	defer lockFile.Close()

	if err := flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking startup lock: %w", err)
	}
	defer flock(int(lockFile.Fd()), syscall.LOCK_UN)

	return fn()
}
