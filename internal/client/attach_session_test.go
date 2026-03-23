package client

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"
)

type ptyOutputCollector struct {
	ptmx    *os.File
	mu      sync.Mutex
	buf     strings.Builder
	updates chan struct{}
	done    chan struct{}
}

func newPTYOutputCollector(ptmx *os.File) *ptyOutputCollector {
	c := &ptyOutputCollector{
		ptmx:    ptmx,
		updates: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(c.done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(buf[:n])
				c.mu.Unlock()
				select {
				case c.updates <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *ptyOutputCollector) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *ptyOutputCollector) waitContains(t *testing.T, want string) string {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		if got := c.snapshot(); strings.Contains(got, want) {
			return got
		}
		select {
		case <-c.updates:
		case <-deadline:
			t.Fatalf("pty output never contained %q; got %q", want, c.snapshot())
		case <-c.done:
			if got := c.snapshot(); strings.Contains(got, want) {
				return got
			}
			t.Fatalf("pty output ended before containing %q; got %q", want, c.snapshot())
		}
	}
}

type runSessionHarness struct {
	t        *testing.T
	session  string
	sockPath string
	listener net.Listener

	ptmx   *os.File
	tty    *os.File
	output *ptyOutputCollector

	attachCh chan *proto.Message
	msgCh    chan *proto.Message

	connMu sync.Mutex
	conn   net.Conn

	pendingMu sync.Mutex
	pending   []*proto.Message

	runErr chan error
}

func newRunSessionHarness(t *testing.T, sizeFn func(int) (int, int, error)) *runSessionHarness {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AMUX_NO_WATCH", "1")

	session := fmt.Sprintf("c%d", time.Now().UnixNano()%1_000_000)
	sockPath := server.SocketPath(session)
	if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		listener.Close()
		t.Fatalf("pty.Open: %v", err)
	}

	prevStdin := os.Stdin
	prevStdout := os.Stdout
	os.Stdin = tty
	os.Stdout = tty

	output := newPTYOutputCollector(ptmx)
	h := &runSessionHarness{
		t:        t,
		session:  session,
		sockPath: sockPath,
		listener: listener,
		ptmx:     ptmx,
		tty:      tty,
		output:   output,
		attachCh: make(chan *proto.Message, 1),
		msgCh:    make(chan *proto.Message, 64),
		runErr:   make(chan error, 1),
	}

	stubTermGetSize(t, sizeFn)

	go h.acceptLoop()
	go func() {
		h.runErr <- RunSession(session)
	}()

	t.Cleanup(func() {
		os.Stdin = prevStdin
		os.Stdout = prevStdout
		_ = listener.Close()
		h.closeConn()
		_ = ptmx.Close()
		_ = tty.Close()
		_ = os.Remove(sockPath)
	})

	return h
}

func (h *runSessionHarness) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		msg, err := proto.ReadMsg(conn)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			continue
		}

		h.connMu.Lock()
		h.conn = conn
		h.connMu.Unlock()

		h.attachCh <- msg
		go h.readLoop(conn)
		return
	}
}

func (h *runSessionHarness) readLoop(conn net.Conn) {
	for {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			return
		}
		h.msgCh <- msg
	}
}

func (h *runSessionHarness) closeConn() {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.conn != nil {
		_ = h.conn.Close()
		h.conn = nil
	}
}

func (h *runSessionHarness) waitAttach(t *testing.T) *proto.Message {
	t.Helper()

	select {
	case msg := <-h.attachCh:
		return msg
	case err := <-h.runErr:
		t.Fatalf("RunSession exited before attach: %v", err)
		return nil
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for attach")
		return nil
	}
}

