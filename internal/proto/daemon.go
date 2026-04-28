package proto

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/dialutil"
)

type daemonFns struct {
	socketAlive   func(string) bool
	startDaemon   func(string) error
	waitForSocket func(string, time.Duration) error
}

type daemonCmd interface {
	Start() error
	Release() error
}

type lockFile interface {
	Close() error
	Fd() uintptr
}

type startDaemonDeps struct {
	executable func() (string, error)
	mkdirAll   func(string, os.FileMode) error
	openLog    func(string) (io.WriteCloser, error)
	newCommand func(exe, sessionName string, logFile io.Writer) daemonCmd
}

type startupLockDeps struct {
	mkdirAll func(string, os.FileMode) error
	openFile func(string, int, os.FileMode) (lockFile, error)
	flock    func(int, int) error
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

type execDaemonCmd struct {
	cmd *exec.Cmd
}

func (c execDaemonCmd) Start() error {
	return c.cmd.Start()
}

func (c execDaemonCmd) Release() error {
	return c.cmd.Process.Release()
}

func defaultStartDaemonDeps() startDaemonDeps {
	return startDaemonDeps{
		executable: os.Executable,
		mkdirAll:   os.MkdirAll,
		openLog: func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		},
		newCommand: func(exe, sessionName string, logFile io.Writer) daemonCmd {
			cmd := exec.Command(exe, "_server", sessionName)
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Setsid: true, // Detach from controlling terminal
			}
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			cmd.Stdin = nil
			return execDaemonCmd{cmd: cmd}
		},
	}
}

func startDaemonWithDeps(sessionName string, deps startDaemonDeps) error {
	exe, err := deps.executable()
	if err != nil {
		return err
	}

	logDir := SocketDir()
	if err := deps.mkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}
	logPath := filepath.Join(logDir, sessionName+".log")
	logFile, err := deps.openLog(logPath)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}

	cmd := deps.newCommand(exe, sessionName, logFile)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()

	// Release the child process so it runs independently.
	return cmd.Release()
}

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error {
	return startDaemonWithDeps(sessionName, defaultStartDaemonDeps())
}

// EnsureDaemon starts the server for a session if needed. Concurrent callers
// for the same session are serialized so only one daemon is spawned.
func EnsureDaemon(sessionName string, timeout time.Duration) error {
	return daemonFns{}.ensureDaemon(sessionName, timeout)
}

// SocketAlive checks if a socket exists and a server is listening on it.
func SocketAlive(sockPath string) bool {
	conn, err := dialutil.DialUnix(sockPath)
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

func defaultStartupLockDeps() startupLockDeps {
	return startupLockDeps{
		mkdirAll: os.MkdirAll,
		openFile: func(path string, flag int, perm os.FileMode) (lockFile, error) {
			return os.OpenFile(path, flag, perm)
		},
		flock: syscall.Flock,
	}
}

func withSessionStartupLockWithDeps(sessionName string, deps startupLockDeps, fn func() error) (err error) {
	if err := deps.mkdirAll(SocketDir(), 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	lockPath := filepath.Join(SocketDir(), sessionName+".start.lock")
	lockFile, err := deps.openFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening startup lock: %w", err)
	}
	defer lockFile.Close()

	if err := deps.flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking startup lock: %w", err)
	}
	defer func() {
		unlockErr := deps.flock(int(lockFile.Fd()), syscall.LOCK_UN)
		if err == nil && unlockErr != nil {
			err = fmt.Errorf("unlocking startup lock: %w", unlockErr)
		}
	}()

	return fn()
}

func withSessionStartupLock(sessionName string, fn func() error) error {
	return withSessionStartupLockWithDeps(sessionName, defaultStartupLockDeps(), fn)
}
