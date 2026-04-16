package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"io"
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func sampleLayoutSnapshot() *LayoutSnapshot {
	return &LayoutSnapshot{
		SessionName:  "sample",
		ActivePaneID: 2,
		ZoomedPaneID: 3,
		Notice:       "notice",
		Root: CellSnapshot{
			X:      0,
			Y:      0,
			W:      80,
			H:      24,
			IsLeaf: false,
			Dir:    0,
			Children: []CellSnapshot{
				{X: 0, Y: 0, W: 39, H: 24, IsLeaf: true, Dir: -1, PaneID: 1},
				{
					X:      40,
					Y:      0,
					W:      40,
					H:      24,
					IsLeaf: false,
					Dir:    1,
					Children: []CellSnapshot{
						{X: 40, Y: 0, W: 40, H: 11, IsLeaf: true, Dir: -1, PaneID: 2},
						{X: 40, Y: 12, W: 40, H: 12, IsLeaf: true, Dir: -1, PaneID: 3},
					},
				},
			},
		},
		Panes: []PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Task: "shell", Color: "rosewater"},
			{ID: 2, Name: "pane-2", Host: "remote", Task: "build", Color: "mauve", ConnStatus: "connected"},
			{ID: 3, Name: "pane-3", Host: "local", Task: "logs", Color: "green"},
		},
		Width:  80,
		Height: 24,
		Windows: []WindowSnapshot{
			{
				ID:           10,
				Name:         "main",
				Index:        1,
				ActivePaneID: 2,
				ZoomedPaneID: 3,
				Root: CellSnapshot{
					X:        0,
					Y:        0,
					W:        80,
					H:        24,
					IsLeaf:   true,
					Dir:      -1,
					PaneID:   2,
					Children: nil,
				},
				Panes: []PaneSnapshot{
					{ID: 2, Name: "pane-2", Host: "remote", Task: "build", Color: "mauve"},
				},
			},
		},
		ActiveWindowID: 10,
	}
}

func TestWriteReadMsgAllMessageTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  Message
	}{
		{name: "input", msg: Message{Type: MsgTypeInput, Input: []byte("hello")}},
		{name: "resize", msg: Message{Type: MsgTypeResize, Cols: 120, Rows: 40}},
		{
			name: "attach",
			msg: Message{
				Type:       MsgTypeAttach,
				Session:    "test-session",
				Cols:       80,
				Rows:       24,
				AttachMode: AttachModeNonInteractive,
				AttachCapabilities: &ClientCapabilities{
					KittyKeyboard:       true,
					Hyperlinks:          true,
					GraphicsPlaceholder: true,
				},
			},
		},
		{name: "detach", msg: Message{Type: MsgTypeDetach}},
		{name: "command", msg: Message{Type: MsgTypeCommand, CmdName: "list", CmdArgs: []string{"--all"}}},
		{name: "render", msg: Message{Type: MsgTypeRender, RenderData: []byte("\x1b[2Jhello")}},
		{name: "cmd result", msg: Message{Type: MsgTypeCmdResult, CmdOutput: "ok\n", CmdErr: ""}},
		{name: "exit", msg: Message{Type: MsgTypeExit}},
		{name: "notify", msg: Message{Type: MsgTypeNotify, Text: "notice"}},
		{name: "bell", msg: Message{Type: MsgTypeBell}},
		{name: "pane output", msg: Message{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("terminal output")}},
		{name: "layout", msg: Message{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()}},
		{name: "server reload", msg: Message{Type: MsgTypeServerReload}},
		{name: "copy mode", msg: Message{Type: MsgTypeCopyMode, PaneID: 3}},
		{name: "clipboard", msg: Message{Type: MsgTypeClipboard, PaneID: 9, PaneData: []byte("base64-data")}},
		{name: "input pane", msg: Message{Type: MsgTypeInputPane, PaneID: 9, PaneData: []byte("pwd\r")}},
		{
			name: "capture request",
			msg: Message{
				Type:    MsgTypeCaptureRequest,
				CmdArgs: []string{"--format", "json"},
				AgentStatus: map[uint32]PaneAgentStatus{
					2: {
						Exited:         false,
						ExitedSince:    "",
						CurrentCommand: "go",
						Idle:           true,
						IdleSince:      "2026-03-28T12:00:02Z",
						LastOutput:     "2026-03-28T12:00:00Z",
					},
				},
			},
		},
		{name: "capture response", msg: Message{Type: MsgTypeCaptureResponse, CmdOutput: "{json}\n"}},
		{name: "type keys", msg: Message{Type: MsgTypeTypeKeys, Input: []byte("abc")}},
		{name: "ui event", msg: Message{Type: MsgTypeUIEvent, UIEvent: UIEventDisplayPanesShown}},
		{name: "pane history", msg: Message{Type: MsgTypePaneHistory, PaneID: 4, History: []string{"one", "two"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := WriteMsg(&buf, &tt.msg); err != nil {
				t.Fatalf("WriteMsg: %v", err)
			}

			got, err := ReadMsg(&buf)
			if err != nil {
				t.Fatalf("ReadMsg: %v", err)
			}

			if !reflect.DeepEqual(got, &tt.msg) {
				t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, &tt.msg)
			}
		})
	}
}