func (h *runSessionHarness) waitMessage(t *testing.T, match func(*proto.Message) bool) *proto.Message {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		h.pendingMu.Lock()
		for i, msg := range h.pending {
			if match(msg) {
				h.pending = append(h.pending[:i], h.pending[i+1:]...)
				h.pendingMu.Unlock()
				return msg
			}
		}
		h.pendingMu.Unlock()

		select {
		case msg := <-h.msgCh:
			if match(msg) {
				return msg
			}
			h.pendingMu.Lock()
			h.pending = append(h.pending, msg)
			h.pendingMu.Unlock()
		case <-deadline:
			t.Fatal("timed out waiting for client message")
			return nil
		}
	}
}

func (h *runSessionHarness) send(t *testing.T, msg *proto.Message) {
	t.Helper()

	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.conn == nil {
		t.Fatal("server connection is not ready")
	}
	if err := proto.WriteMsg(h.conn, msg); err != nil {
		t.Fatalf("write server message: %v", err)
	}
}

func (h *runSessionHarness) writeInput(t *testing.T, data []byte) {
	t.Helper()

	if _, err := h.ptmx.Write(data); err != nil {
		t.Fatalf("write pty input: %v", err)
	}
}

func (h *runSessionHarness) waitRunResult(t *testing.T) error {
	t.Helper()

	select {
	case err := <-h.runErr:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for RunSession to exit")
		return nil
	}
}

func sessionLayoutSnapshot(session string) *proto.LayoutSnapshot {
	snap := twoPane80x23()
	snap.SessionName = session
	return snap
}

func TestAttachBootstrapHelpers(t *testing.T) {
	t.Parallel()

	t.Run("newAttachBootstrapMessage copies payloads", func(t *testing.T) {
		t.Parallel()

		historyMsg := &proto.Message{
			Type:    proto.MsgTypePaneHistory,
			PaneID:  7,
			History: []string{"older", "newer"},
		}
		historyBootstrap, ok := newAttachBootstrapMessage(historyMsg)
		if !ok {
			t.Fatal("history message should be accepted")
		}
		historyMsg.History[0] = "mutated"
		if historyBootstrap.typ != proto.MsgTypePaneHistory || historyBootstrap.paneID != 7 {
			t.Fatalf("history bootstrap = %+v, want pane history for pane 7", historyBootstrap)
		}
		if len(historyBootstrap.history) != 2 || historyBootstrap.history[0] != "older" {
			t.Fatalf("history bootstrap copy = %q, want original history", historyBootstrap.history)
		}

		outputMsg := &proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   9,
			PaneData: []byte("wide"),
		}
		outputBootstrap, ok := newAttachBootstrapMessage(outputMsg)
		if !ok {
			t.Fatal("output message should be accepted")
		}
		outputMsg.PaneData[0] = 'n'
		if outputBootstrap.typ != proto.MsgTypePaneOutput || outputBootstrap.paneID != 9 {
			t.Fatalf("output bootstrap = %+v, want pane output for pane 9", outputBootstrap)
		}
		if string(outputBootstrap.data) != "wide" {
			t.Fatalf("output bootstrap copy = %q, want %q", outputBootstrap.data, "wide")
		}

		if _, ok := newAttachBootstrapMessage(&proto.Message{Type: proto.MsgTypeLayout}); ok {
			t.Fatal("layout message should not be treated as a bootstrap replay payload")
		}
	})

	t.Run("attachBootstrapPaneCount handles nil legacy and multi-window snapshots", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			layout *proto.LayoutSnapshot
			want   int
		}{
			{name: "nil", layout: nil, want: 0},
			{
				name: "legacy panes",
				layout: &proto.LayoutSnapshot{
					Panes: []proto.PaneSnapshot{{ID: 1}, {ID: 2}},
				},
				want: 2,
			},
			{name: "multi-window", layout: multiWindow80x23Zoomed(1, 2), want: 3},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				if got := attachBootstrapPaneCount(tt.layout); got != tt.want {
					t.Fatalf("attachBootstrapPaneCount() = %d, want %d", got, tt.want)
				}
			})
		}
	})

	t.Run("applyAttachBootstrapMessage ignores unexpected types", func(t *testing.T) {
		t.Parallel()

		cr := NewClientRenderer(20, 4)
		if got := applyAttachBootstrapMessage(cr, attachBootstrapMessage{typ: proto.MsgTypeBell}); got != 0 {
			t.Fatalf("applyAttachBootstrapMessage() = %d, want 0 for unexpected type", got)
		}
	})
}

