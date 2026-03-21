package test

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/server"
)

// headlessClient is a lightweight attached client that maintains emulators
// and responds to MsgTypeCaptureRequest. It runs without a terminal —
// used by ServerHarness so capture always routes through a client.
type headlessClient struct {
	conn       net.Conn
	renderer   *client.Renderer
	cmdResults chan *server.Message
	cmdMu      sync.Mutex
	done       chan struct{}
	ready      chan struct{} // closed after first MsgTypeLayout is processed
	readyOnce  sync.Once
}

func dialHeadlessSocket(sockPath string, timeout time.Duration) (net.Conn, error) {
	_ = timeout // timeout is exercised by the layout wait below in newHeadlessClient
	return net.Dial("unix", sockPath)
}

func newTestRenderer(cols, rows int) *client.Renderer {
	return client.NewWithScrollback(cols, rows, mux.DefaultScrollbackLines)
}

// newHeadlessClient attaches to the server and starts a background message
// loop. The connection stays alive until close() is called.
func newHeadlessClient(sockPath, session string, cols, rows int) (*headlessClient, error) {
	conn, err := dialHeadlessSocket(sockPath, 3*time.Second)
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
		conn:       conn,
		renderer:   newTestRenderer(cols, rows),
		cmdResults: make(chan *server.Message, 16),
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
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

func (hc *headlessClient) sendUIEvent(name string) {
	server.WriteMsg(hc.conn, &server.Message{
		Type:    server.MsgTypeUIEvent,
		UIEvent: name,
	})
}

// runCommand sends a server command over the attached client connection and
// waits for the single CmdResult reply.
func (hc *headlessClient) runCommand(cmdName string, args ...string) *server.Message {
	hc.cmdMu.Lock()
	defer hc.cmdMu.Unlock()

	if err := server.WriteMsg(hc.conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: cmdName,
		CmdArgs: args,
	}); err != nil {
		return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: err.Error()}
	}

	select {
	case msg := <-hc.cmdResults:
		return msg
	case <-time.After(10 * time.Second):
		return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "timeout waiting for command result"}
	}
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
		case server.MsgTypeCmdResult:
			hc.cmdResults <- msg
		case server.MsgTypePaneHistory:
			// Headless clients only serve screen captures, so retained history
			// bootstrap can be ignored here.
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

func TestParallelServerStartupKeepsAllSocketsAlive(t *testing.T) {
	const servers = 16

	for i := 0; i < servers; i++ {
		i := i
		t.Run(fmt.Sprintf("server-%02d", i), func(t *testing.T) {
			t.Parallel()

			h := newServerHarness(t)
			screen := h.capture()
			if !strings.Contains(screen, "[pane-1]") {
				t.Fatalf("expected initial pane in capture\nScreen:\n%s", screen)
			}
		})
	}
}

func TestHeadlessClientRunCommand(t *testing.T) {
	h := newServerHarness(t)

	msg := h.client.runCommand("list")
	if msg.CmdErr != "" {
		t.Fatalf("list command failed: %s", msg.CmdErr)
	}
	if !strings.Contains(msg.CmdOutput, "pane-1") {
		t.Fatalf("list output = %q, want pane-1", msg.CmdOutput)
	}
}
