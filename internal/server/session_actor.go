package server

import "github.com/weill-labs/amux/internal/proto"

type sessionQueryResult[T any] struct {
	value T
	err   error
}

type sessionQueryEvent[T any] struct {
	fn    func(*Session) (T, error)
	reply chan sessionQueryResult[T]
}

func (e sessionQueryEvent[T]) handle(s *Session) {
	value, err := e.fn(s)
	e.reply <- sessionQueryResult[T]{value: value, err: err}
}

func enqueueSessionQuery[T any](s *Session, fn func(*Session) (T, error)) (T, error) {
	var zero T

	reply := make(chan sessionQueryResult[T], 1)
	if !s.enqueueEvent(sessionQueryEvent[T]{fn: fn, reply: reply}) {
		return zero, errSessionShuttingDown
	}

	select {
	case res := <-reply:
		return res.value, res.err
	case <-s.sessionEventDone:
		select {
		case res := <-reply:
			return res.value, res.err
		default:
			return zero, errSessionShuttingDown
		}
	}
}

type layoutWaiter struct {
	afterGen uint64
	reply    chan uint64
}

type clipboardWaiter struct {
	afterGen uint64
	reply    chan string
}

type hookWaiter struct {
	afterGen  uint64
	eventName string
	paneName  string
	reply     chan hookResultRecord
}

type captureRequest struct {
	id          uint64
	client      *clientConn
	args        []string
	agentStatus map[uint32]proto.PaneAgentStatus
	reply       chan *Message
}
