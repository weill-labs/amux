package server

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestChunkPaneHistoryMessagesSplitsLargeHistoryUnderThreshold(t *testing.T) {
	t.Parallel()

	const (
		lineCount      = 260
		lineWidth      = 64 * 1024
		chunkThreshold = paneHistoryChunkThreshold
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

func TestNewPaneHistoryMessageCopiesStyledLineHeaders(t *testing.T) {
	t.Parallel()

	history := []proto.StyledLine{
		{
			Text: "line-0",
			Cells: []proto.Cell{
				{Char: "a", Width: 1},
				{Char: "b", Width: 1},
			},
		},
	}

	msg := newPaneHistoryMessage(7, history)

	if got, want := len(msg.StyledHistory), len(history); got != want {
		t.Fatalf("styled history len = %d, want %d", got, want)
	}
	if got, want := msg.History, []string{"line-0"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("history text = %v, want %v", got, want)
	}
	if &msg.StyledHistory[0] == &history[0] {
		t.Fatal("styled history reused caller slice header")
	}
	if len(msg.StyledHistory[0].Cells) != len(history[0].Cells) {
		t.Fatalf("styled history cell count = %d, want %d", len(msg.StyledHistory[0].Cells), len(history[0].Cells))
	}
	if &msg.StyledHistory[0].Cells[0] != &history[0].Cells[0] {
		t.Fatal("styled history cells were deep-cloned, want shared backing cells")
	}

	history[0].Text = "mutated"
	history[0].Cells = nil
	if got, want := msg.StyledHistory[0].Text, "line-0"; got != want {
		t.Fatalf("styled history text = %q, want %q", got, want)
	}
	if got, want := len(msg.StyledHistory[0].Cells), 2; got != want {
		t.Fatalf("styled history cells len after caller mutation = %d, want %d", got, want)
	}
}

func TestHandleAttachChunksLargePaneHistoryDuringBootstrap(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	const (
		lineCount = 260
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

	msg := readMsgWithTimeoutDuration(t, peerConn, 15*time.Second)
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
		msg = readMsgWithTimeoutDuration(t, peerConn, 15*time.Second)
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

func BenchmarkNewPaneHistoryMessage(b *testing.B) {
	history := largeStyledHistoryLines(256, 256)

	b.ReportAllocs()
	b.ResetTimer()

	var msg *Message
	for b.Loop() {
		msg = newPaneHistoryMessage(1, history)
	}
	if msg == nil {
		b.Fatal("newPaneHistoryMessage returned nil")
	}
}

func largeStyledHistoryLines(lineCount, lineWidth int) []proto.StyledLine {
	lines := make([]proto.StyledLine, lineCount)
	for i := range lines {
		text := largeHistoryLines(1, lineWidth)[0]
		text = text[:len(text)-7] + fmt.Sprintf("-%06d", i)

		cells := make([]proto.Cell, 0, len(text))
		for _, r := range text {
			cells = append(cells, proto.Cell{
				Char:  string(r),
				Width: 1,
			})
		}
		lines[i] = proto.StyledLine{
			Text:  text,
			Cells: cells,
		}
	}
	return lines
}
