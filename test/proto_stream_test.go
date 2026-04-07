package test

import (
	"net"
	"sync"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

var (
	testProtoReaders sync.Map
	testProtoWriters sync.Map
)

func testProtoReader(conn net.Conn) *proto.Reader {
	if reader, ok := testProtoReaders.Load(conn); ok {
		return reader.(*proto.Reader)
	}
	reader := proto.NewReader(conn)
	actual, _ := testProtoReaders.LoadOrStore(conn, reader)
	return actual.(*proto.Reader)
}

func testProtoWriter(conn net.Conn) *proto.Writer {
	if writer, ok := testProtoWriters.Load(conn); ok {
		return writer.(*proto.Writer)
	}
	writer := proto.NewWriter(conn)
	actual, _ := testProtoWriters.LoadOrStore(conn, writer)
	return actual.(*proto.Writer)
}

func readMsgOnConn(conn net.Conn) (*server.Message, error) {
	msg, err := testProtoReader(conn).ReadMsg()
	if err != nil {
		testProtoReaders.Delete(conn)
		testProtoWriters.Delete(conn)
	}
	return msg, err
}

func writeMsgOnConn(conn net.Conn, msg *server.Message) error {
	err := testProtoWriter(conn).WriteMsg(msg)
	if err != nil {
		testProtoReaders.Delete(conn)
		testProtoWriters.Delete(conn)
	}
	return err
}
