package server

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

type inputRouter struct {
	target     atomic.Pointer[mux.Pane]
	panes      map[uint32]*mux.Pane
	paneQueues map[uint32]*paneInputQueue
	removed    map[uint32]struct{}
}

type paneInputQueue struct {
	pane  atomic.Pointer[mux.Pane]
	queue *pacedInputQueue
}

func newInputRouter() *inputRouter {
	return &inputRouter{
		panes:      make(map[uint32]*mux.Pane),
		paneQueues: make(map[uint32]*paneInputQueue),
		removed:    make(map[uint32]struct{}),
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

func (r *inputRouter) syncPanes(panes []*mux.Pane) {
	next := make(map[uint32]*mux.Pane, len(panes))
	for _, pane := range panes {
		next[pane.ID] = pane
		delete(r.removed, pane.ID)
	}
	for id := range r.panes {
		if _, ok := next[id]; !ok {
			r.removed[id] = struct{}{}
		}
	}
	for id, queue := range r.paneQueues {
		nextPane, ok := next[id]
		if ok {
			current := r.panes[id]
			if current == nextPane {
				queue.setPane(nextPane)
				continue
			}
			if current != nil && !current.AcceptsInput() && nextPane != nil && nextPane.AcceptsInput() {
				// Keep queued input attached to the pane slot while a pending
				// placeholder is replaced by the real PTY-backed pane.
				queue.setPane(nextPane)
				continue
			}
		}
		queue.close()
		delete(r.paneQueues, id)
		if ok {
			continue
		}
		r.removed[id] = struct{}{}
	}
	r.panes = next
}

func (r *inputRouter) removePane(paneID uint32) {
	delete(r.panes, paneID)
	if queue := r.paneQueues[paneID]; queue != nil {
		queue.close()
		delete(r.paneQueues, paneID)
	}
	r.removed[paneID] = struct{}{}
}

func (r *inputRouter) paneByIDOnActor(sess *Session, paneID uint32) *mux.Pane {
	if pane := r.panes[paneID]; pane != nil {
		return pane
	}
	pane := sess.findPaneByID(paneID)
	if pane == nil {
		return nil
	}
	delete(r.removed, paneID)
	r.panes[paneID] = pane
	return pane
}

func (r *inputRouter) paneQueue(sess *Session, pane *mux.Pane) *paneInputQueue {
	delete(r.removed, pane.ID)
	r.panes[pane.ID] = pane
	return r.paneQueueLocked(sess, pane)
}

func (r *inputRouter) livePaneQueue(sess *Session, pane *mux.Pane) (*paneInputQueue, error) {
	if pane == nil {
		return nil, errPacedInputClosed
	}

	if _, removed := r.removed[pane.ID]; removed {
		return nil, errPacedInputClosed
	}
	r.panes[pane.ID] = pane
	return r.paneQueueLocked(sess, pane), nil
}

func (r *inputRouter) paneQueueLocked(sess *Session, pane *mux.Pane) *paneInputQueue {
	if queue := r.paneQueues[pane.ID]; queue != nil {
		queue.setPane(pane)
		return queue
	}
	paneID := pane.ID
	paneName := pane.Meta.Name
	queue := &paneInputQueue{}
	queue.setPane(pane)
	queue.queue = newPacedInputQueue("pane "+paneName, nil, func(_ uint32, data []byte) error {
		return queue.write(sess, paneID, paneName, data)
	})
	r.paneQueues[pane.ID] = queue
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
		return sess.ensureInputRouter().activeInputPaneForWriteOnActor(sess, cc), nil
	})
	if err != nil {
		return nil
	}
	return pane
}

func (r *inputRouter) activeInputPaneForWriteOnActor(sess *Session, cc *clientConn) *mux.Pane {
	pane := r.targetPane()
	if pane == nil {
		return nil
	}
	if s := sess.currentSizeClient(); s == nil || s != cc {
		if sess.noteClientActivity(cc) {
			sess.recalcSize()
			sess.broadcastLayoutNow()
		}
	}
	return r.targetPane()
}

func (s *Session) activeInputPaneForWrite(cc *clientConn) *mux.Pane {
	return s.ensureInputRouter().activeInputPaneForWrite(s, cc)
}

func (s *Session) enqueuePacedPaneInput(pane *mux.Pane, chunks []encodedKeyChunk) error {
	queue, err := enqueueSessionQuery(s, func(sess *Session) (*paneInputQueue, error) {
		if !sess.hasPane(pane.ID) {
			return nil, fmt.Errorf("%s not found", pane.Meta.Name)
		}
		return sess.ensureInputRouter().paneQueue(sess, pane), nil
	})
	if err != nil {
		return err
	}
	return queue.queue.enqueue(chunks)
}

func (s *Session) enqueueLivePaneInput(pane *mux.Pane, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	queue, err := s.ensureInputRouter().livePaneQueue(s, pane)
	if err != nil {
		return err
	}
	return queue.queue.enqueueAsync([]encodedKeyChunk{{data: append([]byte(nil), data...)}})
}

type paneInputTarget struct {
	pane  *mux.Pane
	ready bool
}

func (q *paneInputQueue) setPane(pane *mux.Pane) {
	q.pane.Store(pane)
}

func (q *paneInputQueue) close() {
	if q == nil || q.queue == nil {
		return
	}
	q.queue.close()
}

func (q *paneInputQueue) write(sess *Session, paneID uint32, paneName string, data []byte) error {
	pane := q.pane.Load()
	if pane != nil && pane.AcceptsInput() {
		_, err := pane.Write(data)
		return err
	}

	pane, err := waitForPaneInputTarget(sess, paneID, paneName)
	if err != nil {
		return err
	}
	q.setPane(pane)
	_, err = pane.Write(data)
	return err
}

func waitForPaneInputTarget(sess *Session, paneID uint32, paneName string) (*mux.Pane, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		target, err := enqueueSessionQuery(sess, func(sess *Session) (paneInputTarget, error) {
			pane := sess.findPaneByID(paneID)
			if pane == nil {
				return paneInputTarget{}, nil
			}
			return paneInputTarget{
				pane:  pane,
				ready: pane.AcceptsInput(),
			}, nil
		})
		if err != nil {
			return nil, err
		}
		if target.pane == nil {
			return nil, fmt.Errorf("%s not found", paneName)
		}
		paneName = target.pane.Meta.Name
		if target.ready {
			return target.pane, nil
		}

		select {
		case <-sess.sessionEventDone:
			return nil, errSessionShuttingDown
		case <-ticker.C:
		}
	}
}
