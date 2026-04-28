package server

import (
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/weill-labs/amux/internal/mux"
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

func TestShutdownTimesOutBlockedCrashCheckpointWrite(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	srv.shutdownCheckpointTimeout = 50 * time.Millisecond
	logger, logBuf := newAuditTestLogger()
	srv.logger = logger

	coord := &blockingCrashCheckpointCoordinator{
		writeStarted: make(chan struct{}),
		releaseWrite: make(chan struct{}),
		stopCalled:   make(chan struct{}),
	}
	sess.checkpointCoordinator = coord

	shutdownDone := make(chan struct{})
	go func() {
		srv.shutdown()
		close(shutdownDone)
	}()

	select {
	case <-coord.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start crash checkpoint write")
	}

	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before checkpoint timeout elapsed")
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		coord.Stop()
		t.Fatal("shutdown hung on blocked crash checkpoint write")
	}

	select {
	case <-coord.stopCalled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not stop the crash checkpoint coordinator")
	}

	if !strings.Contains(logBuf.String(), "timed out waiting for crash checkpoint during shutdown") {
		t.Fatalf("shutdown log = %q, want timeout warning", logBuf.String())
	}
}

func TestShutdownExitsWithWedgedPaneReadLoop(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	logger, logBuf := newAuditTestLogger()
	srv.SetLogger(logger)
	srv.shutdownPaneCloseTimeout = 100 * time.Millisecond

	pane1 := newTestPane(sess, 1, "pane-1")
	wedgedPane := newTestPane(sess, 2, "pane-2")
	pane3 := newTestPane(sess, 3, "pane-3")
	releaseReadLoop := wedgePaneReadLoopForShutdownTest(t, wedgedPane, 25*time.Millisecond)
	defer releaseReadLoop()
	sess.Panes = []*mux.Pane{pane1, wedgedPane, pane3}

	shutdownDone := make(chan struct{})
	start := time.Now()
	go func() {
		srv.shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown hung on wedged pane read loop")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown took %v, want bounded by pane close timeout", elapsed)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "pane_close_read_loop_timeout") {
		t.Fatalf("shutdown log = %q, want read loop timeout warning", logs)
	}
	if !strings.Contains(logs, "pane-2") {
		t.Fatalf("shutdown log = %q, want wedged pane name", logs)
	}
}

func wedgePaneReadLoopForShutdownTest(t *testing.T, pane *mux.Pane, timeout time.Duration) func() {
	t.Helper()

	readLoopDone := make(chan struct{})
	setPaneFieldForShutdownTest(t, pane, "readLoopDone", readLoopDone)
	setPaneFieldForShutdownTest(t, pane, "closeReadLoopTimeout", timeout)

	var releaseOnce sync.Once
	return func() {
		releaseOnce.Do(func() {
			close(readLoopDone)
		})
	}
}

func setPaneFieldForShutdownTest(t *testing.T, pane *mux.Pane, name string, value any) {
	t.Helper()

	field := reflect.ValueOf(pane).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("mux.Pane.%s field not found", name)
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

type blockingCrashCheckpointCoordinator struct {
	writeStarted chan struct{}
	releaseWrite chan struct{}
	stopCalled   chan struct{}
	writeOnce    sync.Once
	stopOnce     sync.Once
}

func (c *blockingCrashCheckpointCoordinator) Trigger() {}

func (c *blockingCrashCheckpointCoordinator) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCalled)
		close(c.releaseWrite)
	})
}

func (c *blockingCrashCheckpointCoordinator) Write() {}

func (c *blockingCrashCheckpointCoordinator) WriteNow() (string, error) {
	c.writeOnce.Do(func() {
		close(c.writeStarted)
	})
	<-c.releaseWrite
	return "", nil
}
