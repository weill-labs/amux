package main

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
)

func TestPrependReloadExecPathArgIncludesResolvedExecutable(t *testing.T) {
	t.Parallel()

	wantPath, err := reload.ResolveExecutable()
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}

	got := prependReloadExecPathArg(reload.ResolveExecutable, []string{"reload-server"})
	if len(got) != 3 {
		t.Fatalf("len(prependReloadExecPathArg) = %d, want 3", len(got))
	}
	if got[0] != server.ReloadServerExecPathFlag {
		t.Fatalf("flag = %q, want %q", got[0], server.ReloadServerExecPathFlag)
	}
	if got[1] != wantPath {
		t.Fatalf("exec path = %q, want %q", got[1], wantPath)
	}
	if got[2] != "reload-server" {
		t.Fatalf("trailing args = %v, want [reload-server]", got[2:])
	}
}

func TestPrependReloadExecPathArgLeavesArgsUnchangedOnResolverError(t *testing.T) {
	t.Parallel()

	args := []string{"reload-server"}
	got := prependReloadExecPathArg(func() (string, error) {
		return "", errors.New("boom")
	}, args)
	if len(got) != 1 || got[0] != "reload-server" {
		t.Fatalf("prependReloadExecPathArg() = %v, want %v", got, args)
	}
}

func TestMainCheckpointReloadStartsServerWithoutSubcommand(t *testing.T) {
	t.Parallel()

	cmd := newHermeticMainCmd(t)
	cmd.Env = append(cmd.Env, "AMUX_CHECKPOINT=/definitely/missing")

	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}

	output := string(out)
	if !strings.Contains(output, `"event":"checkpoint_restore"`) || !strings.Contains(output, `"msg":"reading reload checkpoint failed"`) {
		t.Fatalf("expected checkpoint reload to route into server startup, got:\n%s", output)
	}
	if strings.Contains(output, "amux: server not running") {
		t.Fatalf("checkpoint reload should not fall back to client attach path:\n%s", output)
	}
}

func TestRestoreServerFromReloadCheckpointFallsBackToCrashCheckpoint(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	socketPath := filepath.Join(os.TempDir(), "amux-main-restore.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.UnixListener", listener)
	}
	listenerFile, err := unixListener.File()
	if err != nil {
		t.Fatalf("(*net.UnixListener).File(): %v", err)
	}
	defer listenerFile.Close()

	sessionName := "reload-fallback"
	reloadCPPath, err := checkpoint.Write(&checkpoint.ServerCheckpoint{
		Version:     checkpoint.ServerCheckpointVersion - 1,
		SessionName: sessionName,
		ListenerFd:  int(listenerFile.Fd()),
	})
	if err != nil {
		t.Fatalf("checkpoint.Write: %v", err)
	}

	crashTimestamp := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)
	crashPath := checkpoint.CrashCheckpointPathTimestamped(sessionName, crashTimestamp)
	crashCP := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion - 1,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        restoreFallbackLayout(sessionName),
		PaneStates: []checkpoint.CrashPaneState{{
			ID:      1,
			Meta:    mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"},
			Cols:    80,
			Rows:    23,
			IsProxy: true,
		}},
		Timestamp: crashTimestamp,
	}
	if err := checkpoint.WriteCrash(crashCP, sessionName, crashTimestamp); err != nil {
		t.Fatalf("checkpoint.WriteCrash: %v", err)
	}

	stderr := captureStderr(t, func() {
		srv, err := restoreServerFromReloadCheckpoint(sessionName, reloadCPPath, mux.DefaultScrollbackLines)
		if err != nil {
			t.Fatalf("restoreServerFromReloadCheckpoint: %v", err)
		}
		srv.Shutdown()
	})
	if !strings.Contains(stderr, `"event":"checkpoint_restore_fallback"`) || !strings.Contains(stderr, `"msg":"reload checkpoint incompatible; falling back to crash checkpoint"`) {
		t.Fatalf("stderr = %q, want crash fallback log", stderr)
	}

	if _, statErr := os.Stat(crashPath); !os.IsNotExist(statErr) {
		t.Fatalf("crash checkpoint should be removed after fallback restore, err=%v", statErr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = orig
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing stderr writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing stderr reader: %v", err)
	}
	return string(data)
}

