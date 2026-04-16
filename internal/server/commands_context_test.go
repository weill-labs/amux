package server

import (
	"net"
	"testing"

	"github.com/weill-labs/amux/internal/auditlog"
)

func TestCommandContextSendNilClientIsNoOp(t *testing.T) {
	t.Parallel()

	ctx := &CommandContext{CommandName: "status"}
	ctx.send(&Message{Type: MsgTypeCmdResult, CmdOutput: "ok\n"})
}

func TestCommandContextSendLogsWhenWriteFails(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	_ = peerConn.Close()
	defer func() { _ = serverConn.Close() }()

	cc := newClientConn(serverConn)
	cc.logger = auditlog.Discard()

	ctx := &CommandContext{
		CC:          cc,
		CommandName: "status",
	}
	ctx.send(&Message{Type: MsgTypeCmdResult, CmdOutput: "ok\n"})
}
