package test

import (
	"bytes"
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

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			h.tb.Fatalf("attachAt: read: %v", err)
		}
		if msg.Type == server.MsgTypeLayout {
			return msg
		}
	}
}

// attachRendererAt connects to the server as a client with the given terminal
// size, feeds the initial attach stream (layout + pane replays) into a fresh
// renderer, and returns it. afterLayout runs after the initial layout is
// applied but before replayed pane output is processed.
func (h *ServerHarness) attachRendererAt(cols, rows int, afterLayout func(*client.Renderer)) *client.Renderer {
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

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Drain until the initial layout arrives (pane output may arrive first
	// under -race).
	var layoutMsg *server.Message
	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			h.tb.Fatalf("attachRendererAt: read: %v", err)
		}
		if msg.Type == server.MsgTypeLayout {
			layoutMsg = msg
			break
		}
	}

	r := newTestRenderer(cols, rows)
	r.HandleLayout(layoutMsg.Layout)
	if afterLayout != nil {
		afterLayout(r)
	}

	// Read pane replay messages, skipping any interleaved non-pane messages.
	replayed := 0
	for replayed < len(layoutMsg.Layout.Panes) {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			h.tb.Fatalf("attachRendererAt: read pane output: %v", err)
		}
		if msg.Type == server.MsgTypePaneOutput {
			r.HandlePaneOutput(msg.PaneID, msg.PaneData)
			replayed++
		}
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

	// Reattach at a smaller size (60×20). The newly attached client should
	// temporarily own the session size while it is active.
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

func TestReattachResizeBroadcastsLayoutBeforeFirstResizeRedraw(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.startResizeAwareApp("pane-1")

	existingConn := attachClientForConnectionLog(t, h.session, 80, 24)
	defer existingConn.Close()

	sockPath := server.SocketPath(h.session)
	attachConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial second attach: %v", err)
	}
	defer attachConn.Close()

	if err := server.WriteMsg(attachConn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: h.session,
		Cols:    120,
		Rows:    40,
	}); err != nil {
		t.Fatalf("write second attach: %v", err)
	}

	first := readProtocolMsg(t, existingConn)
	if first.Type != server.MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout before resize redraw output", first.Type)
	}
	if first.Layout == nil || first.Layout.Width != 120 || first.Layout.Height != 39 {
		t.Fatalf("layout = %+v, want 120x39 snapshot", first.Layout)
	}

	wantSize := []byte("SIZE 120x38")
	wantBottom := []byte("BOTTOM 120x38")
	var paneData []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg := readProtocolMsg(t, existingConn)
		if msg.Type != server.MsgTypePaneOutput || msg.PaneID != 1 {
			continue
		}
		paneData = append(paneData, msg.PaneData...)
		if bytes.Contains(paneData, wantSize) && bytes.Contains(paneData, wantBottom) {
			return
		}
	}

	t.Fatalf("resize redraw output never contained %q and %q", wantSize, wantBottom)
}

func TestAttachResyncsStaleCursorState(t *testing.T) {
	t.Parallel()
	h := newServerHarnessWithSize(t, 255, 62)

	h.splitV()
	h.waitFor("pane-2", "$")

	healthyCapture := h.captureJSON()
	healthy := h.jsonPane(healthyCapture, "pane-2")

	var before proto.CapturePane
	r := h.attachRendererAt(255, 62, func(r *client.Renderer) {
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

func (h *ServerHarness) startResizeAwareApp(pane string) {
	h.tb.Helper()

	script := `python3 - <<'PY'
import os
import signal
import sys
import time

def draw(*_args):
    size = os.get_terminal_size()
    size_marker = f"SIZE {size.columns}x{size.lines}"
    bottom_marker = f"BOTTOM {size.columns}x{size.lines}"
    sys.stdout.write('\033[2J\033[H' + size_marker + '\n')
    sys.stdout.write(f'\033[{size.lines};1H{bottom_marker}')
    sys.stdout.flush()

signal.signal(signal.SIGWINCH, draw)
size = os.get_terminal_size()
ready = f"READY {size.columns}x{size.lines}"
sys.stdout.write(ready)
sys.stdout.flush()

while True:
    time.sleep(60)
PY`

	h.sendKeys(pane, script, "Enter")
	h.waitForTimeout(pane, "READY ", "10s")
}

func readProtocolMsg(t *testing.T, conn net.Conn) *server.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	msg, err := server.ReadMsg(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	return msg
}
