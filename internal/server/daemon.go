package server

import (
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/proto"
)

// StartDaemon launches the server as a background daemon.
func StartDaemon(sessionName string) error {
	return proto.StartDaemon(sessionName)
}

// EnsureDaemon starts the server for a session if needed. Concurrent callers
// for the same session are serialized so only one daemon is spawned.
func EnsureDaemon(sessionName string, timeout time.Duration) error {
	return proto.EnsureDaemon(sessionName, timeout)
}

// SocketAlive checks if a socket exists and a server is listening on it.
func SocketAlive(sockPath string) bool {
	return proto.SocketAlive(sockPath)
}

// WaitForSocket polls until the socket becomes available.
func WaitForSocket(sockPath string, timeout time.Duration) error {
	return proto.WaitForSocket(sockPath, timeout)
}

// DetectCrashedSession checks if a crash checkpoint exists for the given
// session AND the server socket is stale or missing. Returns the checkpoint
// path if a crashed session is detected, or "" if no recovery is needed.
func DetectCrashedSession(sessionName string) string {
	cpPaths := checkpoint.FindCrashCheckpoints(sessionName)
	if len(cpPaths) == 0 {
		return "" // no crash checkpoint
	}

	sockPath := SocketPath(sessionName)
	if SocketAlive(sockPath) {
		return "" // server is running — no crash
	}

	return cpPaths[0]
}
