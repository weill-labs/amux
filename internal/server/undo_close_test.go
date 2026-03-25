package server

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// waitUndoStackEmpty polls the session event loop until closedPanes is empty
// or the deadline expires.
func waitUndoStackEmpty(t *testing.T, sess *Session, deadline time.Duration) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		count := mustSessionQuery(t, sess, func(sess *Session) int {
			return len(sess.closedPanes)
		})
		if count == 0 {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("closedPanes not empty after %v", deadline)
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
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2}

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
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1}

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
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2}

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
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2, pane3}

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
	sess.UndoGracePeriod = 50 * time.Millisecond
	defer cleanup()

	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newTestPane(sess, 2, "pane-2")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane1, pane2}

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
