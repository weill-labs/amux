package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanStaleSocketsIn(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a stale socket: listen, disable auto-unlink, then close.
	// This leaves a socket file on disk with no server behind it.
	staleSock := filepath.Join(tmpDir, "stale-session")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: staleSock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	ln.Close()

	staleLog := staleSock + ".log"
	os.WriteFile(staleLog, []byte("old log"), 0600)

	// Create a live socket (server listening).
	liveSock := filepath.Join(tmpDir, "live-session")
	liveLn, err := net.Listen("unix", liveSock)
	if err != nil {
		t.Fatal(err)
	}
	defer liveLn.Close()

	liveLog := liveSock + ".log"
	os.WriteFile(liveLog, []byte("live log"), 0600)

	// Create an orphaned log with no socket.
	orphanLog := filepath.Join(tmpDir, "orphan-session.log")
	os.WriteFile(orphanLog, []byte("orphan"), 0600)

	// Create a regular file (not a socket) — should be ignored.
	regularFile := filepath.Join(tmpDir, "not-a-socket")
	os.WriteFile(regularFile, []byte("data"), 0600)

	cleanStaleSocketsIn(tmpDir)

	// Stale socket and its log should be removed.
	if _, err := os.Stat(staleSock); !os.IsNotExist(err) {
		t.Error("stale socket was not removed")
	}
	if _, err := os.Stat(staleLog); !os.IsNotExist(err) {
		t.Error("stale log was not removed")
	}

	// Live socket and its log should remain.
	if _, err := os.Stat(liveSock); err != nil {
		t.Error("live socket was incorrectly removed")
	}
	if _, err := os.Stat(liveLog); err != nil {
		t.Error("live log was incorrectly removed")
	}

	// Orphaned log (no socket) should remain.
	if _, err := os.Stat(orphanLog); err != nil {
		t.Error("orphaned log was incorrectly removed")
	}

	// Regular file should remain.
	if _, err := os.Stat(regularFile); err != nil {
		t.Error("regular file was incorrectly removed")
	}
}
