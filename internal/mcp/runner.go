package mcp

import (
	"context"
	"fmt"
	"net"

	"github.com/weill-labs/amux/internal/dialutil"
	"github.com/weill-labs/amux/internal/server"
)

type ServerCommandRunner struct {
	SessionName string
	ActorPaneID uint32
	DialUnix    func(string) (net.Conn, error)
}

func (r ServerCommandRunner) RunCommand(ctx context.Context, name string, args []string) (string, error) {
	dial := r.DialUnix
	if dial == nil {
		dial = dialutil.DialUnix
	}
	conn, err := dial(server.SocketPath(r.SessionName))
	if err != nil {
		return "", fmt.Errorf("connecting to server: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	writer := server.NewWriter(conn)
	reader := server.NewReader(conn)
	if err := writer.WriteMsg(&server.Message{
		Type:        server.MsgTypeCommand,
		CmdName:     name,
		CmdArgs:     append([]string(nil), args...),
		ActorPaneID: r.ActorPaneID,
	}); err != nil {
		return "", err
	}

	for {
		reply, err := reader.ReadMsg()
		if err != nil {
			return "", err
		}
		if reply == nil || reply.Type != server.MsgTypeCmdResult {
			continue
		}
		if reply.CmdErr != "" {
			return "", fmt.Errorf("%s", reply.CmdErr)
		}
		return reply.CmdOutput, nil
	}
}