func TestWriteReadCommandMessagePreservesActorPaneID(t *testing.T) {
	t.Parallel()

	msg := Message{
		Type:        MsgTypeCommand,
		CmdName:     "capture",
		CmdArgs:     []string{"shared"},
		ActorPaneID: 42,
	}

	var buf bytes.Buffer
	if err := WriteMsg(&buf, &msg); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	got, err := ReadMsg(&buf)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if got.ActorPaneID != 42 {
		t.Fatalf("ActorPaneID = %d, want 42", got.ActorPaneID)
	}
}

type failAfterWriter struct {
	remaining int
	err       error
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, w.err
	}
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, w.err
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestWriteMsgErrors(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("write failed")
	tests := []struct {
		name   string
		writer io.Writer
		msg    Message
		want   string
	}{
		{
			name:   "pane output header error",
			writer: &failAfterWriter{remaining: 0, err: writeErr},
			msg:    Message{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte("x")},
			want:   "writing binary header",
		},
		{
			name:   "pane output payload error",
			writer: &failAfterWriter{remaining: 9, err: writeErr},
			msg:    Message{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte("payload")},
			want:   "writing pane data",
		},
		{
			name:   "gob write error",
			writer: &failAfterWriter{remaining: 5, err: writeErr},
			msg:    Message{Type: MsgTypeCommand, CmdName: "list"},
			want:   "write failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := WriteMsg(tt.writer, &tt.msg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("WriteMsg() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func encodeRawMessage(discriminator byte, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(discriminator)
	buf.Write(payload)
	return buf.Bytes()
}

func TestReadMsgErrors(t *testing.T) {
	t.Parallel()

	var oversizeBinary bytes.Buffer
	oversizeBinary.WriteByte(wireFormatBinary)
	if err := binary.Write(&oversizeBinary, binary.BigEndian, uint32(12)); err != nil {
		t.Fatalf("binary.Write pane id: %v", err)
	}
	if err := binary.Write(&oversizeBinary, binary.BigEndian, uint32(maxMessageSize+1)); err != nil {
		t.Fatalf("binary.Write length: %v", err)
	}

	var oversizeGob bytes.Buffer
	oversizeGob.WriteByte(wireFormatGob)
	if err := binary.Write(&oversizeGob, binary.BigEndian, uint32(maxMessageSize+1)); err != nil {
		t.Fatalf("binary.Write gob length: %v", err)
	}

	var shortGob bytes.Buffer
	shortGob.WriteByte(wireFormatGob)
	if err := binary.Write(&shortGob, binary.BigEndian, uint32(4)); err != nil {
		t.Fatalf("binary.Write short gob length: %v", err)
	}
	shortGob.Write([]byte{1, 2})

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "missing discriminator", data: nil, want: "EOF"},
		{name: "oversize binary", data: oversizeBinary.Bytes(), want: "message too large"},
		{name: "oversize gob", data: oversizeGob.Bytes(), want: "message too large"},
		{name: "short binary payload", data: encodeRawMessage(wireFormatBinary, []byte{0, 0, 0, 1, 0, 0, 0, 4, 'o', 'k'}), want: "unexpected EOF"},
		{name: "short gob payload", data: shortGob.Bytes(), want: "decoding message"},
		{name: "invalid gob payload", data: encodeRawMessage(wireFormatGob, []byte{0, 0, 0, 1, 0xff}), want: "decoding message"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ReadMsg(bytes.NewReader(tt.data))
			if err == nil {
				t.Fatal("ReadMsg() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.want) && !errors.Is(err, io.EOF) {
				t.Fatalf("ReadMsg() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestCapturePaneApplyAgentStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		pane   CapturePane
		status map[uint32]PaneAgentStatus
		want   CapturePane
	}{
		{
			name:   "missing status leaves pane unchanged",
			pane:   CapturePane{ID: 2, Name: "pane-2"},
			status: map[uint32]PaneAgentStatus{1: {Exited: true}},
			want:   CapturePane{ID: 2, Name: "pane-2"},
		},
		{
			name: "status fields are applied",
			pane: CapturePane{ID: 2},
			status: map[uint32]PaneAgentStatus{
				2: {
					Exited:         true,
					ExitedSince:    "2025-01-01T00:00:00Z",
					Idle:           true,
					IdleSince:      "2025-01-01T00:00:02Z",
					CurrentCommand: "bash",
					LastOutput:     "2025-01-01T00:00:00Z",
				},
			},
			want: CapturePane{
				ID:             2,
				Exited:         true,
				ExitedSince:    "2025-01-01T00:00:00Z",
				Idle:           true,
				IdleSince:      "2025-01-01T00:00:02Z",
				CurrentCommand: "bash",
				LastOutput:     "2025-01-01T00:00:00Z",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.pane
			got.ApplyAgentStatus(tt.status)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ApplyAgentStatus() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFindCellInSnapshot(t *testing.T) {
	t.Parallel()

	root := sampleLayoutSnapshot().Root

	tests := []struct {
		name   string
		paneID uint32
		want   *CellSnapshot
	}{
		{
			name:   "find nested leaf",
			paneID: 2,
			want:   &root.Children[1].Children[0],
		},
		{
			name:   "find top level leaf",
			paneID: 1,
			want:   &root.Children[0],
		},
		{
			name:   "missing pane",
			paneID: 99,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FindCellInSnapshot(root, tt.paneID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FindCellInSnapshot(%d) = %#v, want %#v", tt.paneID, got, tt.want)
			}
		})
	}
}

func TestFindPaneDimensions(t *testing.T) {
	t.Parallel()

	snap := sampleLayoutSnapshot()
	activeRoot := CellSnapshot{
		X:      0,
		Y:      0,
		W:      60,
		H:      20,
		IsLeaf: true,
		Dir:    -1,
		PaneID: 2,
	}
	contentHeight := func(h int) int { return h - 1 }

	tests := []struct {
		name   string
		active CellSnapshot
		paneID uint32
		wantW  int
		wantH  int
	}{
		{name: "active root match", active: activeRoot, paneID: 2, wantW: 60, wantH: 19},
		{name: "finds pane in other window", active: CellSnapshot{}, paneID: 2, wantW: 80, wantH: 23},
		{name: "falls back to snapshot size", active: CellSnapshot{}, paneID: 99, wantW: 80, wantH: 23},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotW, gotH := FindPaneDimensions(snap, tt.active, tt.paneID, contentHeight)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Fatalf("FindPaneDimensions(%d) = (%d, %d), want (%d, %d)", tt.paneID, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestIsKnownUIEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "known", in: UIEventCopyModeShown, want: true},
		{name: "unknown", in: "totally-unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsKnownUIEvent(tt.in); got != tt.want {
				t.Fatalf("IsKnownUIEvent(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClientCapabilitiesEnabledNamesCoversAllFlags(t *testing.T) {
	t.Parallel()

	caps := ClientCapabilities{
		KittyKeyboard:       true,
		Hyperlinks:          true,
		PromptMarkers:       true,
		CursorMetadata:      true,
		GraphicsPlaceholder: true,
		BinaryPaneHistory:   true,
	}

	got := caps.EnabledNames()
	want := []string{
		"kitty_keyboard",
		"hyperlinks",
		"cursor_metadata",
		"prompt_markers",
		"graphics_placeholder",
		"binary_pane_history",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnabledNames() = %v, want %v", got, want)
	}

	if summary := caps.Summary(); summary != strings.Join(want, ",") {
		t.Fatalf("Summary() = %q, want %q", summary, strings.Join(want, ","))
	}
}

func TestReadMsgUsesGobFallbackForUnknownDiscriminator(t *testing.T) {
	t.Parallel()

	var gobPayload bytes.Buffer
	if err := binary.Write(&gobPayload, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("binary.Write length: %v", err)
	}

	_, err := ReadMsg(bytes.NewReader(append([]byte{0xff}, gobPayload.Bytes()...)))
	if err == nil || !strings.Contains(err.Error(), "decoding message") {
		t.Fatalf("ReadMsg() error = %v, want gob decode failure", err)
	}
}

func TestWriteMsgPaneOutputUsesBinaryFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	msg := &Message{Type: MsgTypePaneOutput, PaneID: 99, PaneData: []byte("abc")}
	if err := WriteMsg(&buf, msg); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	raw := buf.Bytes()
	if len(raw) < 9 {
		t.Fatalf("encoded pane output length = %d, want at least 9", len(raw))
	}
	if raw[0] != wireFormatBinary {
		t.Fatalf("discriminator = %#x, want %#x", raw[0], wireFormatBinary)
	}
	if got := binary.BigEndian.Uint32(raw[1:5]); got != msg.PaneID {
		t.Fatalf("pane id = %d, want %d", got, msg.PaneID)
	}
	if got := binary.BigEndian.Uint32(raw[5:9]); got != uint32(len(msg.PaneData)) {
		t.Fatalf("data length = %d, want %d", got, len(msg.PaneData))
	}
	if got := string(raw[9:]); got != "abc" {
		t.Fatalf("payload = %q, want %q", got, "abc")
	}
}

func TestFindCellInSnapshotDoesNotReturnInternalNodes(t *testing.T) {
	t.Parallel()

	root := sampleLayoutSnapshot().Root
	if got := FindCellInSnapshot(root, 0); got != nil {
		t.Fatalf("FindCellInSnapshot(0) = %#v, want nil", got)
	}
}

func BenchmarkWriteReadMsgPaneOutput(b *testing.B) {
	msg := &Message{Type: MsgTypePaneOutput, PaneID: 5, PaneData: []byte(strings.Repeat("x", 128))}
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := WriteMsg(&buf, msg); err != nil {
			b.Fatalf("WriteMsg: %v", err)
		}
		if _, err := ReadMsg(&buf); err != nil {
			b.Fatalf("ReadMsg: %v", err)
		}
	}
}

func ExampleFindPaneDimensions() {
	snap := sampleLayoutSnapshot()
	w, h := FindPaneDimensions(snap, CellSnapshot{}, 2, func(height int) int { return height - 1 })
	fmt.Printf("%d x %d\n", w, h)
	// Output:
	// 80 x 23
}

func TestPaneHistoryWithStyledCellsRoundTrips(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Type:   MsgTypePaneHistory,
		PaneID: 42,
		History: []string{
			"hello world",
			"styled line",
		},
		StyledHistory: []StyledLine{
			{Text: "hello world"},
			{
				Text: "styled line",
				Cells: []Cell{
					{Char: "s", Style: uvStyle(ansi.Red, ansi.BrightWhite), Width: 1},
					{Char: "t", Style: uvStyle(ansi.RGBColor{R: 0xff, G: 0x88, B: 0x00}, nil), Width: 1},
					{Char: "y", Style: uvStyle(ansi.IndexedColor(42), nil), Width: 1},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteMsg(&buf, msg); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	got, err := ReadMsg(&buf)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", got, msg)
	}
}

func uvStyle(fg, bg color.Color) uv.Style {
	return uv.Style{Fg: fg, Bg: bg}
}
