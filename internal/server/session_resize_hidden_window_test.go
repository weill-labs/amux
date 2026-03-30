package server

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestResizeClientDefersHiddenWindowRedrawUntilSelected(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	dir := t.TempDir()
	activeSignalFile := dir + "/active.winch"
	activeReadyFile := dir + "/active.ready"
	hiddenSignalFile := dir + "/hidden.winch"
	hiddenReadyFile := dir + "/hidden.ready"

	p1 := newResizeSignalSessionPane(t, sess, srv, activeSignalFile, activeReadyFile)
	p2 := newResizeSignalSessionPane(t, sess, srv, hiddenSignalFile, hiddenReadyFile)

	w1 := mux.NewWindow(p1, 80, 23)
	w1.ID = 1
	w1.Name = "window-1"

	w2 := mux.NewWindow(p2, 80, 23)
	w2.ID = 2
	w2.Name = "window-2"

	setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1, w2}, p1, p2)

	cc := newClientConn(discardConn{})
	cc.ID = "client-1"
	cc.cols = 80
	cc.rows = 24
	t.Cleanup(cc.Close)

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(cc)
		sess.ensureClientManager().setSizeOwnerForTest(cc)
	})

	beforeResize := sess.generation.Load()
	sess.enqueueResizeClient(cc, 100, 30)
	if _, ok := sess.waitGeneration(beforeResize, time.Second); !ok {
		t.Fatal("timed out waiting for resize layout generation")
	}

	waitUntil(t, func() bool {
		return winchSignalCount(t, activeSignalFile) >= 1
	})

	if waitForWinchSignal(t, hiddenSignalFile, 250*time.Millisecond) {
		t.Fatalf("hidden window redraw signals after resize = %d, want 0", winchSignalCount(t, hiddenSignalFile))
	}

	res := runTestCommand(t, srv, sess, "select-window", "2")
	if res.cmdErr != "" {
		t.Fatalf("select-window error: %s", res.cmdErr)
	}

	waitUntil(t, func() bool {
		return winchSignalCount(t, hiddenSignalFile) >= 1
	})
}

func newResizeSignalSessionPane(t *testing.T, sess *Session, srv *Server, signalFile, readyFile string) *mux.Pane {
	t.Helper()

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 not available: %v", err)
	}

	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		return sess.createPaneWithMeta(srv, mux.PaneMeta{}, 80, 23)
	})
	if err != nil {
		t.Fatalf("createPaneWithMeta: %v", err)
	}
	pane.Start()

	python := `import os, signal, threading; path = os.environ["SIGNAL_FILE"]; open(os.environ["READY_FILE"], "w").write("ready"); signal.signal(signal.SIGWINCH, lambda *_: open(path, "a").write("x")); threading.Event().wait()`
	cmd := "SIGNAL_FILE=" + strconv.Quote(signalFile) +
		" READY_FILE=" + strconv.Quote(readyFile) +
		" " + strconv.Quote(pythonPath) +
		" -u -c " + strconv.Quote(python) + "\n"
	if _, err := pane.Write([]byte(cmd)); err != nil {
		t.Fatalf("pane.Write python command: %v", err)
	}

	waitUntil(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	})

	return pane
}

func winchSignalCount(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return len(data)
}

func waitForWinchSignal(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()

	if winchSignalCount(t, path) > 0 {
		return true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return false
		case <-ticker.C:
			if winchSignalCount(t, path) > 0 {
				return true
			}
		}
	}
}
