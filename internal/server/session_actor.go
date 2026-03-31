package server

import (
	"fmt"
	"log"

	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/proto"
)

func recoverSessionQuery(r any, stack []byte) error {
	log.Printf("[amux] panic in session query: %v\n%s", r, stack)
	return fmt.Errorf("internal error: %v", r)
}

func enqueueSessionQuery[T any](s *Session, fn func(*Session) (T, error)) (T, error) {
	return eventloop.EnqueueQuery(s.sessionEvents, s.sessionEventStop, s.sessionEventDone, fn, recoverSessionQuery, errSessionShuttingDown)
}

type captureRequest struct {
	id          uint64
	client      *clientConn
	args        []string
	agentStatus map[uint32]proto.PaneAgentStatus
	reply       chan *Message
}
