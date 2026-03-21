package client

import (
	"net"

	"github.com/weill-labs/amux/internal/proto"
)

type messageSender struct {
	conn     net.Conn
	requests chan senderCommand
	done     chan struct{}
}

type senderCommand interface {
	handle(net.Conn) bool
}

type sendRequest struct {
	msg   *proto.Message
	reply chan error
}

func (r sendRequest) handle(conn net.Conn) bool {
	r.reply <- proto.WriteMsg(conn, r.msg)
	return false
}

type closeRequest struct {
	reply chan struct{}
}

func (r closeRequest) handle(conn net.Conn) bool {
	if conn != nil {
		_ = conn.Close()
	}
	r.reply <- struct{}{}
	return true
}

func newMessageSender(conn net.Conn) *messageSender {
	s := &messageSender{
		conn:     conn,
		requests: make(chan senderCommand),
		done:     make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *messageSender) Send(msg *proto.Message) error {
	reply := make(chan error, 1)
	if !s.enqueue(sendRequest{msg: msg, reply: reply}) {
		return nil
	}
	return <-reply
}

func (s *messageSender) Command(name string, args []string) {
	_ = s.Send(&proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: name,
		CmdArgs: args,
	})
}

func (s *messageSender) Close() {
	reply := make(chan struct{}, 1)
	if !s.enqueue(closeRequest{reply: reply}) {
		return
	}
	<-reply
}

func (s *messageSender) loop() {
	defer close(s.done)
	for req := range s.requests {
		if req == nil {
			continue
		}
		if req.handle(s.conn) {
			return
		}
	}
}

func (s *messageSender) enqueue(cmd senderCommand) bool {
	select {
	case <-s.done:
		return false
	case s.requests <- cmd:
		return true
	}
}
