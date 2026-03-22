package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	ckpt "github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestNewServerFromCheckpointWithScrollbackRefreshesInputTarget(t *testing.T) {
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
