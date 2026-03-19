package test

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/proto"
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

// attachRendererAt connects to the server as a client with the given terminal
// size, feeds the initial attach stream (layout + pane replays) into a fresh
// renderer, and returns it. afterLayout runs after the initial layout is
// applied but before replayed pane output is processed.
func (h *ServerHarness) attachRendererAt(cols, rows int, afterLayout func(*client.Renderer, *server.Message)) *client.Renderer {
	h.tb.Helper()
	sockPath := server.SocketPath(h.session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		h.tb.Fatalf("attachRendererAt: dial: %v", err)
	}
	defer conn.Close()

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: h.session,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		h.tb.Fatalf("attachRendererAt: write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := server.ReadMsg(conn)
	if err != nil {
		h.tb.Fatalf("attachRendererAt: read layout: %v", err)
	}
	if msg.Type != server.MsgTypeLayout {
		h.tb.Fatalf("attachRendererAt: expected layout, got type %d", msg.Type)
	}

	r := client.New(cols, rows)
	r.HandleLayout(msg.Layout)
	if afterLayout != nil {
		afterLayout(r, msg)
	}

	for range msg.Layout.Panes {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			h.tb.Fatalf("attachRendererAt: read pane output: %v", err)
		}
		if msg.Type != server.MsgTypePaneOutput {
			h.tb.Fatalf("attachRendererAt: expected pane output, got type %d", msg.Type)
		}
		r.HandlePaneOutput(msg.PaneID, msg.PaneData)
	}

	return r
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
	// Layout height excludes the global bar.
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

	// Reattach at a smaller size (60×20). The primary headless client (80×24)
	// is still connected, so "largest client wins" keeps the layout at 80×23.
	msg := h.attachAt(60, 20)
	snap := msg.Layout
	if snap.Width != 80 {
		t.Errorf("shrink width: got %d, want 80 (largest client wins)", snap.Width)
	}
	if snap.Height != 23 {
		t.Errorf("shrink height: got %d, want 23 (largest client wins)", snap.Height)
	}

	root := snap.Root
	if root.W != 80 {
		t.Errorf("root cell width: got %d, want 80", root.W)
	}

	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	left := root.Children[0]
	right := root.Children[1]
	// Children sum to width-1 (1 col for the vertical border).
	if left.W+right.W != 79 {
		t.Errorf("children widths don't sum to 79: %d + %d", left.W, right.W)
	}
}

func TestAttachResyncsStaleCursorState(t *testing.T) {
	t.Parallel()
	h := newServerHarnessWithSize(t, 255, 62)

	h.splitV()
	h.waitFor("pane-2", "$")

	healthyCapture := h.captureJSON()
	healthy := h.jsonPane(healthyCapture, "pane-2")

	var before proto.CapturePane
	r := h.attachRendererAt(255, 62, func(r *client.Renderer, _ *server.Message) {
		// Simulate stale client-side cursor state surviving the initial layout
		// until the attach-time pane replay arrives.
		r.HandlePaneOutput(2, []byte("\033[1;24H"))

		if err := json.Unmarshal([]byte(r.CapturePaneJSON(2, nil)), &before); err != nil {
			t.Fatalf("unmarshal pane-2 before replay: %v", err)
		}
		if got := before.Cursor.Col; got != 23 {
			t.Fatalf("precondition failed: pane-2 cursor col = %d, want 23", got)
		}
	})

	var after proto.CapturePane
	if err := json.Unmarshal([]byte(r.CapturePaneJSON(2, nil)), &after); err != nil {
		t.Fatalf("unmarshal pane-2 after replay: %v", err)
	}
	if got, want := after.Content[0], healthy.Content[0]; got != want {
		t.Fatalf("pane-2 content after attach = %q, want %q", got, want)
	}
	if got, want := after.Cursor.Col, healthy.Cursor.Col; got != want {
		t.Fatalf("pane-2 cursor col after attach = %d, want %d", got, want)
	}
}
