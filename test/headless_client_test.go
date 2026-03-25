package test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// headlessClient is a lightweight attached client that maintains emulators
// and responds to MsgTypeCaptureRequest. It runs without a terminal —
// used by ServerHarness so capture always routes through a client.
type headlessClient struct {
	sockPath   string
	session    string
	cols       int
	rows       int
	caps       proto.ClientCapabilities
	connMu     sync.RWMutex
	writeMu    sync.Mutex
	conn       net.Conn
	renderer   *client.Renderer
	cmdReqs    chan headlessCommand
	cmdResults chan *server.Message
	closing    chan struct{}
	done       chan struct{}
	ready      chan struct{} // closed after first MsgTypeLayout is processed
	readyOnce  sync.Once
	closeOnce  sync.Once
}

type headlessCommand struct {
	msg   *server.Message
	reply chan *server.Message
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
	caps := proto.KnownClientCapabilities()
	hc := &headlessClient{
		sockPath:   sockPath,
		session:    session,
		cols:       cols,
		rows:       rows,
		caps:       caps,
		renderer:   newTestRenderer(cols, rows),
		cmdReqs:    make(chan headlessCommand),
		cmdResults: make(chan *server.Message, 16),
		closing:    make(chan struct{}),
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
	}
	hc.renderer.SetCapabilities(proto.NegotiateClientCapabilities(&caps))
	if err := hc.reconnect(3 * time.Second); err != nil {
		return nil, err
	}

	go hc.commandLoop()
	go hc.readLoop()

	// Block until the server sends the first layout, guaranteeing the
	// window and initial pane exist before any test code runs.
	select {
	case <-hc.ready:
	case <-time.After(10 * time.Second):
		hc.close()
		return nil, fmt.Errorf("timeout waiting for first layout from server")
	}
	return hc, nil
}

func (hc *headlessClient) currentConn() net.Conn {
	hc.connMu.RLock()
	defer hc.connMu.RUnlock()
	return hc.conn
}

func (hc *headlessClient) writeConn(conn net.Conn, msg *server.Message) error {
	if conn == nil {
		return fmt.Errorf("headless client not connected")
	}
	hc.writeMu.Lock()
	defer hc.writeMu.Unlock()
	return server.WriteMsg(conn, msg)
}

func (hc *headlessClient) replaceConn(conn net.Conn) {
	hc.connMu.Lock()
	hc.conn = conn
	hc.connMu.Unlock()
}

func (hc *headlessClient) clearConn(conn net.Conn) {
	hc.connMu.Lock()
	if hc.conn == conn {
		hc.conn = nil
	}
	hc.connMu.Unlock()
}

func (hc *headlessClient) isClosing() bool {
	select {
	case <-hc.closing:
		return true
	default:
		return false
	}
}

func (hc *headlessClient) reconnect(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if hc.isClosing() {
			return fmt.Errorf("headless client closed")
		}

		conn, err := dialHeadlessSocket(hc.sockPath, 250*time.Millisecond)
		if err == nil {
			if err := server.WriteMsg(conn, &server.Message{
				Type:               server.MsgTypeAttach,
				Session:            hc.session,
				Cols:               hc.cols,
				Rows:               hc.rows,
				AttachCapabilities: &hc.caps,
			}); err == nil {
				hc.replaceConn(conn)
				return nil
			}
			lastErr = err
			conn.Close()
		} else {
			lastErr = err
		}

		time.Sleep(25 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for socket")
	}
	return fmt.Errorf("reconnect headless client: %w", lastErr)
}

func (hc *headlessClient) waitCommandReady() error {
	msg := hc.runCommand("cursor", "layout")
	if msg.CmdErr != "" {
		return fmt.Errorf("headless client did not reach command-ready state: %s", msg.CmdErr)
	}
	return nil
}

// resize sends a MsgTypeResize to the server, simulating a terminal resize.
func (hc *headlessClient) resize(cols, rows int) {
	conn := hc.currentConn()
	if conn == nil {
		return
	}
	_ = hc.writeConn(conn, &server.Message{
		Type: server.MsgTypeResize,
		Cols: cols,
		Rows: rows,
	})
}

func (hc *headlessClient) sendUIEvent(name string) {
	conn := hc.currentConn()
	if conn == nil {
		return
	}
	_ = hc.writeConn(conn, &server.Message{
		Type:    server.MsgTypeUIEvent,
		UIEvent: name,
	})
}

// runCommand sends a server command over the attached client connection and
// waits for the single CmdResult reply.
func (hc *headlessClient) runCommand(cmdName string, args ...string) *server.Message {
	req := headlessCommand{
		msg: &server.Message{
			Type:    server.MsgTypeCommand,
			CmdName: cmdName,
			CmdArgs: args,
		},
		reply: make(chan *server.Message, 1),
	}

	select {
	case hc.cmdReqs <- req:
	case <-hc.closing:
		return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
	case <-hc.done:
		return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
	}

	select {
	case msg := <-req.reply:
		return msg
	case <-time.After(10 * time.Second):
		return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "timeout waiting for command result"}
	case <-hc.closing:
		select {
		case msg := <-req.reply:
			return msg
		default:
			return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
		}
	case <-hc.done:
		select {
		case msg := <-req.reply:
			return msg
		default:
			return &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
		}
	}
}

