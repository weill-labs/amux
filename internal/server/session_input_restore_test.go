package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ckpt "github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestNewServerFromCheckpointWithScrollbackRefreshesInputTarget(t *testing.T) {
	// Not parallel: this test hands a live listener FD into a restored server and
	// the close lifecycle has flaked under concurrent package load.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-%d.sock", time.Now().UnixNano()))
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

	listenerFD, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}

	pane, layout := restoreTestLayout()
	cp := &ckpt.ServerCheckpoint{
		SessionName:   "restore-input-target",
		ListenerFd:    listenerFD,
		WindowCounter: 1,
		Layout:        layout,
		Panes: []ckpt.PaneCheckpoint{{
			ID:        pane.ID,
			Meta:      pane.Meta,
			PtmxFd:    -1,
			Cols:      80,
			Rows:      23,
			CreatedAt: time.Now(),
			IsProxy:   true,
		}},
	}

	srv, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	srv.sockPath = socketPath
	defer srv.Shutdown()

	assertSessionInputTarget(t, srv.firstSession(), pane.ID)
}

func TestNewServerFromCheckpointWithScrollbackPreservesManualBranchOverride(t *testing.T) {
	// Not parallel: this test hands a live listener FD into a restored server and
	// the close lifecycle has flaked under concurrent package load.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-%d.sock", time.Now().UnixNano()))
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

	listenerFD, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}

	pane, layout := restoreTestLayout()
	pane.Meta.GitBranch = "feat/manual"
	cp := &ckpt.ServerCheckpoint{
		SessionName:   "restore-manual-branch",
		ListenerFd:    listenerFD,
		WindowCounter: 1,
		Layout:        layout,
		Panes: []ckpt.PaneCheckpoint{{
			ID:           pane.ID,
			Meta:         pane.Meta,
			ManualBranch: true,
			PtmxFd:       -1,
			Cols:         80,
			Rows:         23,
			CreatedAt:    time.Now(),
			IsProxy:      true,
		}},
	}

	srv, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	srv.sockPath = socketPath
	defer srv.Shutdown()

	restored := srv.firstSession().findPaneByID(pane.ID)
	if restored == nil {
		t.Fatal("restored pane = nil")
	}

	restored.ApplyCwdBranch("/tmp/project", "auto-branch")
	if restored.LiveCwd() != "/tmp/project" {
		t.Fatalf("LiveCwd() = %q, want %q", restored.LiveCwd(), "/tmp/project")
	}
	if restored.Meta.GitBranch != "feat/manual" {
		t.Fatalf("GitBranch = %q, want manual override preserved", restored.Meta.GitBranch)
	}
}

func TestNewServerFromCheckpointWithScrollbackPreservesStartedAt(t *testing.T) {
	// Not parallel: this test hands a live listener FD into a restored server and
	// the close lifecycle has flaked under concurrent package load.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-%d.sock", time.Now().UnixNano()))
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

	listenerFD, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}

	pane, layout := restoreTestLayout()
	startedAt := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)
	cp := &ckpt.ServerCheckpoint{
		Version:       ckpt.ServerCheckpointVersion,
		SessionName:   "restore-started-at",
		StartedAt:     startedAt,
		ListenerFd:    listenerFD,
		WindowCounter: 1,
		Layout:        layout,
		Panes: []ckpt.PaneCheckpoint{{
			ID:        pane.ID,
			Meta:      pane.Meta,
			PtmxFd:    -1,
			Cols:      80,
			Rows:      23,
			CreatedAt: time.Now(),
			IsProxy:   true,
		}},
	}

	srv, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	srv.sockPath = socketPath
	defer srv.Shutdown()

	if got := srv.firstSession().startedAt; !got.Equal(startedAt) {
		t.Fatalf("session startedAt = %v, want %v", got, startedAt)
	}
}

func TestNewServerFromCrashCheckpointWithScrollbackRefreshesInputTarget(t *testing.T) {
	sessionName := fmt.Sprintf("crash-input-target-%d", time.Now().UnixNano())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pane, layout := restoreTestLayout()
	cp := &ckpt.CrashCheckpoint{
		Version:       ckpt.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        layout,
		PaneStates: []ckpt.CrashPaneState{{
			ID:        pane.ID,
			Meta:      pane.Meta,
			Cols:      80,
			Rows:      23,
			CreatedAt: time.Now(),
			IsProxy:   true,
		}},
		Timestamp: time.Now(),
	}

	crashPath := ckpt.CrashCheckpointPathTimestamped(sessionName, cp.Timestamp)
	srv, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, crashPath, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer srv.Shutdown()

	sess := srv.firstSession()
	assertSessionInputTarget(t, sess, pane.ID)
	if !sess.startedAt.Equal(cp.Timestamp) {
		t.Fatalf("session startedAt = %v, want %v", sess.startedAt, cp.Timestamp)
	}
}

