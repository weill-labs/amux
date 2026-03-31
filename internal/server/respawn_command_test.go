package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestRespawnCommandRestartsLocalPaneInPlace(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	startDir := t.TempDir()
	nextDir := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "starts")
	shellPath := writeRespawnTestShell(t, markerFile)

	meta := mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
		Task:  "build",
		KV:    map[string]string{"owner": "lab-593"},
		Dir:   startDir,
	}

	pane := func() *mux.Pane {
		restoreShell := withShellForTest(t, shellPath)
		defer restoreShell()
		return mustCreatePaneWithMeta(t, sess, srv, meta, 80, 23)
	}()
	pane.Start()
	waitForMarkerCount(t, markerFile, 1)

	window := newTestWindowWithPanes(t, sess, 1, "main", pane)

	serverConn, peerConn := net.Pipe()
	cc := newClientConn(serverConn)
	cc.ID = "client-1"
	defer cc.Close()
	defer peerConn.Close()

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane}
		sess.ensureClientManager().setClientsForTest(cc)
	})

	pane.SetRetainedHistory([]string{"base-1", "base-2"})
	pane.FeedOutput([]byte("stale-screen"))
	pane.ApplyCwdBranch(nextDir, "feat/respawn")

	oldPID := pane.ProcessPid()
	if oldPID == 0 {
		t.Fatal("old process pid = 0, want live shell")
	}

	res := runTestCommand(t, srv, sess, "respawn", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("respawn cmdErr = %q", res.cmdErr)
	}
	if !strings.Contains(res.output, "Respawned pane-1") {
		t.Fatalf("respawn output = %q, want confirmation", res.output)
	}

	msg := readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneHistory {
		t.Fatalf("first broadcast type = %v, want pane history", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("history pane id = %d, want %d", msg.PaneID, pane.ID)
	}
	if len(msg.History) != 0 {
		t.Fatalf("history after respawn = %#v, want empty", msg.History)
	}

	msg = readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("second broadcast type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("pane output id = %d, want %d", msg.PaneID, pane.ID)
	}

	emu := mux.NewVTEmulatorWithDrainAndScrollback(80, 22, mux.DefaultScrollbackLines)
	if _, err := emu.Write(msg.PaneData); err != nil {
		t.Fatalf("emulator write: %v", err)
	}
	if got := mux.EmulatorContentLines(emu); len(got) != 22 || got[0] != "" {
		t.Fatalf("broadcast respawn screen = %#v, want blank rows", got)
	}
	waitForMarkerCount(t, markerFile, 2)

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		pane        *mux.Pane
		windowPane  *mux.Pane
		history     []string
		content     []string
		activeID    uint32
		zoomedID    uint32
		leadID      uint32
		liveCwd     string
		task        string
		color       string
		kvOwner     string
		processPID  int
		emulatorCol int
		emulatorRow int
	} {
		w := sess.activeWindow()
		cell := w.Root.FindPane(pane.ID)
		p := sess.findPaneByID(pane.ID)
		cols, rows := p.EmulatorSize()
		return struct {
			pane        *mux.Pane
			windowPane  *mux.Pane
			history     []string
			content     []string
			activeID    uint32
			zoomedID    uint32
			leadID      uint32
			liveCwd     string
			task        string
			color       string
			kvOwner     string
			processPID  int
			emulatorCol int
			emulatorRow int
		}{
			pane:        p,
			windowPane:  cell.Pane,
			history:     p.ScrollbackLines(),
			content:     p.ContentLines(),
			activeID:    w.ActivePane.ID,
			zoomedID:    w.ZoomedPaneID,
			leadID:      w.LeadPaneID,
			liveCwd:     p.LiveCwd(),
			task:        p.Meta.Task,
			color:       p.Meta.Color,
			kvOwner:     p.Meta.KV["owner"],
			processPID:  p.ProcessPid(),
			emulatorCol: cols,
			emulatorRow: rows,
		}
	})

	if state.pane == nil {
		t.Fatal("respawned pane missing from session")
	}
	if state.pane == pane {
		t.Fatal("session should replace the old pane pointer")
	}
	if state.windowPane != state.pane {
		t.Fatal("window layout should point at the respawned pane")
	}
	if state.processPID == 0 || state.processPID == oldPID {
		t.Fatalf("respawned process pid = %d, want non-zero new pid (old %d)", state.processPID, oldPID)
	}
	if state.activeID != pane.ID {
		t.Fatalf("active pane id = %d, want %d", state.activeID, pane.ID)
	}
	if state.zoomedID != 0 {
		t.Fatalf("zoomed pane id = %d, want 0", state.zoomedID)
	}
	if state.leadID != 0 {
		t.Fatalf("lead pane id = %d, want 0", state.leadID)
	}
	if state.task != meta.Task || state.color != meta.Color || state.kvOwner != "lab-593" {
		t.Fatalf("respawned metadata = task %q color %q kv owner %q", state.task, state.color, state.kvOwner)
	}
	if state.liveCwd != nextDir {
		t.Fatalf("live cwd after respawn = %q, want %q", state.liveCwd, nextDir)
	}
	if len(state.history) != 0 {
		t.Fatalf("respawned scrollback = %#v, want empty", state.history)
	}
	if len(state.content) == 0 || strings.TrimSpace(state.content[0]) != "" {
		t.Fatalf("respawned content = %#v, want blank screen", state.content)
	}
	if state.emulatorCol != 80 || state.emulatorRow != 23 {
		t.Fatalf("respawned emulator size = %dx%d, want 80x23", state.emulatorCol, state.emulatorRow)
	}

	waitForPaneCwd(t, state.pane, nextDir)
	waitForProcessExit(t, oldPID)
}

func TestRespawnCommandRejectsProxyPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "respawn", "pane-1")
	if got := res.cmdErr; got != "cannot respawn proxy pane" {
		t.Fatalf("respawn proxy pane error = %q, want %q", got, "cannot respawn proxy pane")
	}
}

func mustCreatePaneWithMeta(t *testing.T, sess *Session, srv *Server, meta mux.PaneMeta, cols, rows int) *mux.Pane {
	t.Helper()

	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		return sess.createPaneWithMeta(srv, meta, cols, rows)
	})
	if err != nil {
		t.Fatalf("enqueueSessionQuery(createPaneWithMeta): %v", err)
	}
	return pane
}

func writeRespawnTestShell(t *testing.T, markerFile string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "respawn-test-shell.sh")
	script := "#!/bin/sh\n" +
		"printf x >> " + strconv.Quote(markerFile) + "\n" +
		"while [ \"$1\" = \"-l\" ]; do\n\tshift\n" +
		"done\n" +
		"while IFS= read -r line; do\n\teval \"$line\"\n" +
		"done\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

func withShellForTest(t *testing.T, shellPath string) func() {
	t.Helper()

	previous, hadPrevious := os.LookupEnv("SHELL")
	if err := os.Setenv("SHELL", shellPath); err != nil {
		t.Fatalf("Setenv(SHELL): %v", err)
	}
	return func() {
		var err error
		if hadPrevious {
			err = os.Setenv("SHELL", previous)
		} else {
			err = os.Unsetenv("SHELL")
		}
		if err != nil {
			t.Fatalf("restore SHELL: %v", err)
		}
	}
}

func waitForMarkerCount(t *testing.T, path string, want int) {
	t.Helper()

	waitUntilRespawn(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && len(bytes.TrimSpace(data)) >= want
	})
}

func waitForFileString(t *testing.T, path, want string) {
	t.Helper()

	waitUntilRespawn(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.TrimSpace(string(data)) == want
	})
}

func waitForPaneCwd(t *testing.T, pane *mux.Pane, want string) {
	t.Helper()

	waitUntilRespawn(t, 5*time.Second, func() bool {
		cwd, _ := pane.DetectCwdBranch()
		return cwd == want
	})
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()

	waitUntilRespawn(t, 5*time.Second, func() bool {
		return syscall.Kill(pid, 0) != nil
	})
}

func waitUntilRespawn(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	if cond() {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("timed out waiting for condition")
		case <-ticker.C:
			if cond() {
				return
			}
		}
	}
}
