package test

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// attachAt connects to the server as a client with the given terminal size,
// reads the layout response, and returns it. The connection is closed on return.
func (h *ServerHarness) attachAt(cols, rows int) *server.Message {
	h.tb.Helper()
	sockPath := server.SocketPath(h.session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		h.tb.Fatalf("attachAt: dial: %v", err)
	}
	defer conn.Close()

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: h.session,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		h.tb.Fatalf("attachAt: write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := server.ReadMsg(conn)
	if err != nil {
		h.tb.Fatalf("attachAt: read layout: %v", err)
	}
	if msg.Type != server.MsgTypeLayout {
		h.tb.Fatalf("attachAt: expected layout, got type %d", msg.Type)
	}
	return msg
}

func TestReattachResize(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Initial session is 80×24. Split vertically to get 2 side-by-side panes.
	h.splitV()

	// Verify initial dimensions.
	msg := h.attachAt(80, 24)
	snap := msg.Layout
	if snap.Width != 80 {
		t.Fatalf("initial width: got %d, want 80", snap.Width)
	}

	// Reattach at a larger size (120×40).
	msg = h.attachAt(120, 40)
	snap = msg.Layout
	if snap.Width != 120 {
		t.Errorf("reattach width: got %d, want 120", snap.Width)
	}
	// layoutH = rows - 1 (global bar)
	if snap.Height != 39 {
		t.Errorf("reattach height: got %d, want 39", snap.Height)
	}

	// The root cell should span the full new dimensions.
	root := snap.Root
	if root.W != 120 {
		t.Errorf("root cell width: got %d, want 120", root.W)
	}
	if root.H != 39 {
		t.Errorf("root cell height: got %d, want 39", root.H)
	}

	// Both children should have roughly half the width (proportional resize).
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	left := root.Children[0]
	right := root.Children[1]
	// Children sum to width-1 (1 col for the vertical border).
	if left.W+right.W != 119 {
		t.Errorf("children widths don't sum to 119: %d + %d", left.W, right.W)
	}
	// Each child should be approximately 60 cols (allow ±2 for rounding).
	if left.W < 58 || left.W > 62 {
		t.Errorf("left pane width: got %d, want ~60", left.W)
	}
}

func TestReattachResizeShrink(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Initial session is 80×24. Split vertically.
	h.splitV()

	// Reattach at a smaller size (60×20).
	msg := h.attachAt(60, 20)
	snap := msg.Layout
	if snap.Width != 60 {
		t.Errorf("shrink width: got %d, want 60", snap.Width)
	}
	if snap.Height != 19 {
		t.Errorf("shrink height: got %d, want 19", snap.Height)
	}

	root := snap.Root
	if root.W != 60 {
		t.Errorf("root cell width: got %d, want 60", root.W)
	}

	// Both children should sum to 60.
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	left := root.Children[0]
	right := root.Children[1]
	// Children sum to width-1 (1 col for the vertical border).
	if left.W+right.W != 59 {
		t.Errorf("children widths don't sum to 59: %d + %d", left.W, right.W)
	}
}
