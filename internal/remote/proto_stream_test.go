package remote

import (
	"net"
	"sync"

	"github.com/weill-labs/amux/internal/proto"
)

var (
	remoteTestReaders sync.Map
	remoteTestWriters sync.Map
)

func remoteTestReader(conn net.Conn) *proto.Reader {
	if reader, ok := remoteTestReaders.Load(conn); ok {
		return reader.(*proto.Reader)
	}
	reader := proto.NewReader(conn)
	actual, _ := remoteTestReaders.LoadOrStore(conn, reader)
	return actual.(*proto.Reader)
}

func remoteTestWriter(conn net.Conn) *proto.Writer {
	if writer, ok := remoteTestWriters.Load(conn); ok {
		return writer.(*proto.Writer)
	}
	writer := proto.NewWriter(conn)
	actual, _ := remoteTestWriters.LoadOrStore(conn, writer)
	return actual.(*proto.Writer)
}