func TestRestoreServerFromReloadCheckpointErrorsWithoutCrashFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	socketPath := filepath.Join(os.TempDir(), "amux-main-restore-missing.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.UnixListener", listener)
	}
	listenerFile, err := unixListener.File()
	if err != nil {
		t.Fatalf("(*net.UnixListener).File(): %v", err)
	}
	defer listenerFile.Close()

	reloadCPPath, err := checkpoint.Write(&checkpoint.ServerCheckpoint{
		Version:     checkpoint.ServerCheckpointVersion - 1,
		SessionName: "reload-missing-crash",
		ListenerFd:  int(listenerFile.Fd()),
	})
	if err != nil {
		t.Fatalf("checkpoint.Write: %v", err)
	}

	_, err = restoreServerFromReloadCheckpoint("reload-missing-crash", reloadCPPath, mux.DefaultScrollbackLines)
	if err == nil {
		t.Fatal("restoreServerFromReloadCheckpoint error = nil, want missing crash fallback")
	}
	if !strings.Contains(err.Error(), "no crash checkpoint fallback found") {
		t.Fatalf("restoreServerFromReloadCheckpoint error = %v, want missing crash fallback", err)
	}
}

func TestRestoreServerFromReloadCheckpointErrorsWhenCrashFallbackUnreadable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	socketPath := filepath.Join(os.TempDir(), "amux-main-restore-corrupt.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.UnixListener", listener)
	}
	listenerFile, err := unixListener.File()
	if err != nil {
		t.Fatalf("(*net.UnixListener).File(): %v", err)
	}
	defer listenerFile.Close()

	sessionName := "reload-corrupt-crash"
	reloadCPPath, err := checkpoint.Write(&checkpoint.ServerCheckpoint{
		Version:     checkpoint.ServerCheckpointVersion - 1,
		SessionName: sessionName,
		ListenerFd:  int(listenerFile.Fd()),
	})
	if err != nil {
		t.Fatalf("checkpoint.Write: %v", err)
	}

	crashTimestamp := time.Date(2026, time.March, 27, 13, 0, 0, 0, time.UTC)
	crashPath := checkpoint.CrashCheckpointPathTimestamped(sessionName, crashTimestamp)
	if err := os.MkdirAll(filepath.Dir(crashPath), 0700); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", filepath.Dir(crashPath), err)
	}
	if err := os.WriteFile(crashPath, []byte(`{"version":1,"session_name":`), 0600); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", crashPath, err)
	}

	_, err = restoreServerFromReloadCheckpoint(sessionName, reloadCPPath, mux.DefaultScrollbackLines)
	if err == nil {
		t.Fatal("restoreServerFromReloadCheckpoint error = nil, want unreadable crash fallback")
	}
	if !strings.Contains(err.Error(), crashPath) || !strings.Contains(err.Error(), "decoding crash checkpoint") {
		t.Fatalf("restoreServerFromReloadCheckpoint error = %v, want crash decode context", err)
	}
}

func TestRestoreServerFromReloadCheckpointRejectsInvalidFallbackListenerFD(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sessionName := "reload-invalid-listener"
	reloadCPPath, err := checkpoint.Write(&checkpoint.ServerCheckpoint{
		Version:     checkpoint.ServerCheckpointVersion - 1,
		SessionName: sessionName,
		ListenerFd:  0,
	})
	if err != nil {
		t.Fatalf("checkpoint.Write: %v", err)
	}

	crashTimestamp := time.Date(2026, time.March, 27, 14, 0, 0, 0, time.UTC)
	if err := checkpoint.WriteCrash(&checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        restoreFallbackLayout(sessionName),
		PaneStates: []checkpoint.CrashPaneState{{
			ID:      1,
			Meta:    mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"},
			Cols:    80,
			Rows:    23,
			IsProxy: true,
		}},
		Timestamp: crashTimestamp,
	}, sessionName, crashTimestamp); err != nil {
		t.Fatalf("checkpoint.WriteCrash: %v", err)
	}

	_, err = restoreServerFromReloadCheckpoint(sessionName, reloadCPPath, mux.DefaultScrollbackLines)
	if err == nil {
		t.Fatal("restoreServerFromReloadCheckpoint error = nil, want invalid listener fd")
	}
	if !strings.Contains(err.Error(), "invalid listener fd 0 in reload checkpoint") {
		t.Fatalf("restoreServerFromReloadCheckpoint error = %v, want invalid listener fd context", err)
	}
}

func restoreFallbackLayout(sessionName string) proto.LayoutSnapshot {
	return proto.LayoutSnapshot{
		SessionName:    sessionName,
		Width:          80,
		Height:         23,
		ActiveWindowID: 1,
		Windows: []proto.WindowSnapshot{{
			ID:           1,
			Name:         "window-1",
			Index:        1,
			ActivePaneID: 1,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 1,
			},
			Panes: []proto.PaneSnapshot{{
				ID:    1,
				Name:  "pane-1",
				Host:  mux.DefaultHost,
				Color: "f5e0dc",
			}},
		}},
	}
}
