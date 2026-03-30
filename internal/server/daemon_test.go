package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/ipc"
)

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
		if e.Type()&os.ModeSocket == 0 {
			continue
		}
		sockPath := filepath.Join(dir, name)
		if ipc.SocketAlive(sockPath) {
			continue
		}
		os.Remove(sockPath)
		os.Remove(sockPath + ".log")
	}
}

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

func TestDetectCrashedSessionReturnsNewestCheckpoint(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	session := fmt.Sprintf("detect-crashed-%d", time.Now().UnixNano())
	socketPath := SocketPath(session)
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	older := checkpoint.CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 55, 0, time.UTC))
	newer := checkpoint.CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 56, 0, time.UTC))
	for _, path := range []string{older, newer} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	if got := DetectCrashedSession(session); got != newer {
		t.Fatalf("DetectCrashedSession() = %q, want %q", got, newer)
	}
}

func TestDetectCrashedSessionWithoutCheckpoint(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if got := DetectCrashedSession(fmt.Sprintf("detect-none-%d", time.Now().UnixNano())); got != "" {
		t.Fatalf("DetectCrashedSession() = %q, want empty string when no checkpoint exists", got)
	}
}

func TestDetectCrashedSessionSkipsLiveSocket(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	session := fmt.Sprintf("detect-live-%d", time.Now().UnixNano())
	socketPath := SocketPath(session)
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	crashPath := checkpoint.CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 56, 0, time.UTC))
	if err := os.MkdirAll(filepath.Dir(crashPath), 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(crashPath), err)
	}
	if err := os.WriteFile(crashPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("WriteFile(%q): %v", crashPath, err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", socketPath, err)
	}
	defer ln.Close()

	if got := DetectCrashedSession(session); got != "" {
		t.Fatalf("DetectCrashedSession() = %q, want empty string while socket is live", got)
	}
}
