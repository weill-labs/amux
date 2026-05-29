package server

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

// waitUndoStackEmpty polls the session event loop until the undo stack is empty
// or the deadline expires.
func waitUndoStackEmpty(t *testing.T, sess *Session, deadline time.Duration) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		count := mustSessionQuery(t, sess, func(sess *Session) int {
			return sess.ensureUndoManager().closedPaneCount()
		})
		if count == 0 {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("undo stack not empty after %v", deadline)
		default:
		}
	}
}

func TestUndoClosePaneUnit(t *testing.T) {
	t.Parallel()

	t.Run("kill then undo restores pane", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane1 := newTestPane(sess, 1, "pane-1")
		pane2 := newTestPane(sess, 2, "pane-2")
		window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
		setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

		res := runTestCommand(t, srv, sess, "kill", "pane-2")
		if res.cmdErr != "" || !strings.Contains(res.output, "Killed pane-2") {
			t.Fatalf("kill result = %#v", res)
		}

		state := mustSessionQuery(t, sess, func(sess *Session) int {
			return len(sess.Panes)
		})
		if state != 1 {
			t.Fatalf("pane count after kill = %d, want 1", state)
		}

		res = runTestCommand(t, srv, sess, "undo")
		if res.cmdErr != "" || !strings.Contains(res.output, "pane-2") {
			t.Fatalf("undo result = %#v", res)
		}

		state = mustSessionQuery(t, sess, func(sess *Session) int {
			return len(sess.Panes)
		})
		if state != 2 {
			t.Fatalf("pane count after undo = %d, want 2", state)
		}
	})

	t.Run("undo with no closed panes returns error", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane1 := newTestPane(sess, 1, "pane-1")
		window := newTestWindowWithPanes(t, sess, 1, "main", pane1)
		setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1)

		res := runTestCommand(t, srv, sess, "undo")
		if res.cmdErr == "" || !strings.Contains(res.cmdErr, "no closed pane") {
			t.Fatalf("undo with nothing should error, got output=%q err=%q", res.output, res.cmdErr)
		}
	})

	t.Run("pane exit during grace period finalizes", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane1 := newTestPane(sess, 1, "pane-1")
		pane2 := newTestPane(sess, 2, "pane-2")
		window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
		setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

		res := runTestCommand(t, srv, sess, "kill", "pane-2")
		if res.cmdErr != "" {
			t.Fatalf("kill error: %s", res.cmdErr)
		}

		// Enqueue exit and fence through the event loop.
		sess.enqueuePaneExit(pane2.ID, "exited")
		waitUndoStackEmpty(t, sess, 2*time.Second)

		res = runTestCommand(t, srv, sess, "undo")
		if res.cmdErr == "" || !strings.Contains(res.cmdErr, "no closed pane") {
			t.Fatalf("undo after pane exit should fail, got output=%q err=%q", res.output, res.cmdErr)
		}
	})

	t.Run("multiple kill then undo pops in LIFO order", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane1 := newTestPane(sess, 1, "pane-1")
		pane2 := newTestPane(sess, 2, "pane-2")
		pane3 := newTestPane(sess, 3, "pane-3")
		window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2, pane3)
		setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2, pane3)

		runTestCommand(t, srv, sess, "kill", "pane-2")
		runTestCommand(t, srv, sess, "kill", "pane-3")

		res := runTestCommand(t, srv, sess, "undo")
		if !strings.Contains(res.output, "pane-3") {
			t.Fatalf("first undo should restore pane-3, got: %s", res.output)
		}

		res = runTestCommand(t, srv, sess, "undo")
		if !strings.Contains(res.output, "pane-2") {
			t.Fatalf("second undo should restore pane-2, got: %s", res.output)
		}
	})
}

func TestUndoGracePeriodExpiry(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.undo = newUndoManager(undoManagerConfig{gracePeriod: 50 * time.Millisecond})
	})
	defer cleanup()

	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newTestPane(sess, 2, "pane-2")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	res := runTestCommand(t, srv, sess, "kill", "pane-2")
	if res.cmdErr != "" {
		t.Fatalf("kill error: %s", res.cmdErr)
	}

	// Wait for the grace period timer to fire and the event loop to process it.
	waitUndoStackEmpty(t, sess, 2*time.Second)

	res = runTestCommand(t, srv, sess, "undo")
	if res.cmdErr == "" || !strings.Contains(res.cmdErr, "no closed pane") {
		t.Fatalf("undo after expiry should fail, got output=%q err=%q", res.output, res.cmdErr)
	}
}

func TestSoftClosePaneDetachesMirrorDuringUndoWindow(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newProxyPane(2, mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)}, 80, 23,
		sess.paneOutputCallback(), sess.paneExitCallback(), sess.mirrorWriteOverride(2),
	)
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	mustSessionMutation(t, sess, func(sess *Session) {
		if err := sess.trackMirrorPane(pane2, ref); err != nil {
			t.Fatalf("trackMirrorPane: %v", err)
		}
		sess.softClosePane(pane2.ID)
	})

	got := mustSessionQuery(t, sess, func(sess *Session) struct {
		snap mirrorpkg.Snapshot
		ok   bool
	} {
		snap, ok := sess.mirror.Snapshot(pane2.ID)
		return struct {
			snap mirrorpkg.Snapshot
			ok   bool
		}{snap: snap, ok: ok}
	})
	if !got.ok {
		t.Fatal("mirror snapshot missing")
	}
	snap := got.snap
	if snap.State != mirrorpkg.StateDetached {
		t.Fatalf("mirror state after soft close = %s, want %s", snap.State, mirrorpkg.StateDetached)
	}
}

func TestUndoClosePaneRetracksSoftClosedMirror(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newProxyPane(2, mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)}, 80, 23,
		sess.paneOutputCallback(), sess.paneExitCallback(), sess.mirrorWriteOverride(2),
	)
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	mustSessionMutation(t, sess, func(sess *Session) {
		if err := sess.trackMirrorPane(pane2, ref); err != nil {
			t.Fatalf("trackMirrorPane: %v", err)
		}
		sess.softClosePane(pane2.ID)
		if _, err := sess.undoClosePane(); err != nil {
			t.Fatalf("undoClosePane: %v", err)
		}
	})

	got := mustSessionQuery(t, sess, func(sess *Session) struct {
		snap mirrorpkg.Snapshot
		ok   bool
	} {
		snap, ok := sess.mirror.Snapshot(pane2.ID)
		return struct {
			snap mirrorpkg.Snapshot
			ok   bool
		}{snap: snap, ok: ok}
	})
	if !got.ok {
		t.Fatal("mirror snapshot missing")
	}
	snap := got.snap
	if snap.State == mirrorpkg.StateDetached {
		t.Fatalf("mirror state after undo = %s, want retracked mirror", snap.State)
	}
	if snap.RemoteRef != ref {
		t.Fatalf("RemoteRef = %+v, want %+v", snap.RemoteRef, ref)
	}
}
