package server

import (
	"fmt"
	"sync/atomic"

	"github.com/weill-labs/amux/internal/mux"
)

type inputRouter struct {
	target     atomic.Pointer[mux.Pane]
	pacedPanes map[uint32]*pacedInputQueue
}

func newInputRouter() *inputRouter {
	return &inputRouter{
		pacedPanes: make(map[uint32]*pacedInputQueue),
	}
}

func (s *Session) ensureInputRouter() *inputRouter {
	if s.input == nil {
		s.input = newInputRouter()
	}
	return s.input
}

func (r *inputRouter) refreshTarget(pane *mux.Pane) {
	r.target.Store(pane)
}

func (r *inputRouter) targetPane() *mux.Pane {
	return r.target.Load()
}

func (r *inputRouter) removePane(paneID uint32) {
	if queue := r.pacedPanes[paneID]; queue != nil {
		queue.close()
		delete(r.pacedPanes, paneID)
	}
}

func (r *inputRouter) pacedPaneQueue(pane *mux.Pane) *pacedInputQueue {
	if queue := r.pacedPanes[pane.ID]; queue != nil {
		return queue
	}
	queue := newPacedInputQueue("pane "+pane.Meta.Name, func(data []byte) error {
		_, err := pane.Write(data)
		return err
	})
	r.pacedPanes[pane.ID] = queue
	return queue
}

func (r *inputRouter) activeInputPaneForWrite(sess *Session, cc *clientConn) *mux.Pane {
	pane := r.targetPane()
	if pane == nil {
		return nil
	}
	sizeClient := sess.currentSizeClient()
	if sizeClient == nil || sizeClient == cc {
		return pane
	}

	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		if s := sess.currentSizeClient(); s == nil || s != cc {
			if sess.noteClientActivity(cc) {
				sess.recalcSize()
				sess.broadcastLayoutNow()
			}
		}
		return sess.inputTargetPane(), nil
	})
	if err != nil {
		return nil
	}
	return pane
}

func (s *Session) activeInputPaneForWrite(cc *clientConn) *mux.Pane {
	return s.ensureInputRouter().activeInputPaneForWrite(s, cc)
}

func (s *Session) enqueuePacedPaneInput(pane *mux.Pane, chunks []encodedKeyChunk) error {
	queue, err := enqueueSessionQuery(s, func(sess *Session) (*pacedInputQueue, error) {
		if !sess.hasPane(pane.ID) {
			return nil, fmt.Errorf("%s not found", pane.Meta.Name)
		}
		return sess.ensureInputRouter().pacedPaneQueue(pane), nil
	})
	if err != nil {
		return err
	}
	return queue.enqueue(chunks)
}
