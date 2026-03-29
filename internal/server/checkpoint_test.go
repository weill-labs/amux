package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
)

func TestServerReloadReturnsSessionShuttingDownBeforeCheckpoint(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	srv := &Server{
		sessions: map[string]*Session{DefaultSessionName: sess},
	}

	err := srv.Reload("/definitely/missing")
	if !errors.Is(err, errSessionShuttingDown) {
		t.Fatalf("Reload() error = %v, want %v", err, errSessionShuttingDown)
	}
	if sess.shutdown.Load() {
		t.Fatal("Reload() should not mark session shutdown on early query failure")
	}
}

func TestServerReloadWritesCrashCheckpointBeforeExec(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reload-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer func() {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("os.Remove(%q): %v", socketPath, err)
		}
	}()

	sess := newSession("reload-crash-checkpoint")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	startedAt := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)
	sess.startedAt = startedAt

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())

		win := mux.NewWindow(pane, 80, 23)
		win.ID = 1
		win.Name = "window-1"

		sess.Panes = append(sess.Panes, pane)
		sess.Windows = append(sess.Windows, win)
		sess.ActiveWindowID = win.ID
		sess.counter.Store(1)
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
		return struct{}{}
	})

	srv := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sess.Name: sess},
		sockPath:     socketPath,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = srv

	err = srv.Reload(filepath.Join(t.TempDir(), "missing-amux"))
	if err == nil {
		t.Fatal("Reload() error = nil, want exec failure")
	}

	path := checkpoint.CrashCheckpointPathTimestamped(sess.Name, startedAt)
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected crash checkpoint at %s, err=%v", path, statErr)
	}

	cp, err := checkpoint.ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash(%q): %v", path, err)
	}
	if cp.SessionName != sess.Name {
		t.Fatalf("SessionName = %q, want %q", cp.SessionName, sess.Name)
	}
	if len(cp.PaneStates) != 1 || cp.PaneStates[0].Meta.Name != "pane-1" {
		t.Fatalf("PaneStates = %+v, want single pane-1", cp.PaneStates)
	}
}
