package remote

import (
	"net"
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
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("skips non-layout messages", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hello")})
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("skips unusable layout messages", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout})
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := waitForLayout(client, 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error on closed connection", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		server.Close() // close immediately

		err := waitForLayout(client, 5*time.Second)
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

		err := waitForLayout(client, 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error")
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
			msg, _ := proto.ReadMsg(server)
			if msg.Type != proto.MsgTypeAttach {
				return
			}
			if msg.Interactive == nil || *msg.Interactive {
				t.Errorf("attach interactive = %v, want explicit interactive=false", msg.Interactive)
			}
			// Reply with layout
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout, Layout: testLayoutSnapshot()})
		}()

		if err := attachAndWait(client, "test-session", 5*time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error when attach write fails", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		server.Close() // close so write fails

		err := attachAndWait(client, "test-session", 5*time.Second)
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
			proto.ReadMsg(server)
			server.Close()
		}()

		err := attachAndWait(client, "test-session", 5*time.Second)
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
			proto.ReadMsg(server)
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout})
			server.Close()
		}()

		err := attachAndWait(client, "test-session", 5*time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "waiting for remote layout") {
			t.Fatalf("expected layout error, got: %v", err)
		}
	})
}
