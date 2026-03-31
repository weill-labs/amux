package server

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/weill-labs/amux/internal/mux"
)

type inputRouter struct {
	target     atomic.Pointer[mux.Pane]
	mu         sync.RWMutex
	panes      map[uint32]*mux.Pane
	paneQueues map[uint32]*pacedInputQueue
	removed    map[uint32]struct{}
}

func newInputRouter() *inputRouter {
	return &inputRouter{
		panes:      make(map[uint32]*mux.Pane),
		paneQueues: make(map[uint32]*pacedInputQueue),
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
	r.mu.Lock()
	defer r.mu.Unlock()

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
			if current := r.panes[id]; current == nextPane {
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
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.panes, paneID)
	if queue := r.paneQueues[paneID]; queue != nil {
		queue.close()
		delete(r.paneQueues, paneID)
	}
	r.removed[paneID] = struct{}{}
}

func (r *inputRouter) paneQueue(pane *mux.Pane) *pacedInputQueue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paneQueueLocked(pane)
}

func (r *inputRouter) livePaneQueue(pane *mux.Pane) (*pacedInputQueue, error) {
	if pane == nil {
		return nil, errPacedInputClosed
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, removed := r.removed[pane.ID]; removed {
		return nil, errPacedInputClosed
	}
	r.panes[pane.ID] = pane
	return r.paneQueueLocked(pane), nil
}

func (r *inputRouter) paneByID(paneID uint32) *mux.Pane {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.panes[paneID]
}

func (r *inputRouter) paneQueueLocked(pane *mux.Pane) *pacedInputQueue {
	if queue := r.paneQueues[pane.ID]; queue != nil {
		return queue
	}
	queue := newPacedInputQueue("pane "+pane.Meta.Name, func(data []byte) error {
		_, err := pane.Write(data)
		return err
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
		return sess.ensureInputRouter().paneQueue(pane), nil
	})
	if err != nil {
		return err
	}
	return queue.enqueue(chunks)
}

func (s *Session) enqueueLivePaneInput(pane *mux.Pane, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	queue, err := s.ensureInputRouter().livePaneQueue(pane)
	if err != nil {
		return err
	}
	return queue.enqueueAsync([]encodedKeyChunk{{data: append([]byte(nil), data...)}})
}
