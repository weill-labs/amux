package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCommandSpawnAutoAppendsToShortestUnderfilledColumnAndEqualizes(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitPaneWithOptions(p1.ID, mux.SplitHorizontal, p2, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}
	if _, err := w.SplitRoot(mux.SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--name", "worker-4")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		p1X int
		p2X int
		p3X int
		p4X int
		p1H int
		p2H int
		p3H int
		p4H int
	} {
		w := sess.activeWindow()
		p4, err := sess.findPaneByRef("worker-4")
		if err != nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p4X int
				p1H int
				p2H int
				p3H int
				p4H int
			}{}
		}
		return struct {
			p1X int
			p2X int
			p3X int
			p4X int
			p1H int
			p2H int
			p3H int
			p4H int
		}{
			p1X: w.Root.FindPane(p1.ID).X,
			p2X: w.Root.FindPane(p2.ID).X,
			p3X: w.Root.FindPane(p3.ID).X,
			p4X: w.Root.FindPane(p4.ID).X,
			p1H: w.Root.FindPane(p1.ID).H,
			p2H: w.Root.FindPane(p2.ID).H,
			p3H: w.Root.FindPane(p3.ID).H,
			p4H: w.Root.FindPane(p4.ID).H,
		}
	})

	if state.p1X != state.p2X {
		t.Fatalf("auto spawn should keep pane-1 and pane-2 in the original column: %+v", state)
	}
	if state.p3X != state.p4X {
		t.Fatalf("auto spawn should place pane-3 and worker-4 in the shortest column: %+v", state)
	}
	if state.p1X == state.p4X {
		t.Fatalf("auto spawn should leave pane-1/pane-2 in a different column from worker-4: %+v", state)
	}
	if state.p3H != state.p4H {
		t.Fatalf("auto spawn should equalize row heights in the filled column: %+v", state)
	}
}

func TestCommandSpawnAutoRootSplitsWhenColumnsAreFull(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitPaneWithOptions(p1.ID, mux.SplitHorizontal, p2, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--name", "worker-3")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		p1X int
		p2X int
		p3X int
		p3H int
	} {
		w := sess.activeWindow()
		p3, err := sess.findPaneByRef("worker-3")
		if err != nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p3H int
			}{}
		}
		return struct {
			p1X int
			p2X int
			p3X int
			p3H int
		}{
			p1X: w.Root.FindPane(p1.ID).X,
			p2X: w.Root.FindPane(p2.ID).X,
			p3X: w.Root.FindPane(p3.ID).X,
			p3H: w.Root.FindPane(p3.ID).H,
		}
	})

	if state.p1X != state.p2X {
		t.Fatalf("pane-1 and pane-2 should remain stacked in the original column: %+v", state)
	}
	if state.p3X == state.p1X {
		t.Fatalf("worker-3 should be placed in a new root column: %+v", state)
	}
	if state.p3H != 23 {
		t.Fatalf("new root column should span the full window height, got %+v", state)
	}
}
