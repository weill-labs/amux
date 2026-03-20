package server

import (
	"net"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func HandleAttachForTest(s *Server, conn net.Conn, msg *Message) {
	s.handleAttach(conn, msg)
}

func SetAttachBootstrapHookForTest(s *Server, hook func()) {
	s.attachBootstrapHook = hook
}

func NewCommandTestSessionForTest(t *testing.T) (*Server, *Session, func()) {
	return newCommandTestSession(t)
}

func NewProxyPaneForTest(sess *Session, id uint32, name string, cols, rows int) *mux.Pane {
	return mux.NewProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, cols, rows, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
}

func SetLayoutStateForTest(sess *Session, windows []*mux.Window, activeWindowID uint32, panes []*mux.Pane) error {
	_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = windows
		sess.ActiveWindowID = activeWindowID
		sess.Panes = panes
		return struct{}{}, nil
	})
	return err
}

func SubscribePaneOutputForTest(sess *Session, paneID uint32) (chan struct{}, func()) {
	ch := sess.enqueuePaneOutputSubscribe(paneID)
	cleanup := func() {}
	if ch != nil {
		cleanup = func() {
			sess.enqueuePaneOutputUnsubscribe(paneID, ch)
		}
	}
	return ch, cleanup
}

func QueuePreparedSplitForTest(sess *Session, pane *mux.Pane, dir mux.SplitDir, rootLevel bool) error {
	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if err := sess.insertPreparedPaneIntoActiveWindow(pane, dir, rootLevel); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		return res.err
	}
	if res.broadcastLayout {
		sess.broadcastLayout()
	}
	return nil
}

func SnapshotLayoutForTest(sess *Session) (*proto.LayoutSnapshot, error) {
	return enqueueSessionQuery(sess, func(sess *Session) (*proto.LayoutSnapshot, error) {
		idleSnap := sess.snapshotIdleState()
		return sess.snapshotLayout(idleSnap), nil
	})
}