func TestReadAttachBootstrapAppliesZoomedReplayBeforeReturn(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	cr := NewClientRenderer(80, 24)
	layout := multiWindow80x23Zoomed(1, 2)

	const zoomedLine = "LAB352-01234567890123456789012345678901234567890123456789012345"
	const hiddenLine = "hidden-window-pane"

	go func() {
		_ = proto.WriteMsg(serverConn, &proto.Message{
			Type:    proto.MsgTypePaneHistory,
			PaneID:  2,
			History: []string{"older-zoomed-line"},
		})
		_ = proto.WriteMsg(serverConn, &proto.Message{
			Type:   proto.MsgTypeLayout,
			Layout: layout,
		})
		_ = proto.WriteMsg(serverConn, &proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   1,
			PaneData: []byte("\033[2J\033[Hpeer-pane"),
		})
		_ = proto.WriteMsg(serverConn, &proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   2,
			PaneData: []byte("\033[2J\033[H" + zoomedLine),
		})
		_ = proto.WriteMsg(serverConn, &proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   3,
			PaneData: []byte("\033[2J\033[H" + hiddenLine),
		})
	}()

	if err := readAttachBootstrap(clientConn, cr); err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	emu, ok := cr.Emulator(2)
	if !ok {
		t.Fatal("zoomed pane-2 emulator missing")
	}
	if w, h := emu.Size(); w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after bootstrap = %dx%d, want 80x22", w, h)
	}

	lines := strings.Split(cr.renderer.CapturePaneText(2, false), "\n")
	if len(lines) == 0 || lines[0] != zoomedLine {
		t.Fatalf("zoomed pane-2 first line after bootstrap = %q, want %q", lines[0], zoomedLine)
	}

	hidden := strings.Split(cr.renderer.CapturePaneText(3, false), "\n")
	if len(hidden) == 0 || hidden[0] != hiddenLine {
		t.Fatalf("pane-3 first line after bootstrap = %q, want %q", hidden[0], hiddenLine)
	}

	history := cr.loadState().baseHistory[2]
	if len(history) != 1 || history[0] != "older-zoomed-line" {
		t.Fatalf("pane-2 bootstrap history = %q, want %q", history, []string{"older-zoomed-line"})
	}
}

func TestReadAttachBootstrapAppliesImmediateReattachResizeCorrectionBeforeReturn(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("attach bootstrap writer did not exit")
		}
	})

	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 120, H: 39,
		IsLeaf: true, Dir: -1, PaneID: 1,
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
	}
	layout := &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        120,
		Height:       39,
		Root:         root,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{{
			ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1,
			Root:  root,
			Panes: panes,
		}},
		ActiveWindowID: 1,
	}

	const staleLine = "READY 80x22"
	const resizedLine = "SIZE 120x38"

	go func() {
		defer close(done)
		msgs := []*proto.Message{
			{Type: proto.MsgTypeLayout, Layout: layout},
			{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("\033[2J\033[H" + staleLine)},
			{Type: proto.MsgTypeLayout, Layout: layout},
			{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("\033[2J\033[H" + resizedLine)},
		}
		for _, msg := range msgs {
			if err := proto.WriteMsg(serverConn, msg); err != nil {
				return
			}
		}
	}()

	cr := NewClientRenderer(120, 40)
	if err := readAttachBootstrap(clientConn, cr); err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	emu, ok := cr.Emulator(1)
	if !ok {
		t.Fatal("pane-1 emulator missing")
	}
	if w, h := emu.Size(); w != 120 || h != 38 {
		t.Fatalf("pane-1 emulator size after bootstrap = %dx%d, want 120x38", w, h)
	}

	lines := strings.Split(cr.renderer.CapturePaneText(1, false), "\n")
	if len(lines) == 0 || lines[0] != resizedLine {
		t.Fatalf("pane-1 first line after bootstrap = %q, want %q", lines[0], resizedLine)
	}
}

