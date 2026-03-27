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

	if sess.crashCheckpointStop != nil {
		close(sess.crashCheckpointStop)
		<-sess.crashCheckpointDone
		sess.crashCheckpointStop = nil
	}
}

func stopSessionBackgroundLoops(t *testing.T, sess *Session) {
	t.Helper()

	if sess.sessionEventStop != nil {
		close(sess.sessionEventStop)
		<-sess.sessionEventDone
		sess.sessionEventStop = nil
	}
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
