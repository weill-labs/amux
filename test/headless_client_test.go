package test

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/server"
)

// headlessClient is a lightweight attached client that maintains emulators
// and responds to MsgTypeCaptureRequest. It runs without a terminal —
// used by ServerHarness so capture always routes through a client.
type headlessClient struct {
	conn      net.Conn
	renderer  *client.Renderer
	done      chan struct{}
	ready     chan struct{} // closed after first MsgTypeLayout is processed
	readyOnce sync.Once
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
		ready:    make(chan struct{}),
	}

	go hc.readLoop()

	// Block until the server sends the first layout, guaranteeing the
	// window and initial pane exist before any test code runs.
	select {
	case <-hc.ready:
	case <-time.After(10 * time.Second):
		conn.Close()
		return nil, fmt.Errorf("timeout waiting for first layout from server")
	}
	return hc, nil
}

// resize sends a MsgTypeResize to the server, simulating a terminal resize.
func (hc *headlessClient) resize(cols, rows int) {
	server.WriteMsg(hc.conn, &server.Message{
		Type: server.MsgTypeResize,
		Cols: cols,
		Rows: rows,
	})
}

// capture returns a plain-text rendering from the client's local emulators.
func (hc *headlessClient) capture() string {
	return hc.renderer.Capture(true)
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
			hc.readyOnce.Do(func() { close(hc.ready) })
		case server.MsgTypePaneOutput:
			hc.renderer.HandlePaneOutput(msg.PaneID, msg.PaneData)
		case server.MsgTypeCaptureRequest:
			resp := hc.renderer.HandleCaptureRequest(msg.CmdArgs, msg.AgentStatus)
			server.WriteMsg(hc.conn, resp)
		case server.MsgTypeTypeKeys:
			// no-op: headless client doesn't process keystrokes
		case server.MsgTypeExit:
			return
		}
	}
}
