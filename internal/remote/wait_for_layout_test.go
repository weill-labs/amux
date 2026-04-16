package remote

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func testLayoutSnapshot() *proto.LayoutSnapshot {
	return &proto.LayoutSnapshot{
		ActivePaneID:   1,
		ActiveWindowID: 1,
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1"},
		},
		Windows: []proto.WindowSnapshot{
			{
				ID:           1,
				Name:         "window-1",
				ActivePaneID: 1,
				Panes: []proto.PaneSnapshot{
					{ID: 1, Name: "pane-1"},
				},
			},
		},
	}
}

func TestWaitForLayout(t *testing.T) {
	t.Parallel()

	t.Run("returns nil on layout", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			mustWriteMsg(t, remoteTestWriter(server), &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, remoteTestReader(client), 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("skips non-layout messages", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			writer := remoteTestWriter(server)
			mustWriteMsg(t, writer, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hello")})
			mustWriteMsg(t, writer, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, remoteTestReader(client), 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("skips unusable layout messages", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			writer := remoteTestWriter(server)
			mustWriteMsg(t, writer, &proto.Message{Type: proto.MsgTypeLayout})
			mustWriteMsg(t, writer, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, remoteTestReader(client), 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error on closed connection", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		server.Close() // close immediately

		err := waitForLayout(client, remoteTestReader(client), 5*time.Second)
		client.Close()
		if err == nil {
			t.Fatal("expected error for closed connection")
		}
	})

	t.Run("returns error on timeout", func(t *testing.T) {
		t.Parallel()
		_, client := net.Pipe()
		defer client.Close()
		// Don't write anything — let the deadline expire

		err := waitForLayout(client, remoteTestReader(client), 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})

	t.Run("falls back when deadlines are unsupported", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			_ = remoteTestWriter(server).WriteMsg(&proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		conn := noDeadlineConn{Conn: client}
		if err := waitForLayout(conn, remoteTestReader(conn), 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("falls back when deadline errors are not wrapped", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			_ = remoteTestWriter(server).WriteMsg(&proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		conn := stringNoDeadlineConn{Conn: client}
		if err := waitForLayout(conn, remoteTestReader(conn), 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestAttachAndWait(t *testing.T) {
	t.Parallel()

	t.Run("sends attach and returns on layout", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			// Read the attach message
			msg, _ := remoteTestReader(server).ReadMsg()
			if msg.Type != proto.MsgTypeAttach {
				return
			}
			if msg.AttachMode != proto.AttachModeNonInteractive {
				t.Errorf("attach mode = %v, want %v", msg.AttachMode, proto.AttachModeNonInteractive)
			}
			// Reply with layout
			mustWriteMsg(t, remoteTestWriter(server), &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := attachAndWait(client, remoteTestWriter(client), remoteTestReader(client), "test-session", 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error when attach write fails", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		server.Close() // close so write fails

		err := attachAndWait(client, remoteTestWriter(client), remoteTestReader(client), "test-session", 5*time.Second)
		client.Close()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "attaching") {
			t.Fatalf("expected attaching error, got: %v", err)
		}
	})

	t.Run("returns error when layout never arrives", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer client.Close()

		go func() {
			// Read the attach, then close without sending layout
			_ = mustReadMsg(t, remoteTestReader(server))
			server.Close()
		}()

		err := attachAndWait(client, remoteTestWriter(client), remoteTestReader(client), "test-session", 5*time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "waiting for remote layout") {
			t.Fatalf("expected layout error, got: %v", err)
		}
	})

	t.Run("returns error when only unusable layout arrives", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer client.Close()

		go func() {
			_ = mustReadMsg(t, remoteTestReader(server))
			mustWriteMsg(t, remoteTestWriter(server), &proto.Message{Type: proto.MsgTypeLayout})
			server.Close()
		}()

		err := attachAndWait(client, remoteTestWriter(client), remoteTestReader(client), "test-session", 5*time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "waiting for remote layout") {
			t.Fatalf("expected layout error, got: %v", err)
		}
	})
}

type noDeadlineConn struct {
	net.Conn
}

func (c noDeadlineConn) SetDeadline(time.Time) error      { return os.ErrNoDeadline }
func (c noDeadlineConn) SetReadDeadline(time.Time) error  { return os.ErrNoDeadline }
func (c noDeadlineConn) SetWriteDeadline(time.Time) error { return os.ErrNoDeadline }

type stringNoDeadlineConn struct {
	net.Conn
}

func (c stringNoDeadlineConn) SetDeadline(time.Time) error { return nil }
func (c stringNoDeadlineConn) SetReadDeadline(time.Time) error {
	return os.NewSyscallError("tcpChan", errDeadlineNotSupported)
}
func (c stringNoDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

var errDeadlineNotSupported = deadlineNotSupportedError{}

type deadlineNotSupportedError struct{}

func (deadlineNotSupportedError) Error() string { return "deadline not supported" }
