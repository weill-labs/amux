package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
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
		if SocketAlive(sockPath) {
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

func TestEnsureDaemonStartsServerOnceUnderConcurrency(t *testing.T) {
	session := "ensure-daemon-race-test"
	lockPath := filepath.Join(SocketDir(), session+".start.lock")
	_ = os.Remove(lockPath)
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	origSocketAlive := socketAliveFn
	origStartDaemon := startDaemonFn
	origWaitForSocket := waitForSocketFn
	defer func() {
		socketAliveFn = origSocketAlive
		startDaemonFn = origStartDaemon
		waitForSocketFn = origWaitForSocket
	}()

	var alive atomic.Bool
	var starts atomic.Int32
	var mu sync.Mutex
	started := make(chan struct{}, 1)
	releaseStart := make(chan struct{})

	socketAliveFn = func(sockPath string) bool {
		return alive.Load()
	}
	startDaemonFn = func(sessionName string) error {
		starts.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-releaseStart
		return nil
	}
	waitForSocketFn = func(sockPath string, timeout time.Duration) error {
		mu.Lock()
		alive.Store(true)
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureDaemon(session, 250*time.Millisecond)
		}()
	}
	<-started
	close(releaseStart)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("EnsureDaemon returned error: %v", err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("startDaemon called %d times, want 1", got)
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
