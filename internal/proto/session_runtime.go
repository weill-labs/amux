package proto

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

const SocketDirEnv = "AMUX_SOCKET_DIR"

// DefaultSocketDir returns the system-level socket directory used when no
// socket directory override is configured.
func DefaultSocketDir() string {
	return fmt.Sprintf("/tmp/amux-%d", os.Getuid())
}

// SocketDir returns the directory for amux Unix sockets.
func SocketDir() string {
	if dir := os.Getenv(SocketDirEnv); dir != "" {
		return dir
	}
	return DefaultSocketDir()
}

// SocketPath returns the socket path for a session.
func SocketPath(session string) string {
	return filepath.Join(SocketDir(), session)
}
