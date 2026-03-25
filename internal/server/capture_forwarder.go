package server

import "sync/atomic"

type captureForwarder struct {
	requestCounter atomic.Uint64
	current        *captureRequest
	queue          []*captureRequest
}

func newCaptureForwarder() *captureForwarder {
	return &captureForwarder{}
}

func (s *Session) ensureCaptureForwarder() *captureForwarder {
	if s.capture == nil {
		s.capture = newCaptureForwarder()
	}
	return s.capture
}

func (f *captureForwarder) nextRequestID() uint64 {
	return f.requestCounter.Add(1)
}

func (f *captureForwarder) routeResponse(msg *Message, send func(*captureRequest)) {
	if f.current == nil {
		return
	}
	req := f.current
	f.current = nil
	select {
	case req.reply <- msg:
	default:
	}
	f.startNext(send)
}

func (f *captureForwarder) startNext(send func(*captureRequest)) {
	if f.current != nil || len(f.queue) == 0 {
		return
	}
	next := f.queue[0]
	f.queue = f.queue[1:]
	f.current = next
	sendCaptureRequest(next, send)
}

func (f *captureForwarder) enqueue(req *captureRequest, send func(*captureRequest)) {
	if f.current == nil {
		f.current = req
		sendCaptureRequest(req, send)
		return
	}
	f.queue = append(f.queue, req)
}

func (f *captureForwarder) cancel(id uint64, send func(*captureRequest)) {
	if f.current != nil && f.current.id == id {
		f.current = nil
		f.startNext(send)
		return
	}
	for i, req := range f.queue {
		if req.id != id {
			continue
		}
		f.queue = append(f.queue[:i], f.queue[i+1:]...)
		break
	}
}

func sendCaptureRequest(req *captureRequest, send func(*captureRequest)) {
	if req == nil {
		return
	}
	send(req)
}

type captureForwarderState struct {
	currentID  uint64
	queuedIDs  []uint64
	hasCurrent bool
	queueLen   int
}

func (f *captureForwarder) snapshot() captureForwarderState {
	state := captureForwarderState{hasCurrent: f.current != nil, queueLen: len(f.queue)}
	if f.current != nil {
		state.currentID = f.current.id
	}
	if len(f.queue) > 0 {
		state.queuedIDs = make([]uint64, 0, len(f.queue))
		for _, req := range f.queue {
			state.queuedIDs = append(state.queuedIDs, req.id)
		}
	}
	return state
}