func TestNewServerFromCrashCheckpointWithScrollbackPreservesManualBranchOverride(t *testing.T) {
	sessionName := fmt.Sprintf("crash-manual-branch-%d", time.Now().UnixNano())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pane, layout := restoreTestLayout()
	pane.Meta.GitBranch = "feat/manual"
	cp := &ckpt.CrashCheckpoint{
		Version:       ckpt.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        layout,
		PaneStates: []ckpt.CrashPaneState{{
			ID:           pane.ID,
			Meta:         pane.Meta,
			ManualBranch: true,
			Cols:         80,
			Rows:         23,
			CreatedAt:    time.Now(),
			IsProxy:      true,
		}},
		Timestamp: time.Now(),
	}

	crashPath := ckpt.CrashCheckpointPathTimestamped(sessionName, cp.Timestamp)
	srv, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, crashPath, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer srv.Shutdown()

	restored := srv.firstSession().findPaneByID(pane.ID)
	if restored == nil {
		t.Fatal("restored pane = nil")
	}

	restored.ApplyCwdBranch("/tmp/project", "auto-branch")
	if restored.LiveCwd() != "/tmp/project" {
		t.Fatalf("LiveCwd() = %q, want %q", restored.LiveCwd(), "/tmp/project")
	}
	if restored.Meta.GitBranch != "feat/manual" {
		t.Fatalf("GitBranch = %q, want manual override preserved", restored.Meta.GitBranch)
	}
}

func TestNewServerFromCrashCheckpointWithScrollbackPrimesVTIdleFromRecoveryTime(t *testing.T) {
	// Not parallel: uses t.Setenv for crash checkpoint paths, so it cannot call t.Parallel().
	sessionName := fmt.Sprintf("crash-vt-idle-%d", time.Now().UnixNano())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pane, layout := restoreTestLayout()
	restoreStart := time.Now()
	originalCreatedAt := restoreStart.Add(-time.Hour)
	cp := &ckpt.CrashCheckpoint{
		Version:       ckpt.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        layout,
		PaneStates: []ckpt.CrashPaneState{{
			ID:        pane.ID,
			Meta:      pane.Meta,
			Cols:      80,
			Rows:      23,
			CreatedAt: originalCreatedAt,
			Cwd:       t.TempDir(),
			IsProxy:   false,
		}},
		Timestamp: time.Now(),
	}

	crashPath := ckpt.CrashCheckpointPathTimestamped(sessionName, cp.Timestamp)
	srv, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, crashPath, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer srv.Shutdown()

	sess := srv.firstSession()
	restored := sess.findPaneByID(pane.ID)
	if restored == nil {
		t.Fatal("restored pane = nil")
	}
	if !restored.CreatedAt().Equal(originalCreatedAt) {
		t.Fatalf("CreatedAt() = %v, want %v", restored.CreatedAt(), originalCreatedAt)
	}

	early := sess.paneIdleStatus(restored.ID, restored.CreatedAt(), restoreStart.Add(time.Second))
	if early.idle {
		t.Fatal("vt-idle should still be settling from the fresh crash-recovery runtime")
	}

	settled := sess.paneIdleStatus(restored.ID, restored.CreatedAt(), restoreStart.Add(3*time.Second))
	if !settled.idle {
		t.Fatal("vt-idle should settle after the recovery-time grace window")
	}
}

func TestNewServerFromCrashCheckpointWithListenerErrorsWhenNoPanesRestore(t *testing.T) {
	t.Parallel()

	listener := &trackingListener{}
	sessionName := "crash-empty-restore"
	cp := &ckpt.CrashCheckpoint{
		Version:     ckpt.CrashVersion,
		SessionName: sessionName,
		Timestamp:   time.Now(),
	}

	srv, err := newServerFromCrashCheckpointWithListener(sessionName, listener, "/tmp/unused.sock", cp, "", mux.DefaultScrollbackLines)
	if err == nil {
		t.Fatal("newServerFromCrashCheckpointWithListener error = nil, want no panes restored")
	}
	if !strings.Contains(err.Error(), "no panes restored from crash checkpoint") {
		t.Fatalf("newServerFromCrashCheckpointWithListener error = %v, want no panes restored", err)
	}
	if srv != nil {
		t.Fatalf("newServerFromCrashCheckpointWithListener server = %+v, want nil", srv)
	}
	if !listener.closed {
		t.Fatal("listener should be closed when crash restore fails with no panes")
	}
}

func TestNewServerFromCheckpointWithScrollbackErrorsWhenAllPanesFailToRestore(t *testing.T) {
	// Not parallel: this test hands a live listener FD into a restored server and
	// the close lifecycle has flaked under concurrent package load.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-empty-%d.sock", time.Now().UnixNano()))
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

	listenerFD, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}

	pane, layout := restoreTestLayout()
	cp := &ckpt.ServerCheckpoint{
		SessionName:   "restore-empty",
		ListenerFd:    listenerFD,
		WindowCounter: 1,
		Layout:        layout,
		Panes: []ckpt.PaneCheckpoint{{
			ID:      pane.ID,
			Meta:    pane.Meta,
			PtmxFd:  -1, // FD -1 causes RestorePaneWithScrollback to return an error
			IsProxy: false,
		}},
	}

	srv, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err == nil {
		srv.Shutdown()
		t.Fatal("NewServerFromCheckpointWithScrollback error = nil, want no panes restored")
	}
	if !strings.Contains(err.Error(), "no panes restored from checkpoint") {
		t.Fatalf("NewServerFromCheckpointWithScrollback error = %v, want no panes restored", err)
	}
}

