package server

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestClientConnQueuesBroadcastsDuringBootstrap(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)
	cc.startBootstrap()

	layout := &Message{
		Type: MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{
			Width:  80,
			Height: 23,
		},
	}
	cc.sendBroadcast(layout)
	cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("live-output")}, 7, 9)

	assertNoClientMessage(t, clientConn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(map[uint32]uint64{7: 5})
	}()

	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}
	if msg.Layout == nil || msg.Layout.Width != 80 || msg.Layout.Height != 23 {
		t.Fatalf("layout = %+v, want 80x23", msg.Layout)
	}

	msg = readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("second message type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != 7 || string(msg.PaneData) != "live-output" {
		t.Fatalf("pane output = pane %d %q, want pane 7 live-output", msg.PaneID, string(msg.PaneData))
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}
}

func TestClientConnDropsStaleQueuedPaneOutputAfterBootstrap(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)
	cc.startBootstrap()
	cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 3, PaneData: []byte("stale")}, 3, 5)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(map[uint32]uint64{3: 5})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}

	assertNoClientMessage(t, clientConn)

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 3, PaneData: []byte("fresh")}, 3, 6)
	}()
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("sendPaneOutput blocked before client read")
	}
	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("message type = %v, want pane output", msg.Type)
	}
	if string(msg.PaneData) != "fresh" {
		t.Fatalf("pane output = %q, want fresh", string(msg.PaneData))
	}
}

func TestClientConnSendPaneOutputDoesNotBlockOnUnreadClient(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 9, PaneData: []byte("hello")}, 9, 1)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sendPaneOutput blocked on unread client")
	}

	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("message type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != 9 || string(msg.PaneData) != "hello" {
		t.Fatalf("pane output = pane %d %q, want pane 9 hello", msg.PaneID, string(msg.PaneData))
	}
}

func TestClientConnBootstrappingStateTracksLifecycle(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	if cc.isBootstrapping() {
		t.Fatal("new client should not report bootstrapping")
	}

	cc.startBootstrap()
	if !cc.isBootstrapping() {
		t.Fatal("startBootstrap should report bootstrapping")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(nil)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}

	if cc.isBootstrapping() {
		t.Fatal("finishBootstrap should clear bootstrapping state")
	}
}

func assertNoClientMessage(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := ReadMsg(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			t.Fatalf("reset read deadline: %v", err)
		}
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("reset read deadline: %v", err)
	}
	if err != nil {
		t.Fatalf("ReadMsg unexpected error: %v", err)
	}
	t.Fatalf("unexpected message during bootstrap: %+v", msg)
}
