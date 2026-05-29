package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClearCloexecReturnsErrnoOnInvalidFD(t *testing.T) {
	t.Parallel()

	err := clearCloexec(^uintptr(0))
	if err == nil {
		t.Fatal("clearCloexec() error = nil, want errno")
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno == 0 {
		t.Fatalf("clearCloexec() error = %v, want syscall.Errno", err)
	}
}

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

		seedSessionPanesForTest(sess, pane)
		sess.Windows = append(sess.Windows, win)
		sess.ActiveWindowID = win.ID
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

func TestServerShutdownPreservesCrashCheckpointForCrashRestore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sess := newSession("graceful-crash-restore")
	defer stopSessionBackgroundLoops(t, sess)

	startedAt := time.Date(2026, time.March, 27, 12, 34, 56, 0, time.UTC)
	sess.startedAt = startedAt

	newPane := func(id uint32, name string) *mux.Pane {
		pane := newProxyPane(id, mux.PaneMeta{Name: name, Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())
		return pane
	}

	pane1 := newPane(1, "pane-1")
	pane2 := newPane(2, "pane-2")
	win := mux.NewWindow(pane1, 80, 24)
	win.ID = 1
	win.Name = "graceful"
	if _, err := win.Split(mux.SplitVertical, pane2); err != nil {
		t.Fatalf("win.Split: %v", err)
	}

	mustSessionMutation(t, sess, func(sess *Session) {
		seedSessionPanesForTest(sess, pane1, pane2)
		sess.Windows = []*mux.Window{win}
		sess.ActiveWindowID = win.ID
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
	})

	srv := &Server{
		sessions:     map[string]*Session{sess.Name: sess},
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = srv

	crashPath := checkpoint.CrashCheckpointPathTimestamped(sess.Name, startedAt)
	srv.Shutdown()

	if _, statErr := os.Stat(crashPath); statErr != nil {
		t.Fatalf("crash checkpoint should survive clean shutdown, err=%v", statErr)
	}

	cp, err := checkpoint.ReadCrash(crashPath)
	if err != nil {
		t.Fatalf("ReadCrash(%q): %v", crashPath, err)
	}
	if len(cp.Layout.Windows) != 1 || cp.Layout.Windows[0].Name != "graceful" {
		t.Fatalf("restorable checkpoint windows = %+v, want single graceful window", cp.Layout.Windows)
	}
	if len(cp.PaneStates) != 2 {
		t.Fatalf("PaneStates = %d, want 2", len(cp.PaneStates))
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sess.Name, cp, crashPath, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	restoredSess := restored.firstSession()
	if len(restoredSess.Windows) != 1 || restoredSess.Windows[0].Name != "graceful" {
		t.Fatalf("restored windows = %+v, want single graceful window", restoredSess.Windows)
	}
	if len(restoredSess.Panes) != 2 {
		t.Fatalf("restored panes = %d, want 2", len(restoredSess.Panes))
	}
}

func TestBuildCrashCheckpointPreservesMirrorRemoteRef(t *testing.T) {
	t.Parallel()

	sess := newSession("mirror-crash-checkpoint")
	defer stopSessionBackgroundLoops(t, sess)

	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	mustSessionMutation(t, sess, func(sess *Session) {
		pane := newProxyPane(1, mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(), sess.mirrorWriteOverride(1),
		)
		win := mux.NewWindow(pane, 80, 24)
		win.ID = 1
		win.Name = "window-1"
		seedSessionPanesForTest(sess, pane)
		sess.Windows = []*mux.Window{win}
		sess.ActiveWindowID = win.ID
		if err := sess.trackMirrorPane(pane, ref); err != nil {
			t.Fatalf("trackMirrorPane: %v", err)
		}
	})

	cp := sess.buildCrashCheckpoint()
	if cp == nil || len(cp.PaneStates) != 1 {
		t.Fatalf("crash checkpoint = %+v, want one pane state", cp)
	}
	if got := cp.PaneStates[0].RemoteRef; got == nil || *got != ref {
		t.Fatalf("RemoteRef = %+v, want %+v", got, ref)
	}
}

func TestCrashRestoreTracksMirrorRemoteRef(t *testing.T) {
	t.Parallel()

	sessionName := fmt.Sprintf("mirror-crash-restore-%d", time.Now().UnixNano())
	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	cp := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   sessionName,
		Counter:       1,
		WindowCounter: 1,
		Layout: proto.LayoutSnapshot{
			SessionName:  sessionName,
			Width:        80,
			Height:       24,
			ActivePaneID: 1,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 1,
			},
			Panes: []proto.PaneSnapshot{{
				ID:    1,
				Name:  "mirror",
				Host:  "remote",
				Color: config.AccentColor(0),
			}},
		},
		PaneStates: []checkpoint.CrashPaneState{{
			ID:        1,
			Meta:      mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)},
			Cols:      80,
			Rows:      23,
			Screen:    "cached remote screen",
			IsProxy:   true,
			RemoteRef: &ref,
		}},
		Timestamp: time.Now(),
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, "", mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	restoredSess := restored.firstSession()
	got, ok := restoredSess.mirror.RemoteRef(1)
	if !ok || got == nil || *got != ref {
		t.Fatalf("RemoteRef = (%+v, %v), want %+v true", got, ok, ref)
	}
}
