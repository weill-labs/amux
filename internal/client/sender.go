package client

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/weill-labs/amux/internal/proto"
)

const senderRequestBufferSize = 256

type messageSender struct {
	conn      net.Conn
	requests  chan senderCommand
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	err       atomic.Pointer[error]
}

type senderCommand interface {
	handle(net.Conn) (bool, error)
}

type sendRequest struct {
	msg *proto.Message
}

func (r sendRequest) handle(conn net.Conn) (bool, error) {
	return false, proto.WriteMsg(conn, r.msg)
}

func newMessageSender(conn net.Conn) *messageSender {
	s := &messageSender{
		conn:     conn,
		requests: make(chan senderCommand, senderRequestBufferSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *messageSender) Send(msg *proto.Message) error {
	if !s.enqueue(sendRequest{msg: cloneProtoMessage(msg)}) {
		return s.loadError()
	}
	return nil
}

func (s *messageSender) SendAsync(msg *proto.Message) error {
	return s.Send(msg)
}

func (s *messageSender) Command(name string, args []string) {
	_ = s.Send(&proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: name,
		CmdArgs: args,
	})
}

func (s *messageSender) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
	<-s.done
}

func (s *messageSender) loop() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case req := <-s.requests:
			if req == nil {
				continue
			}
			stop, err := req.handle(s.conn)
			if err != nil {
				s.storeError(err)
				return
			}
			if stop {
				return
			}
		}
	}
}

func (s *messageSender) enqueue(cmd senderCommand) bool {
	select {
	case <-s.stop:
		return false
	case <-s.done:
		return false
	case s.requests <- cmd:
		return true
	}
}

func (s *messageSender) storeError(err error) {
	if err == nil {
		return
	}
	errCopy := err
	s.err.CompareAndSwap(nil, &errCopy)
}

func (s *messageSender) loadError() error {
	err := s.err.Load()
	if err == nil {
		return nil
	}
	return *err
}

func cloneProtoMessage(msg *proto.Message) *proto.Message {
	if msg == nil {
		return nil
	}
	cp := *msg
	cp.Input = append([]byte(nil), msg.Input...)
	cp.CmdArgs = append([]string(nil), msg.CmdArgs...)
	cp.RenderData = append([]byte(nil), msg.RenderData...)
	cp.PaneData = append([]byte(nil), msg.PaneData...)
	cp.History = append([]string(nil), msg.History...)
	return &cp
}