func TestReadImmediateAttachCorrectionReturnsErrorOnConnectionClose(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	// Send a valid bootstrap (layout + pane output), then close immediately
	// so the correction loop gets a read error (not a timeout).
	layout := singlePane20x3()
	go func() {
		_ = proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeLayout, Layout: layout})
		_ = proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hi")})
		_ = serverConn.Close()
	}()

	err := readAttachBootstrap(clientConn, NewClientRenderer(20, 4))
	if err == nil {
		t.Fatal("expected error from closed connection during correction window")
	}
}

func TestReadImmediateAttachCorrectionRejectsUnexpectedMessageType(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	layout := singlePane20x3()
	go func() {
		_ = proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeLayout, Layout: layout})
		_ = proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hi")})
		// Send an unexpected message type during the correction window.
		_ = proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeBell})
	}()

	err := readAttachBootstrap(clientConn, NewClientRenderer(20, 4))
	if err == nil || !strings.Contains(err.Error(), "unexpected attach bootstrap correction message type") {
		t.Fatalf("got err=%v, want 'unexpected attach bootstrap correction message type'", err)
	}
}

func TestReadAttachBootstrapRejectsUnexpectedMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		messages []*proto.Message
		wantErr  string
	}{
		{
			name:     "before layout",
			messages: []*proto.Message{{Type: proto.MsgTypeBell}},
			wantErr:  "before layout",
		},
		{
			name: "after layout",
			messages: []*proto.Message{
				{Type: proto.MsgTypeLayout, Layout: singlePane20x3()},
				{Type: proto.MsgTypeBell},
			},
			wantErr: "after layout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serverConn, clientConn := net.Pipe()
			t.Cleanup(func() {
				_ = serverConn.Close()
				_ = clientConn.Close()
			})

			go func() {
				for _, msg := range tt.messages {
					_ = proto.WriteMsg(serverConn, msg)
				}
				_ = serverConn.Close()
			}()

			err := readAttachBootstrap(clientConn, NewClientRenderer(20, 4))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readAttachBootstrap() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunSessionHandlesServerMessagesAndInteractiveInput(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")

	var sizeCalls atomic.Int32
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		if sizeCalls.Add(1) == 1 {
			return 80, 24, nil
		}
		return 40, 12, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	if attach.Session != h.session {
		t.Fatalf("attach session = %q, want %q", attach.Session, h.session)
	}
	if attach.Cols != 80 || attach.Rows != 24 {
		t.Fatalf("attach size = %dx%d, want 80x24", attach.Cols, attach.Rows)
	}
	if attach.AttachCapabilities == nil || !attach.AttachCapabilities.CursorMetadata || !attach.AttachCapabilities.KittyKeyboard {
		t.Fatalf("attach capabilities = %+v, want negotiated ghostty features", attach.AttachCapabilities)
	}

	h.output.waitContains(t, render.AltScreenEnter)

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneHistory, PaneID: 1, History: []string{"hist-1", "hist-2"}})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("界界界界界\r\nnext")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("peer")})
	h.output.waitContains(t, "pane-1")
	h.output.waitContains(t, "next")

	resize := h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeResize
	})
	if resize.Cols != 40 || resize.Rows != 12 {
		t.Fatalf("resize size = %dx%d, want 40x12", resize.Cols, resize.Rows)
	}

	h.writeInput(t, []byte("\x1b[I"))
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventClientFocusGained
	})

	h.writeInput(t, []byte("hi"))
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeInput && string(msg.Input) == "hi"
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventInputBusy
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventInputIdle
	})

	h.writeInput(t, []byte{0x01, 'q', '2'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventDisplayPanesShown
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCommand && msg.CmdName == "focus" && len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "2"
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventDisplayPanesHidden
	})
	focusSnap := sessionLayoutSnapshot(h.session)
	focusSnap.ActivePaneID = 2
	focusSnap.Windows[0].ActivePaneID = 2
	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: focusSnap})

	h.writeInput(t, []byte{0x01, '?'})
	h.output.waitContains(t, "No binding for C-a ?")

	h.writeInput(t, []byte{0x01, 'M'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCommand && msg.CmdName == "toggle-minimize"
	})
	h.output.waitContains(t, "cannot minimize: pane")

	h.writeInput(t, []byte{0x01, '[', 'q'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventCopyModeShown
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeUIEvent && msg.UIEvent == proto.UIEventCopyModeHidden
	})

	h.writeInput(t, []byte{0x01, 0x1b, '[', 'C'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCommand && msg.CmdName == "focus" && len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "right"
	})

	h.writeInput(t, []byte{0x1b, 'h'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCommand && msg.CmdName == "focus" && len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "left"
	})

	h.writeInput(t, []byte{0x01, 0x01})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeInput && len(msg.Input) == 1 && msg.Input[0] == 0x01
	})

	h.send(t, &proto.Message{
		Type:        proto.MsgTypeCaptureRequest,
		CmdArgs:     []string{"--format", "json"},
		AgentStatus: map[uint32]proto.PaneAgentStatus{1: {CurrentCommand: "make test"}},
	})
	capture := h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCaptureResponse
	})
	if !strings.Contains(capture.CmdOutput, "\"pane-1\"") || !strings.Contains(capture.CmdOutput, "界") {
		t.Fatalf("capture output = %q, want pane metadata and unicode content", capture.CmdOutput)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeTypeKeys, Input: []byte("Z")})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeInput && string(msg.Input) == "Z"
	})

	h.send(t, &proto.Message{Type: proto.MsgTypeBell})
	h.output.waitContains(t, "\a")

	h.send(t, &proto.Message{Type: proto.MsgTypeClipboard, PaneData: []byte("clipboard-text")})
	h.output.waitContains(t, "clipboard-text")

	h.send(t, &proto.Message{Type: proto.MsgTypeCmdResult, CmdErr: "boom"})
	h.output.waitContains(t, "boom")

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
	h.output.waitContains(t, render.AltScreenExit)
}

