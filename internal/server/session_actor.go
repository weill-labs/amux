package server

import (
	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/proto"
)

func recoverSessionQuery(s *Session, r any, stack []byte) error {
	return s.logPanic("session_query_panic", r, stack)
}

func enqueueSessionQuery[T any](s *Session, fn func(*Session) (T, error)) (T, error) {
	return eventloop.EnqueueQuery(s.sessionEvents, s.sessionEventStop, s.sessionEventDone, fn, func(r any, stack []byte) error {
		return recoverSessionQuery(s, r, stack)
	}, errSessionShuttingDown)
}

type captureRequest struct {
	id          uint64
	client      *clientConn
	args        []string
	agentStatus map[uint32]proto.PaneAgentStatus
	reply       chan *Message
}
