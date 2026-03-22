package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func startAsyncCommand(t *testing.T, srv *Server, sess *Session, name string, args ...string) (net.Conn, *ClientConn, <-chan struct{}) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	cc := NewClientConn(serverConn)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.handleCommand(srv, sess, &Message{
			Type:    MsgTypeCommand,
			CmdName: name,
			CmdArgs: args,
		})
	}()

	t.Cleanup(func() {
		cc.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	return clientConn, cc, done
}

func assertNoCmdResultWithin(t *testing.T, conn net.Conn, d time.Duration) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	msg, err := ReadMsg(conn)
	if err == nil {
		t.Fatalf("unexpected message before deadline: %#v", msg)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return
	}
	t.Fatalf("ReadMsg: %v", err)
}

func setupWaitVTIdleTestPane(t *testing.T) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[0],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}
	return srv, sess, pane, cleanup
}

func TestCmdWaitVTIdleUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "wait-vt-idle")
	if got := res.cmdErr; got != "usage: wait-vt-idle <pane> [--settle <duration>] [--timeout <duration>]" {
		t.Fatalf("wait-vt-idle usage error = %q", got)
	}
}

func TestCmdWaitVTIdleTimeout(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupWaitVTIdleTestPane(t)
	defer cleanup()

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait-vt-idle", "pane-1", "--settle", "200ms", "--timeout", "40ms")

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdErr; got != "timeout waiting for pane-1 to become vt-idle" {
		t.Fatalf("wait-vt-idle timeout error = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-vt-idle timeout command did not return")
	}
}

func TestCmdWaitVTIdleResetsSettleTimerOnOutput(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitVTIdleTestPane(t)
	defer cleanup()

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait-vt-idle", "pane-1", "--settle", "60ms", "--timeout", "500ms")

	pane.FeedOutput([]byte("first"))
	assertNoCmdResultWithin(t, clientConn, 30*time.Millisecond)

	pane.FeedOutput([]byte("second"))
	assertNoCmdResultWithin(t, clientConn, 30*time.Millisecond)

	msg := readMsgWithTimeout(t, clientConn)
	if got := strings.TrimSpace(msg.CmdOutput); got != "vt-idle" {
		t.Fatalf("wait-vt-idle output = %q, want vt-idle", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-vt-idle command did not return after settling")
	}
}
