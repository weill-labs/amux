package server

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestShutdownCommandFlushesReplyBeforeShutdownStarts(t *testing.T) {
	t.Parallel()

	srv, _, cleanup := newCommandTestSession(t)
	defer cleanup()

	listener := &notifyListener{closed: make(chan struct{})}
	srv.listener = listener
	srv.sockPath = t.TempDir() + "/amux.sock"
	srv.shutdownDone = make(chan struct{})
	srv.commands = map[string]CommandHandler{
		"shutdown": func(ctx *CommandContext) {
			ctx.replyCommandMutation(commandMutationResult{
				output:         "ok\n",
				shutdownServer: true,
			})
		},
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	gated := &gatedConn{
		Conn:         serverConn,
		writeStarted: make(chan struct{}),
		writeGate:    make(chan struct{}),
	}

	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		srv.handleOneShot(newClientConn(gated), &Message{Type: MsgTypeCommand, CmdName: "shutdown"})
	}()

	select {
	case <-gated.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("shutdown command did not start writing reply")
	}

	select {
	case <-listener.closed:
		t.Fatal("shutdown started before reply flush completed")
	case <-time.After(100 * time.Millisecond):
	}

	replyCh := make(chan *Message, 1)
	readErrCh := make(chan error, 1)
	go func() {
		msg, err := readMsgOnConn(clientConn)
		if err != nil {
			readErrCh <- err
			return
		}
		replyCh <- msg
	}()

	close(gated.writeGate)

	select {
	case msg := <-replyCh:
		if msg.Type != MsgTypeCmdResult || msg.CmdOutput != "ok\n" {
			t.Fatalf("shutdown reply = %#v, want ok cmd result", msg)
		}
	case err := <-readErrCh:
		t.Fatalf("ReadMsg() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shutdown reply")
	}

	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start after reply flush")
	}

	select {
	case <-commandDone:
	case <-time.After(time.Second):
		t.Fatal("handleOneShot did not return")
	}
}

func TestHandleOneShotWithoutSessionReturnsNoSession(t *testing.T) {
	t.Parallel()

	srv := &Server{}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleOneShot(newClientConn(serverConn), &Message{Type: MsgTypeCommand, CmdName: "status"})
	}()

	msg, err := readMsgOnConn(clientConn)
	if err != nil {
		t.Fatalf("ReadMsg() error = %v", err)
	}
	if msg.Type != MsgTypeCmdResult || msg.CmdErr != "no session" {
		t.Fatalf("one-shot no-session reply = %#v, want no-session cmd result", msg)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleOneShot did not return")
	}
}

type gatedConn struct {
	net.Conn
	writeStarted chan struct{}
	writeGate    chan struct{}
	startOnce    sync.Once
}

func (c *gatedConn) Write(p []byte) (int, error) {
	c.startOnce.Do(func() { close(c.writeStarted) })
	<-c.writeGate
	return c.Conn.Write(p)
}

type notifyListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *notifyListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (l *notifyListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *notifyListener) Addr() net.Addr {
	return trackingAddr("notify")
}