func TestRunSessionDetachFlushesPendingInput(t *testing.T) {
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	h.output.waitContains(t, render.AltScreenEnter)

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, "left")

	h.writeInput(t, []byte{'x', 0x01, 'd'})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeInput && string(msg.Input) == "x"
	})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeDetach
	})

	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
}

func TestAdvertisedAttachCapabilitiesUsesEnvironment(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("AMUX_CLIENT_CAPABILITIES", "graphics_placeholder")

	caps := advertisedAttachCapabilities()
	if caps == nil {
		t.Fatal("advertisedAttachCapabilities() = nil, want capabilities")
	}
	if !caps.KittyKeyboard || !caps.Hyperlinks || !caps.CursorMetadata || !caps.GraphicsPlaceholder {
		t.Fatalf("advertised capabilities = %+v, want iTerm defaults plus override", *caps)
	}
}

func TestFormatKeyHelpers(t *testing.T) {
	tests := []struct {
		in   byte
		want string
	}{
		{in: 0x01, want: "C-a"},
		{in: 0x1b, want: "Esc"},
		{in: ' ', want: "Space"},
		{in: 'x', want: "x"},
	}

	for _, tt := range tests {
		if got := formatKeyName(tt.in); got != tt.want {
			t.Fatalf("formatKeyName(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}

	if got := formatUnboundPrefixMessage(0x01, '?'); got != "No binding for C-a ?" {
		t.Fatalf("formatUnboundPrefixMessage() = %q, want %q", got, "No binding for C-a ?")
	}
}

func TestExecSelfMissingBinaryReturnsWithoutDetaching(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	ExecSelf(filepath.Join(t.TempDir(), "missing-amux"), sender, 0, nil, proto.ClientCapabilities{})
	assertNoMessage(t, serverConn)
}
