package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestMovePaneToWindowCommandUsageAndErrors(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w1 := newTestWindowWithPanes(t, sess, 1, "editor", p1)
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p2)
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = append(w1.Panes(), w2.Panes()...)

	if got := runTestCommand(t, srv, sess, "move-pane-to-window", "pane-1").cmdErr; got != "usage: move-pane-to-window <pane> <window>" {
		t.Fatalf("usage error = %q", got)
	}
	if got := runTestCommand(t, srv, sess, "move-pane-to-window", "pane-1", "missing").cmdErr; got != `window "missing" not found` {
		t.Fatalf("missing window error = %q", got)
	}
	if got := runTestCommand(t, srv, sess, "move-pane-to-window", "missing", "2").cmdErr; got != `pane "missing" not found` {
		t.Fatalf("missing pane error = %q", got)
	}
}

func TestMovePaneToWindowCommandMovesPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w1 := newTestWindowWithPanes(t, sess, 1, "editor", p1, p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p3)
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = append(w1.Panes(), w2.Panes()...)

	res := runTestCommand(t, srv, sess, "move-pane-to-window", "pane-1", "2")
	if res.cmdErr != "" {
		t.Fatalf("move-pane-to-window error = %q", res.cmdErr)
	}
	if got := w1.Root.FindPane(1); got != nil {
		t.Fatal("source window should no longer contain pane-1")
	}
	if got := w2.Root.FindPane(1); got == nil {
		t.Fatal("target window should contain moved pane-1")
	}
}

func TestDropPaneMovesPaneAcrossWindowsBeforePlacement(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sourcePane := newTestPane(sess, 1, "pane-1")
	sourcePeer := newTestPane(sess, 2, "pane-2")
	targetPane := newTestPane(sess, 3, "pane-3")

	source := newTestWindowWithPanes(t, sess, 1, "editor", sourcePane, sourcePeer)
	target := newTestWindowWithPanes(t, sess, 2, "logs", targetPane)
	sess.Windows = []*mux.Window{source, target}
	sess.ActiveWindowID = target.ID
	sess.Panes = append(source.Panes(), target.Panes()...)

	res := runTestCommandWithActor(t, srv, sess, targetPane.ID, "drop-pane", "pane-1", "root", "left")
	if res.cmdErr != "" {
		t.Fatalf("drop-pane error = %q", res.cmdErr)
	}
	if got := source.Root.FindPane(1); got != nil {
		t.Fatal("source window should no longer contain pane-1")
	}
	if got := target.Root.FindPane(1); got == nil {
		t.Fatal("target window should contain moved pane-1 after cross-window drop")
	}
}