func TestNewServerFromCheckpointWithScrollbackUsesLegacySingleWindowFallback(t *testing.T) {
	// Not parallel: this test hands a live listener FD into a restored server and
	// the close lifecycle has flaked under concurrent package load.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-legacy-%d.sock", time.Now().UnixNano()))
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

	listenerFD, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}

	pane, _ := restoreTestLayout()
	// A layout without Windows set triggers the legacy single-window fallback path.
	cp := &ckpt.ServerCheckpoint{
		SessionName:   "restore-legacy",
		ListenerFd:    listenerFD,
		WindowCounter: 1,
		Layout: proto.LayoutSnapshot{
			SessionName:  "restore-legacy",
			Width:        80,
			Height:       23,
			ActivePaneID: pane.ID,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: pane.ID,
			},
			Panes: []proto.PaneSnapshot{{ID: pane.ID, Name: pane.Meta.Name, Host: pane.Meta.Host, Color: pane.Meta.Color}},
			// Windows intentionally omitted — exercises legacy RebuildFromSnapshot path.
		},
		Panes: []ckpt.PaneCheckpoint{{
			ID:      pane.ID,
			Meta:    pane.Meta,
			PtmxFd:  -1,
			Cols:    80,
			Rows:    23,
			IsProxy: true,
		}},
	}

	srv, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	srv.sockPath = socketPath
	defer srv.Shutdown()

	sess := srv.firstSession()
	if len(sess.Windows) != 1 {
		t.Fatalf("Windows = %d, want 1 window from legacy fallback", len(sess.Windows))
	}
}

func TestNewServerFromCrashCheckpointWithListenerUsesLegacySingleWindowFallback(t *testing.T) {
	t.Parallel()

	pane, _ := restoreTestLayout()
	sessionName := "crash-legacy-window"
	// A layout without Windows set triggers the legacy single-window fallback path.
	cp := &ckpt.CrashCheckpoint{
		Version:       ckpt.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 0,
		Layout: proto.LayoutSnapshot{
			SessionName:  sessionName,
			Width:        80,
			Height:       23,
			ActivePaneID: pane.ID,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: pane.ID,
			},
			Panes: []proto.PaneSnapshot{{ID: pane.ID, Name: pane.Meta.Name, Host: pane.Meta.Host, Color: pane.Meta.Color}},
			// Windows intentionally omitted — exercises legacy RebuildFromSnapshot path.
		},
		PaneStates: []ckpt.CrashPaneState{{
			ID:      pane.ID,
			Meta:    pane.Meta,
			Cols:    80,
			Rows:    23,
			IsProxy: true,
		}},
		Timestamp: time.Now(),
	}

	listener := &trackingListener{}
	srv, err := newServerFromCrashCheckpointWithListener(sessionName, listener, "/tmp/unused.sock", cp, "", mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("newServerFromCrashCheckpointWithListener: %v", err)
	}
	defer srv.Shutdown()

	if len(srv.firstSession().Windows) != 1 {
		t.Fatalf("Windows = %d, want 1 window from legacy fallback", len(srv.firstSession().Windows))
	}
}

func restoreTestLayout() (*mux.Pane, proto.LayoutSnapshot) {
	pane := mux.NewProxyPaneWithScrollback(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})

	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"

	return pane, proto.LayoutSnapshot{
		SessionName:    "restore-test",
		Width:          80,
		Height:         23,
		ActiveWindowID: w.ID,
		Windows:        []proto.WindowSnapshot{w.SnapshotWindow(0)},
	}
}

func assertSessionInputTarget(t *testing.T, sess *Session, paneID uint32) {
	t.Helper()
	if sess == nil {
		t.Fatal("session is nil")
	}
	target := sess.inputTargetPane()
	if target == nil {
		t.Fatal("input target = nil, want active pane")
	}
	if target.ID != paneID {
		t.Fatalf("input target pane = %d, want %d", target.ID, paneID)
	}
}

type trackingListener struct {
	closed bool
}

func (l *trackingListener) Accept() (net.Conn, error) {
	return nil, errors.New("unexpected Accept call")
}

func (l *trackingListener) Close() error {
	l.closed = true
	return nil
}

func (l *trackingListener) Addr() net.Addr {
	return trackingAddr("tracking")
}

type trackingAddr string

func (a trackingAddr) Network() string {
	return "tracking"
}

func (a trackingAddr) String() string {
	return string(a)
}
