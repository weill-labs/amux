package remote

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestWaitForLayout(t *testing.T) {
	t.Parallel()

	t.Run("returns nil on layout", func(t *testing.T) {
		t.Parallel()
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		go func() {
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout})
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
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout})
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
			// Reply with layout
			proto.WriteMsg(server, &proto.Message{Type: proto.MsgTypeLayout})
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
}
