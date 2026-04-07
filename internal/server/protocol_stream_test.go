package server

import (
	"net"
	"sync"
)

var (
	serverTestReaders sync.Map
	serverTestWriters sync.Map
)

func serverTestReader(conn net.Conn) *Reader {
	if reader, ok := serverTestReaders.Load(conn); ok {
		return reader.(*Reader)
	}
	reader := NewReader(conn)
	actual, _ := serverTestReaders.LoadOrStore(conn, reader)
	return actual.(*Reader)
}

func serverTestWriter(conn net.Conn) *Writer {
	if writer, ok := serverTestWriters.Load(conn); ok {
		return writer.(*Writer)
	}
	writer := NewWriter(conn)
	actual, _ := serverTestWriters.LoadOrStore(conn, writer)
	return actual.(*Writer)
}

func readMsgOnConn(conn net.Conn) (*Message, error) {
	msg, err := serverTestReader(conn).ReadMsg()
	if err != nil {
		serverTestReaders.Delete(conn)
		serverTestWriters.Delete(conn)
	}
	return msg, err
}

func writeMsgOnConn(conn net.Conn, msg *Message) error {
	err := serverTestWriter(conn).WriteMsg(msg)
	if err != nil {
		serverTestReaders.Delete(conn)
		serverTestWriters.Delete(conn)
	}
	return err
}
