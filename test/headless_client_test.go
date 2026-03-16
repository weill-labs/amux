package test

import (
	"net"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/server"
)

// headlessClient is a lightweight attached client that maintains emulators
// and responds to MsgTypeCaptureRequest. It runs without a terminal —
// used by ServerHarness so capture always routes through a client.
type headlessClient struct {
	conn     net.Conn
	renderer *client.Renderer
	done     chan struct{}
}

// newHeadlessClient attaches to the server and starts a background message
// loop. The connection stays alive until close() is called.
func newHeadlessClient(sockPath, session string, cols, rows int) (*headlessClient, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: session,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		conn.Close()
		return nil, err
	}

	hc := &headlessClient{
		conn:     conn,
		renderer: client.New(cols, rows),
		done:     make(chan struct{}),
	}

	go hc.readLoop()
	return hc, nil
}

func (hc *headlessClient) close() {
	hc.conn.Close()
	<-hc.done
}

func (hc *headlessClient) readLoop() {
	defer close(hc.done)
	for {
		msg, err := server.ReadMsg(hc.conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case server.MsgTypeLayout:
			hc.renderer.HandleLayout(msg.Layout)
		case server.MsgTypePaneOutput:
			hc.renderer.HandlePaneOutput(msg.PaneID, msg.PaneData)
		case server.MsgTypeCaptureRequest:
			resp := hc.renderer.HandleCaptureRequest(msg.CmdArgs, msg.AgentStatus)
			server.WriteMsg(hc.conn, resp)
		case server.MsgTypeExit:
			return
		}
	}
}
