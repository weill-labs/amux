package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func newSession(name string) *Session {
	return newSessionWithScrollback(name, mux.DefaultScrollbackLines)
}

func NewServer(sessionName string) (*Server, error) {
	return NewServerWithScrollback(sessionName, mux.DefaultScrollbackLines)
}

func newProxyPane(id uint32, meta mux.PaneMeta, cols, rows int,
	onOutput func(uint32, []byte, uint64), onExit func(uint32, string),
	writeOverride func([]byte) (int, error)) *mux.Pane {
	return mux.NewProxyPaneWithScrollback(id, meta, cols, rows, mux.DefaultScrollbackLines, onOutput, onExit, writeOverride)
}

func stopCrashCheckpointLoop(t *testing.T, sess *Session) {
	t.Helper()

	sess.stopCrashCheckpointLoop()
}

func stopSessionBackgroundLoops(t *testing.T, sess *Session) {
	t.Helper()

	sess.stopEventLoop()
	sess.waitPendingLocalPaneBuilds()
	stopCrashCheckpointLoop(t, sess)
}

func mustSessionQuery[T any](t *testing.T, sess *Session, fn func(*Session) T) T {
	t.Helper()

	value, err := enqueueSessionQuery(sess, func(sess *Session) (T, error) {
		return fn(sess), nil
	})
	if err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}
	return value
}

func mustSessionMutation(t *testing.T, sess *Session, fn func(*Session)) {
	t.Helper()

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		fn(sess)
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}
}

func setSessionLayoutForTest(t *testing.T, sess *Session, activeWindowID uint32, windows []*mux.Window, panes ...*mux.Pane) {
	t.Helper()

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Windows = append([]*mux.Window(nil), windows...)
		sess.ActiveWindowID = activeWindowID
		sess.Panes = append([]*mux.Pane(nil), panes...)
	})
}

func mustCreatePane(t *testing.T, sess *Session, srv *Server, cols, rows int) *mux.Pane {
	t.Helper()

	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		return sess.createPane(srv, cols, rows)
	})
	if err != nil {
		t.Fatalf("enqueueSessionQuery(createPane): %v", err)
	}
	return pane
}

func TestSetSessionLayoutForTestAdvancesCounterPastSeededPaneIDs(t *testing.T) {
	t.Parallel()

	sess := newSession("test-set-session-layout-advances-counter")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	p3 := newTestPane(sess, 3, "pane-3")
	p7 := newTestPane(sess, 7, "pane-7")
	window := newTestWindowWithPanes(t, sess, 1, "main", p3, p7)

	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, p3, p7)

	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 {
		return sess.counter.Load()
	}); got != p7.ID {
		t.Fatalf("counter after seeding = %d, want %d", got, p7.ID)
	}

	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 {
		id, _ := sess.reserveLocalPaneMeta(mux.PaneMeta{})
		return id
	}); got != p7.ID+1 {
		t.Fatalf("next pane ID after seeding = %d, want %d", got, p7.ID+1)
	}
}
