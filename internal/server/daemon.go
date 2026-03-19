package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
)

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	logDir := SocketDir()
	os.MkdirAll(logDir, 0700)
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
		logFile.Close()
		return err
	}
	logFile.Close()

	// Release the child process so it runs independently
	cmd.Process.Release()
	return nil
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

// DetectCrashedSession checks if a crash checkpoint exists for the given
// session AND the server socket is stale or missing. Returns the checkpoint
// path if a crashed session is detected, or "" if no recovery is needed.
func DetectCrashedSession(sessionName string) string {
	cpPath := checkpoint.CrashCheckpointPath(sessionName)
	if _, err := os.Stat(cpPath); err != nil {
		return "" // no crash checkpoint
	}

	sockPath := SocketPath(sessionName)
	if SocketAlive(sockPath) {
		return "" // server is running — no crash
	}

	return cpPath
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
