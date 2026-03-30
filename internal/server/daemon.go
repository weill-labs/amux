package server

import (
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/proto"
)

// DetectCrashedSession checks if a crash checkpoint exists for the given
// session AND the server socket is stale or missing. Returns the checkpoint
// path if a crashed session is detected, or "" if no recovery is needed.
func DetectCrashedSession(sessionName string) string {
	cpPaths := checkpoint.FindCrashCheckpoints(sessionName)
	if len(cpPaths) == 0 {
		return "" // no crash checkpoint
	}

	sockPath := SocketPath(sessionName)
	if proto.SocketAlive(sockPath) {
		return "" // server is running — no crash
	}

	return cpPaths[0]
}
