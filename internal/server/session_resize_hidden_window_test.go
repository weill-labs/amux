package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestResizeClientDefersHiddenWindowResizeUntilSelected(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	if err := p1.Resize(80, 22); err != nil {
		t.Fatalf("Resize pane-1: %v", err)
	}

	p2 := newTestPane(sess, 2, "pane-2")
	if err := p2.Resize(80, 22); err != nil {
		t.Fatalf("Resize pane-2: %v", err)
	}

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

	assertPaneEmulatorSize(t, p1, 100, 28)
	assertPaneEmulatorSize(t, p2, 80, 22)
	assertWindowSize(t, sess, w1.ID, 100, 29)
	assertWindowSize(t, sess, w2.ID, 80, 23)

	res := runTestCommand(t, srv, sess, "select-window", "2")
	if res.cmdErr != "" {
		t.Fatalf("select-window error: %s", res.cmdErr)
	}

	assertPaneEmulatorSize(t, p2, 100, 28)
	assertWindowSize(t, sess, w2.ID, 100, 29)
}

func assertPaneEmulatorSize(t *testing.T, pane *mux.Pane, wantCols, wantRows int) {
	t.Helper()

	if pane == nil {
		t.Fatal("pane is nil")
	}
	gotCols, gotRows := pane.EmulatorSize()
	if gotCols != wantCols || gotRows != wantRows {
		t.Fatalf("pane %s emulator size = %dx%d, want %dx%d", pane.Meta.Name, gotCols, gotRows, wantCols, wantRows)
	}
}

func assertWindowSize(t *testing.T, sess *Session, windowID uint32, wantWidth, wantHeight int) {
	t.Helper()

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		width  int
		height int
		ok     bool
	} {
		for _, w := range sess.Windows {
			if w.ID == windowID {
				return struct {
					width  int
					height int
					ok     bool
				}{
					width:  w.Width,
					height: w.Height,
					ok:     true,
				}
			}
		}
		return struct {
			width  int
			height int
			ok     bool
		}{}
	})
	if !state.ok {
		t.Fatalf("window %d not found", windowID)
	}
	if state.width != wantWidth || state.height != wantHeight {
		t.Fatalf("window %d size = %dx%d, want %dx%d", windowID, state.width, state.height, wantWidth, wantHeight)
	}
}