func (hc *headlessClient) commandLoop() {
	for {
		select {
		case <-hc.closing:
			return
		case <-hc.done:
			return
		case req := <-hc.cmdReqs:
			conn := hc.currentConn()
			if conn == nil {
				req.reply <- &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client not connected"}
				continue
			}
			if err := hc.writeConn(conn, req.msg); err != nil {
				req.reply <- &server.Message{Type: server.MsgTypeCmdResult, CmdErr: err.Error()}
				hc.clearConn(conn)
				_ = conn.Close()
				continue
			}

			select {
			case msg := <-hc.cmdResults:
				req.reply <- msg
			case <-hc.closing:
				req.reply <- &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
				return
			case <-hc.done:
				req.reply <- &server.Message{Type: server.MsgTypeCmdResult, CmdErr: "headless client closed"}
				return
			}
		}
	}
}

func (hc *headlessClient) close() {
	_ = hc.shutdown(false)
}

func (hc *headlessClient) detach() error {
	return hc.shutdown(true)
}

func (hc *headlessClient) shutdown(sendDetach bool) error {
	var err error
	hc.closeOnce.Do(func() {
		close(hc.closing)
		if conn := hc.currentConn(); conn != nil {
			if sendDetach {
				err = hc.writeConn(conn, &server.Message{Type: server.MsgTypeDetach})
			}
			_ = conn.Close()
		}
		<-hc.done
		hc.renderer.Close()
	})
	return err
}

func (hc *headlessClient) readLoop() {
	defer close(hc.done)
	for {
		conn := hc.currentConn()
		if conn == nil {
			if err := hc.reconnect(10 * time.Second); err != nil {
				if hc.isClosing() {
					return
				}
				return
			}
			continue
		}

		msg, err := server.ReadMsg(conn)
		if err != nil {
			if hc.isClosing() {
				return
			}
			hc.clearConn(conn)
			_ = conn.Close()
			continue
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
			if err := hc.writeConn(conn, resp); err != nil {
				hc.clearConn(conn)
				_ = conn.Close()
			}
		case server.MsgTypeTypeKeys:
			// no-op: headless client doesn't process keystrokes
		case server.MsgTypeServerReload:
			hc.clearConn(conn)
			_ = conn.Close()
		case server.MsgTypeExit:
			return
		}
	}
}

func TestNewHeadlessClientWaitsForCommandReadyState(t *testing.T) {
	t.Parallel()

	socketDir := filepath.Join(os.TempDir(), "amux-headless-test")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", socketDir, err)
	}
	sockPath := filepath.Join(socketDir, fmt.Sprintf("c-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(sockPath)
	})

	serverReady := make(chan struct{})
	releaseCmdRead := make(chan struct{})
	serverDone := make(chan struct{})

	go func() {
		defer close(serverDone)

		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		msg, err := server.ReadMsg(conn)
		if err != nil {
			return
		}
		if msg.Type != server.MsgTypeAttach {
			return
		}

		layout := &proto.LayoutSnapshot{
			SessionName: "test",
			Width:       80,
			Height:      23,
			Root: proto.CellSnapshot{
				X:      0,
				Y:      0,
				W:      80,
				H:      23,
				IsLeaf: true,
				Dir:    -1,
				PaneID: 1,
			},
			Panes: []proto.PaneSnapshot{{ID: 1, Name: "pane-1"}},
		}
		if err := server.WriteMsg(conn, &server.Message{Type: server.MsgTypeLayout, Layout: layout}); err != nil {
			return
		}
		close(serverReady)

		<-releaseCmdRead

		cmd, err := server.ReadMsg(conn)
		if err != nil {
			return
		}
		if cmd.Type != server.MsgTypeCommand || cmd.CmdName != "cursor" || len(cmd.CmdArgs) != 1 || cmd.CmdArgs[0] != "layout" {
			return
		}
		_ = server.WriteMsg(conn, &server.Message{Type: server.MsgTypeCmdResult, CmdOutput: "1\n"})
		time.Sleep(20 * time.Millisecond)
	}()

	clientReady := make(chan error, 1)
	go func() {
		hc, err := newHeadlessClient(sockPath, "test", 80, 24)
		if err == nil {
			err = hc.waitCommandReady()
			hc.close()
		}
		clientReady <- err
	}()

	select {
	case <-serverReady:
	case <-time.After(time.Second):
		t.Fatal("fake server did not send initial layout")
	}

	select {
	case err := <-clientReady:
		t.Fatalf("newHeadlessClient returned before command ready state: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseCmdRead)

	select {
	case err := <-clientReady:
		if err != nil {
			t.Fatalf("newHeadlessClient error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newHeadlessClient did not become ready after command round-trip")
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("fake server did not exit")
	}
}

// capture returns a plain-text rendering from the client's local emulators.
func (hc *headlessClient) capture() string {
	return hc.renderer.Capture(true)
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

func TestHeadlessClientRunCommandConcurrent(t *testing.T) {
	h := newServerHarness(t)

	results := make(chan *server.Message, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		results <- h.client.runCommand("list")
	}()
	go func() {
		defer wg.Done()
		results <- h.client.runCommand("cursor", "layout")
	}()

	wg.Wait()
	close(results)

	var sawList bool
	var sawGeneration bool
	for msg := range results {
		if msg.CmdErr != "" {
			t.Fatalf("concurrent command failed: %s", msg.CmdErr)
		}

		output := strings.TrimSpace(msg.CmdOutput)
		switch {
		case strings.Contains(output, "pane-1"):
			sawList = true
		default:
			if _, err := strconv.ParseUint(output, 10, 64); err == nil {
				sawGeneration = true
				continue
			}
			t.Fatalf("unexpected command output: %q", output)
		}
	}

	if !sawList {
		t.Fatal("did not receive list output from concurrent commands")
	}
	if !sawGeneration {
		t.Fatal("did not receive generation output from concurrent commands")
	}
}
