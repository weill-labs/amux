package client

import (
	"net"
	"sync"

	"github.com/weill-labs/amux/internal/proto"
)

type messageSender struct {
	conn net.Conn
	mu   sync.Mutex
}

func newMessageSender(conn net.Conn) *messageSender {
	return &messageSender{conn: conn}
}

func (s *messageSender) Send(msg *proto.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.WriteMsg(s.conn, msg)
}

func (s *messageSender) Command(name string, args []string) {
	_ = s.Send(&proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: name,
		CmdArgs: args,
	})
}
