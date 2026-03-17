package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
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

// CleanStaleSockets scans the socket directory and removes sockets (and their
// matching .log files) that belong to dead servers. A socket is considered stale
// if a connect attempt fails, meaning no server is listening.
func CleanStaleSockets() {
	cleanStaleSocketsIn(SocketDir())
}

func cleanStaleSocketsIn(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) == ".log" {
			continue
		}
		// Only consider Unix sockets (mode type bit ModeSocket).
		if e.Type()&os.ModeSocket == 0 {
			continue
		}
		sockPath := filepath.Join(dir, name)
		if SocketAlive(sockPath) {
			continue
		}
		os.Remove(sockPath)
		os.Remove(sockPath + ".log")
	}
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
