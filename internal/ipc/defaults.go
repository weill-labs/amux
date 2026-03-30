// Package ipc defines shared client/server IPC bootstrap helpers that do not
// belong to either side of the runtime boundary.
package ipc

import (
	"fmt"
	"os"
	"path/filepath"
)

// Default terminal dimensions when the client doesn't report a size.
const (
	DefaultTermCols = 80
	DefaultTermRows = 24
)

// DefaultSessionName is the implicit session used when callers do not specify one.
const DefaultSessionName = "main"

// SocketDir returns the directory for amux Unix sockets.
func SocketDir() string {
	return fmt.Sprintf("/tmp/amux-%d", os.Getuid())
}

// SocketPath returns the socket path for a session.
func SocketPath(session string) string {
	return filepath.Join(SocketDir(), session)
}
