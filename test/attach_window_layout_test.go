package test

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// TestAttachWindowStreamsLayoutUpdates verifies the MsgTypeAttachWindow
// subscription: the connection receives an initial layout snapshot and then a
// fresh MsgTypeLayout whenever the window's structure changes. This is the
// server side of remote window-mirror dynamic resync.
func TestAttachWindowStreamsLayoutUpdates(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	conn, err := net.Dial("unix", server.SocketPath(h.session))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := writeMsgOnConn(conn, &server.Message{
		Type:       proto.MsgTypeAttachWindow,
		Session:    h.session,
		WindowName: "1", // first window, by index
	}); err != nil {
		t.Fatalf("attach-window write: %v", err)
	}

	init, err := readMsgOnConn(conn)
	if err != nil {
		t.Fatalf("initial layout read: %v", err)
	}
	if init.Type != proto.MsgTypeLayout || init.Layout == nil {
		t.Fatalf("expected initial MsgTypeLayout, got type %d", init.Type)
	}
	before := activeWindowPaneCount(init.Layout)
	if before < 1 {
		t.Fatalf("initial pane count = %d, want >= 1", before)
	}

	// A structural change to the window must push a fresh layout.
	h.runCmd("spawn", "--vertical")
	waitForWindowLayout(t, conn, "new pane", func(layout *proto.LayoutSnapshot) bool {
		return activeWindowPaneCount(layout) > before
	})

	// A resize sent over the subscription resizes the remote window (the size a
	// window mirror pushes to match its local dimensions).
	if err := writeMsgOnConn(conn, &server.Message{Type: proto.MsgTypeResize, Cols: 123, Rows: 45}); err != nil {
		t.Fatalf("resize write: %v", err)
	}
	waitForWindowLayout(t, conn, "resized window", func(layout *proto.LayoutSnapshot) bool {
		return layout.Width == 123
	})
}

func waitForWindowLayout(t *testing.T, conn net.Conn, what string, match func(*proto.LayoutSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		msg, err := readMsgOnConn(conn)
		if err != nil {
			t.Fatalf("did not receive layout update (%s): %v", what, err)
		}
		if msg.Type != proto.MsgTypeLayout || msg.Layout == nil {
			continue
		}
		if match(msg.Layout) {
			return
		}
	}
}

func activeWindowPaneCount(layout *proto.LayoutSnapshot) int {
	if layout == nil {
		return 0
	}
	if len(layout.Panes) > 0 {
		return len(layout.Panes)
	}
	if len(layout.Windows) > 0 {
		return len(layout.Windows[0].Panes)
	}
	return 0
}
