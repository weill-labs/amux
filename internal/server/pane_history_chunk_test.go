package server

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestChunkPaneHistoryMessagesSplitsLargeHistoryUnderThreshold(t *testing.T) {
	t.Parallel()

	const (
		lineCount      = 320
		lineWidth      = 64 * 1024
		chunkThreshold = 1 << 20
	)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 2, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	lines := largeHistoryLines(lineCount, lineWidth)
	pane.SetRetainedHistory(lines)

	styledHistory, _, _ := pane.StyledHistoryScreenSnapshot()
	chunks, err := chunkPaneHistoryMessages(pane.ID, styledHistory, chunkThreshold)
	if err != nil {
		t.Fatalf("chunkPaneHistoryMessages: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunk count = %d, want more than one chunk", len(chunks))
	}

	var flat []string
	for i, msg := range chunks {
		if msg.Type != MsgTypePaneHistory {
			t.Fatalf("chunk %d type = %v, want pane history", i, msg.Type)
		}
		if msg.PaneID != pane.ID {
			t.Fatalf("chunk %d pane id = %d, want %d", i, msg.PaneID, pane.ID)
		}
		size, err := estimatePaneHistoryMessageSize(msg)
		if err != nil {
			t.Fatalf("estimate chunk %d size: %v", i, err)
		}
		if size > chunkThreshold {
			t.Fatalf("chunk %d size = %d, want <= %d", i, size, chunkThreshold)
		}
		flat = append(flat, msg.History...)
	}

	if got, want := len(flat), len(lines); got != want {
		t.Fatalf("history line count after chunking = %d, want %d", got, want)
	}
	for i, want := range lines {
		if got := flat[i]; got != want {
			t.Fatalf("history line %d = %q, want %q", i, got, want)
		}
	}
}

func TestHandleAttachChunksLargePaneHistoryDuringBootstrap(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	const (
		lineCount = 320
		lineWidth = 64 * 1024
	)

	pane := newAttachTestPane(sess, 1, "pane-1", 80, 2)
	lines := largeHistoryLines(lineCount, lineWidth)
	pane.SetRetainedHistory(lines)

	w := mux.NewWindow(pane, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	serverConn, peerConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleAttach(newClientConn(serverConn), &Message{
			Type:    MsgTypeAttach,
			Session: sess.Name,
			Cols:    80,
			Rows:    24,
		})
	}()

	msg := readMsgWithTimeoutDuration(t, peerConn, 5*time.Second)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}
	if msg.Layout == nil || len(msg.Layout.Panes) != 1 {
		t.Fatalf("layout panes = %d, want 1", len(msg.Layout.Panes))
	}
	paneCount := len(msg.Layout.Panes)

	var (
		historyMsgs int
		gotHistory  []string
		outputs     int
	)
	for outputs < paneCount {
		msg = readMsgWithTimeoutDuration(t, peerConn, 5*time.Second)
		switch msg.Type {
		case MsgTypePaneHistory:
			historyMsgs++
			gotHistory = append(gotHistory, msg.History...)
		case MsgTypePaneOutput:
			outputs++
		default:
			t.Fatalf("unexpected bootstrap message: %+v", msg)
		}
	}

	if historyMsgs < 2 {
		t.Fatalf("history message count = %d, want at least 2 chunks", historyMsgs)
	}
	if got, want := len(gotHistory), len(lines); got != want {
		t.Fatalf("bootstrapped history line count = %d, want %d", got, want)
	}
	for i, want := range lines {
		if got := gotHistory[i]; got != want {
			t.Fatalf("bootstrapped history line %d = %q, want %q", i, got, want)
		}
	}

	if err := peerConn.Close(); err != nil {
		t.Fatalf("Close peer conn: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleAttach did not exit after detach")
	}
}

func largeHistoryLines(lineCount, lineWidth int) []string {
	lines := make([]string, lineCount)
	for i := range lines {
		suffix := fmt.Sprintf("-%06d", i)
		lines[i] = strings.Repeat(string(rune('a'+(i%26))), lineWidth-len(suffix)) + suffix
	}
	return lines
}
