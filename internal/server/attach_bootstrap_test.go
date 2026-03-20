package server_test

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

func TestHandleAttachFlushesQueuedPaneOutputAfterBootstrap(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := server.NewCommandTestSessionForTest(t)
	defer cleanup()

	pane := server.NewProxyPaneForTest(sess, 1, "pane-1", 80, 2)

	w := mux.NewWindow(pane, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	if err := server.SetLayoutStateForTest(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane}); err != nil {
		t.Fatalf("SetLayoutStateForTest: %v", err)
	}
	pane.FeedOutput([]byte("line-1\r\nline-2\r\nline-3"))

	clientConn, cr, paused, release, done := startPausedAttach(t, srv, sess, 80, 4)
	defer closeAttach(t, clientConn, release, done)

	readInitialAttachReplay(t, clientConn, cr)
	waitForPause(t, paused)

	outputCh, unsubscribe := server.SubscribePaneOutputForTest(sess, pane.ID)
	defer unsubscribe()

	pane.FeedOutput([]byte("\r\nline-4\r\nline-5"))
	waitForSignal(t, outputCh, "queued pane output")

	want := pane.CaptureSnapshot()

	release()
	drainAttachMessages(t, clientConn, cr, 1)

	assertClientPaneMatchesSnapshot(t, cr, pane.ID, want)
}

func TestHandleAttachAppliesQueuedLayoutAfterConcurrentSplit(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := server.NewCommandTestSessionForTest(t)
	defer cleanup()

	pane1 := server.NewProxyPaneForTest(sess, 1, "pane-1", 80, 2)

	w := mux.NewWindow(pane1, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	if err := server.SetLayoutStateForTest(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane1}); err != nil {
		t.Fatalf("SetLayoutStateForTest: %v", err)
	}
	pane1.FeedOutput([]byte("before-1\r\nbefore-2\r\nbefore-3"))

	clientConn, cr, paused, release, done := startPausedAttach(t, srv, sess, 80, 4)
	defer closeAttach(t, clientConn, release, done)

	readInitialAttachReplay(t, clientConn, cr)
	waitForPause(t, paused)

	pane2 := server.NewProxyPaneForTest(sess, 2, "pane-2", 80, 2)
	if err := server.QueuePreparedSplitForTest(sess, pane2, mux.SplitVertical, false); err != nil {
		t.Fatalf("QueuePreparedSplitForTest: %v", err)
	}

	pane1Ch, unsubscribePane1 := server.SubscribePaneOutputForTest(sess, pane1.ID)
	defer unsubscribePane1()
	pane2Ch, unsubscribePane2 := server.SubscribePaneOutputForTest(sess, pane2.ID)
	defer unsubscribePane2()

	pane1.FeedOutput([]byte("\r\nsplit-pane-1"))
	pane2.FeedOutput([]byte("split-pane-2"))
	waitForSignal(t, pane1Ch, "pane-1 queued output")
	waitForSignal(t, pane2Ch, "pane-2 queued output")

	wantLayout, err := server.SnapshotLayoutForTest(sess)
	if err != nil {
		t.Fatalf("SnapshotLayoutForTest: %v", err)
	}
	wantPane1 := pane1.CaptureSnapshot()
	wantPane2 := pane2.CaptureSnapshot()

	release()
	drainAttachMessages(t, clientConn, cr, 2)

	assertClientLayoutMatchesSnapshot(t, cr, wantLayout)
	assertClientPaneMatchesSnapshot(t, cr, pane1.ID, wantPane1)
	assertClientPaneMatchesSnapshot(t, cr, pane2.ID, wantPane2)
}

func startPausedAttach(t *testing.T, srv *server.Server, sess *server.Session, cols, rows int) (net.Conn, *client.ClientRenderer, <-chan struct{}, func(), <-chan struct{}) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	paused := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	done := make(chan struct{})

	server.SetAttachBootstrapHookForTest(srv, func() {
		close(paused)
		<-release
	})

	go func() {
		defer close(done)
		server.HandleAttachForTest(srv, serverConn, &server.Message{
			Type:    server.MsgTypeAttach,
			Session: sess.Name,
			Cols:    cols,
			Rows:    rows,
		})
	}()

	return clientConn, client.NewClientRenderer(cols, rows), paused, func() {
		releaseOnce.Do(func() { close(release) })
	}, done
}

func closeAttach(t *testing.T, clientConn net.Conn, release func(), done <-chan struct{}) {
	t.Helper()

	release()
	if clientConn != nil {
		_ = server.WriteMsg(clientConn, &server.Message{Type: server.MsgTypeDetach})
		_ = clientConn.Close()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit")
	}
}

