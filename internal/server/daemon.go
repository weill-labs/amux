package server

import (
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/ipc"
)

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error { return ipc.StartDaemon(sessionName) }

// EnsureDaemon starts the server for a session if needed.
func EnsureDaemon(sessionName string, timeout time.Duration) error {
	return ipc.EnsureDaemon(sessionName, timeout)
}

// SocketAlive checks if a socket exists and a server is listening on it.
func SocketAlive(sockPath string) bool { return ipc.SocketAlive(sockPath) }

// DetectCrashedSession checks if a crash checkpoint exists for the given
// session AND the server socket is stale or missing. Returns the checkpoint
// path if a crashed session is detected, or "" if no recovery is needed.
func DetectCrashedSession(sessionName string) string {
	cpPaths := checkpoint.FindCrashCheckpoints(sessionName)
	if len(cpPaths) == 0 {
		return "" // no crash checkpoint
	}

	sockPath := ipc.SocketPath(sessionName)
	if ipc.SocketAlive(sockPath) {
		return "" // server is running — no crash
	}

	return cpPaths[0]
}

// WaitForSocket polls until the socket becomes available.
func WaitForSocket(sockPath string, timeout time.Duration) error {
	return ipc.WaitForSocket(sockPath, timeout)
}