func readInitialAttachReplay(t *testing.T, conn net.Conn, cr *client.ClientRenderer) *proto.LayoutSnapshot {
	t.Helper()

	var layout *proto.LayoutSnapshot
	for layout == nil {
		msg := readMsgWithTimeout(t, conn)
		if msg.Type != server.MsgTypeLayout {
			continue
		}
		layout = msg.Layout
		cr.HandleLayout(layout)
	}

	replayed := 0
	for replayed < len(layout.Panes) {
		msg := readMsgWithTimeout(t, conn)
		switch msg.Type {
		case server.MsgTypePaneHistory:
			cr.HandlePaneHistory(msg.PaneID, msg.History)
		case server.MsgTypePaneOutput:
			cr.HandlePaneOutput(msg.PaneID, msg.PaneData)
			replayed++
		default:
			t.Fatalf("unexpected bootstrap message: %+v", msg)
		}
	}

	return layout
}

func drainAttachMessages(t *testing.T, conn net.Conn, cr *client.ClientRenderer, wantLayouts int) {
	t.Helper()

	layouts := 0
	for layouts < wantLayouts {
		msg := readMsgWithTimeout(t, conn)
		switch msg.Type {
		case server.MsgTypeLayout:
			cr.HandleLayout(msg.Layout)
			layouts++
		case server.MsgTypePaneHistory:
			cr.HandlePaneHistory(msg.PaneID, msg.History)
		case server.MsgTypePaneOutput:
			cr.HandlePaneOutput(msg.PaneID, msg.PaneData)
		default:
			t.Fatalf("unexpected post-bootstrap message: %+v", msg)
		}
	}
}

func assertClientLayoutMatchesSnapshot(t *testing.T, cr *client.ClientRenderer, want *proto.LayoutSnapshot) {
	t.Helper()

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(cr.CaptureJSON(nil)), &capture); err != nil {
		t.Fatalf("unmarshal CaptureJSON: %v", err)
	}

	if got, wantCount := len(capture.Panes), len(want.Panes); got != wantCount {
		t.Fatalf("capture pane count = %d, want %d", got, wantCount)
	}

	for _, pane := range capture.Panes {
		cell := proto.FindCellInSnapshot(want.Root, pane.ID)
		if cell == nil {
			t.Fatalf("pane %d missing from layout snapshot", pane.ID)
		}
		if pane.Position == nil {
			t.Fatalf("pane %d missing capture position", pane.ID)
		}
		if got, wantPos := pane.Position.X, cell.X; got != wantPos {
			t.Fatalf("pane %d x = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := pane.Position.Y, cell.Y; got != wantPos {
			t.Fatalf("pane %d y = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := pane.Position.Width, cell.W; got != wantPos {
			t.Fatalf("pane %d width = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := pane.Position.Height, cell.H; got != wantPos {
			t.Fatalf("pane %d height = %d, want %d", pane.ID, got, wantPos)
		}
	}
}

func assertClientPaneMatchesSnapshot(t *testing.T, cr *client.ClientRenderer, paneID uint32, want mux.CaptureSnapshot) {
	t.Helper()

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(cr.CapturePaneJSON(paneID, nil)), &pane); err != nil {
		t.Fatalf("unmarshal CapturePaneJSON(%d): %v", paneID, err)
	}
	if len(pane.Content) != len(want.Content) {
		t.Fatalf("pane %d content rows = %d, want %d", paneID, len(pane.Content), len(want.Content))
	}
	for i, wantLine := range want.Content {
		if got := pane.Content[i]; got != wantLine {
			t.Fatalf("pane %d content[%d] = %q, want %q", paneID, i, got, wantLine)
		}
	}
	if pane.Cursor.Col != want.CursorCol || pane.Cursor.Row != want.CursorRow || pane.Cursor.Hidden != want.CursorHidden {
		t.Fatalf("pane %d cursor = %+v, want col=%d row=%d hidden=%v", paneID, pane.Cursor, want.CursorCol, want.CursorRow, want.CursorHidden)
	}

	cr.EnterCopyMode(paneID)
	cm := cr.CopyModeForPane(paneID)
	if cm == nil {
		t.Fatalf("copy mode missing for pane %d", paneID)
	}

	wantLines := append([]string(nil), want.History...)
	wantLines = append(wantLines, want.Content...)
	if got, wantTotal := cm.TotalLines(), len(wantLines); got != wantTotal {
		t.Fatalf("pane %d total lines = %d, want %d", paneID, got, wantTotal)
	}
	for i, wantLine := range wantLines {
		if got := cm.LineText(i); got != wantLine {
			t.Fatalf("pane %d line %d = %q, want %q", paneID, i, got, wantLine)
		}
	}
}

func waitForPause(t *testing.T, paused <-chan struct{}) {
	t.Helper()
	waitForSignal(t, paused, "bootstrap pause")
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	if ch == nil {
		t.Fatalf("missing channel for %s", label)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for %s", label)
	}
}

func readMsgWithTimeout(t *testing.T, conn net.Conn) *server.Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	msg, err := server.ReadMsg(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	return msg
}
