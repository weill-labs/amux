package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type asyncOutputCollector struct {
	mu      sync.Mutex
	buf     strings.Builder
	updates chan struct{}
	done    chan struct{}
}

type ptyOutputCollector struct {
	*asyncOutputCollector
}

type streamOutputCollector struct {
	*asyncOutputCollector
}

func newAsyncOutputCollector(reader io.Reader) *asyncOutputCollector {
	c := &asyncOutputCollector{
		updates: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(c.done)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
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

func newPTYOutputCollector(ptmx *os.File) *ptyOutputCollector {
	return &ptyOutputCollector{asyncOutputCollector: newAsyncOutputCollector(ptmx)}
}

func newStreamOutputCollector(reader io.Reader) *streamOutputCollector {
	return &streamOutputCollector{asyncOutputCollector: newAsyncOutputCollector(reader)}
}

func (c *asyncOutputCollector) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *asyncOutputCollector) waitContains(t *testing.T, source, want string) string {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		if got := c.snapshot(); strings.Contains(got, want) {
			return got
		}
		select {
		case <-c.updates:
		case <-deadline:
			t.Fatalf("%s never contained %q; got %q", source, want, c.snapshot())
		case <-c.done:
			if got := c.snapshot(); strings.Contains(got, want) {
				return got
			}
			t.Fatalf("%s ended before containing %q; got %q", source, want, c.snapshot())
		}
	}
}

func (c *ptyOutputCollector) waitContains(t *testing.T, want string) string {
	return c.asyncOutputCollector.waitContains(t, "pty output", want)
}

func (c *ptyOutputCollector) waitContainsWithin(want string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if got := c.snapshot(); strings.Contains(got, want) {
			return true
		}
		select {
		case <-c.updates:
		case <-deadline:
			return strings.Contains(c.snapshot(), want)
		case <-c.done:
			return strings.Contains(c.snapshot(), want)
		}
	}
}

func (c *streamOutputCollector) waitContains(t *testing.T, want string) string {
	return c.asyncOutputCollector.waitContains(t, "stream output", want)
}

type runSessionHarness struct {
	t        *testing.T
	session  string
	sockPath string
	listener net.Listener

	ptmx   *os.File
	tty    *os.File
	output *ptyOutputCollector
	stderr *streamOutputCollector

	attachCh chan *proto.Message
	msgCh    chan *proto.Message

	connMu sync.Mutex
	conn   net.Conn
	wire   *proto.Writer

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
	sockPath := proto.SocketPath(session)
	if err := os.MkdirAll(proto.SocketDir(), 0700); err != nil {
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
	prevStderr := os.Stderr
	os.Stdin = tty
	os.Stdout = tty
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		listener.Close()
		_ = ptmx.Close()
		_ = tty.Close()
		t.Fatalf("os.Pipe(stderr): %v", err)
	}
	os.Stderr = stderrW

	output := newPTYOutputCollector(ptmx)
	stderr := newStreamOutputCollector(stderrR)
	h := &runSessionHarness{
		t:        t,
		session:  session,
		sockPath: sockPath,
		listener: listener,
		ptmx:     ptmx,
		tty:      tty,
		output:   output,
		stderr:   stderr,
		attachCh: make(chan *proto.Message, 1),
		msgCh:    make(chan *proto.Message, 64),
		runErr:   make(chan error, 1),
	}

	go h.acceptLoop()
	go func() {
		h.runErr <- RunSession(session, sizeFn)
	}()

	t.Cleanup(func() {
		os.Stdin = prevStdin
		os.Stdout = prevStdout
		os.Stderr = prevStderr
		_ = stderrW.Close()
		_ = stderrR.Close()
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
		reader := proto.NewReader(conn)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		msg, err := reader.ReadMsg()
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			continue
		}

		h.connMu.Lock()
		h.conn = conn
		h.wire = proto.NewWriter(conn)
		h.connMu.Unlock()

		h.attachCh <- msg
		go h.readLoop(reader)
		return
	}
}

func (h *runSessionHarness) readLoop(reader *proto.Reader) {
	for {
		msg, err := reader.ReadMsg()
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
		h.wire = nil
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
	if err := h.wire.WriteMsg(msg); err != nil {
		t.Fatalf("write server message: %v", err)
	}
}

func writeProtoMessages(conn net.Conn, msgs ...*proto.Message) error {
	writer := proto.NewWriter(conn)
	for _, msg := range msgs {
		if err := writer.WriteMsg(msg); err != nil {
			return err
		}
	}
	return nil
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

type stubAttachConn struct {
	readErr            error
	setReadDeadlineErr error
}

func (c *stubAttachConn) Read([]byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}

func (*stubAttachConn) Write([]byte) (int, error) { return 0, errors.New("unexpected write") }
func (*stubAttachConn) Close() error              { return nil }
func (*stubAttachConn) LocalAddr() net.Addr       { return stubAttachAddr("local") }
func (*stubAttachConn) RemoteAddr() net.Addr      { return stubAttachAddr("remote") }
func (*stubAttachConn) SetDeadline(time.Time) error {
	return nil
}
func (c *stubAttachConn) SetReadDeadline(time.Time) error {
	return c.setReadDeadlineErr
}
func (*stubAttachConn) SetWriteDeadline(time.Time) error {
	return nil
}

type deadlineErrorConn struct {
	net.Conn
	setReadDeadlineErr error
}

func (c *deadlineErrorConn) SetReadDeadline(time.Time) error {
	return c.setReadDeadlineErr
}

type stubAttachAddr string

func (a stubAttachAddr) Network() string { return "test" }
func (a stubAttachAddr) String() string  { return string(a) }

func TestAttachBootstrapHelpers(t *testing.T) {
	t.Parallel()

	t.Run("newAttachBootstrapMessage accepts replay payloads", func(t *testing.T) {
		t.Parallel()

		historyMsg := &proto.Message{
			Type:    proto.MsgTypePaneHistory,
			PaneID:  7,
			History: []string{"older", "newer"},
			StyledHistory: []proto.StyledLine{{
				Text: "older",
				Cells: []proto.Cell{{
					Char:  "o",
					Width: 1,
					Style: uv.Style{Fg: ansi.BasicColor(1)},
				}},
			}},
		}
		historyBootstrap, ok := newAttachBootstrapMessage(historyMsg)
		if !ok {
			t.Fatal("history message should be accepted")
		}
		if historyBootstrap.msg == nil {
			t.Fatal("history bootstrap message should keep the original proto message")
		}
		if historyBootstrap.msg.Type != proto.MsgTypePaneHistory || historyBootstrap.msg.PaneID != 7 {
			t.Fatalf("history bootstrap = %+v, want pane history for pane 7", historyBootstrap)
		}
		if len(historyBootstrap.msg.History) != 2 || historyBootstrap.msg.History[0] != "older" {
			t.Fatalf("history bootstrap = %q, want original history", historyBootstrap.msg.History)
		}
		if len(historyBootstrap.msg.StyledHistory) != 1 || historyBootstrap.msg.StyledHistory[0].Text != "older" {
			t.Fatalf("styled history bootstrap = %+v, want original styled history", historyBootstrap.msg.StyledHistory)
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
		if outputBootstrap.msg == nil {
			t.Fatal("output bootstrap message should keep the original proto message")
		}
		if outputBootstrap.msg.Type != proto.MsgTypePaneOutput || outputBootstrap.msg.PaneID != 9 {
			t.Fatalf("output bootstrap = %+v, want pane output for pane 9", outputBootstrap)
		}
		if string(outputBootstrap.msg.PaneData) != "wide" {
			t.Fatalf("output bootstrap = %q, want %q", outputBootstrap.msg.PaneData, "wide")
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
		t.Cleanup(cr.renderer.Close)
		if got := applyAttachBootstrapMessage(cr, attachBootstrapMessage{
			msg: &proto.Message{Type: proto.MsgTypeBell},
		}); got != 0 {
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
		_ = writeProtoMessages(
			serverConn,
			&proto.Message{
				Type:    proto.MsgTypePaneHistory,
				PaneID:  2,
				History: []string{"older-zoomed-line"},
			},
			&proto.Message{
				Type:   proto.MsgTypeLayout,
				Layout: layout,
			},
			&proto.Message{
				Type:     proto.MsgTypePaneOutput,
				PaneID:   1,
				PaneData: []byte("\033[2J\033[Hpeer-pane"),
			},
			&proto.Message{
				Type:     proto.MsgTypePaneOutput,
				PaneID:   2,
				PaneData: []byte("\033[2J\033[H" + zoomedLine),
			},
			&proto.Message{
				Type:     proto.MsgTypePaneOutput,
				PaneID:   3,
				PaneData: []byte("\033[2J\033[H" + hiddenLine),
			},
		)
	}()

	if err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), cr); err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	w, h, ok := cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("zoomed pane-2 emulator missing")
	}
	if w != 80 || h != 22 {
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
	if len(history) != 1 || history[0].Text != "older-zoomed-line" {
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
		_ = writeProtoMessages(serverConn, msgs...)
	}()

	cr := NewClientRenderer(120, 40)
	if err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), cr); err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	w, h, ok := cr.renderer.PaneSize(1)
	if !ok {
		t.Fatal("pane-1 emulator missing")
	}
	if w != 120 || h != 38 {
		t.Fatalf("pane-1 emulator size after bootstrap = %dx%d, want 120x38", w, h)
	}

	lines := strings.Split(cr.renderer.CapturePaneText(1, false), "\n")
	if len(lines) == 0 || lines[0] != resizedLine {
		t.Fatalf("pane-1 first line after bootstrap = %q, want %q", lines[0], resizedLine)
	}
}

func TestReadAttachBootstrapDefersHiddenPaneHistoryUntilLaterWindowActivation(t *testing.T) {
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

	root1 := proto.CellSnapshot{
		X: 0, Y: 0, W: 20, H: 3,
		IsLeaf: true, Dir: -1, PaneID: 1,
	}
	root2 := proto.CellSnapshot{
		X: 0, Y: 0, W: 20, H: 3,
		IsLeaf: true, Dir: -1, PaneID: 2,
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
	}
	layout1 := &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        20,
		Height:       3,
		Root:         root1,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{
			{ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1, Root: root1, Panes: []proto.PaneSnapshot{panes[0]}},
			{ID: 2, Name: "window-2", Index: 2, ActivePaneID: 2, Root: root2, Panes: []proto.PaneSnapshot{panes[1]}},
		},
		ActiveWindowID: 1,
	}
	layout2 := &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 2,
		Width:        20,
		Height:       3,
		Root:         root2,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{
			{ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1, Root: root1, Panes: []proto.PaneSnapshot{panes[0]}},
			{ID: 2, Name: "window-2", Index: 2, ActivePaneID: 2, Root: root2, Panes: []proto.PaneSnapshot{panes[1]}},
		},
		ActiveWindowID: 2,
	}

	const hiddenScreen = "hidden-live"

	go func() {
		defer close(done)
		_ = writeProtoMessages(
			serverConn,
			&proto.Message{Type: proto.MsgTypeLayout, Layout: layout1},
			&proto.Message{Type: proto.MsgTypePaneHistory, PaneID: 1, History: []string{"older-visible"}},
			&proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("\033[2J\033[Hvisible-live")},
			&proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("\033[2J\033[H" + hiddenScreen)},
			&proto.Message{Type: proto.MsgTypeBell},
		)
	}()

	cr := NewClientRenderer(20, 4)
	if err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), cr); err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	if history := cr.loadState().baseHistory[2]; len(history) != 0 {
		t.Fatalf("hidden pane bootstrap history = %q, want no history before deferred update", history)
	}

	cr.HandleLayout(layout2)

	lines := strings.Split(cr.renderer.CapturePaneText(2, false), "\n")
	if len(lines) == 0 || lines[0] != hiddenScreen {
		t.Fatalf("hidden pane first line after window activation = %q, want %q", lines[0], hiddenScreen)
	}

	cr.HandlePaneHistoryMessage(2, []string{"older-hidden"}, nil)

	var capture proto.CaptureJSON
	out := cr.CaptureJSONWithHistory(nil)
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if len(capture.Panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(capture.Panes))
	}
	got := capture.Panes[0].Content
	for len(got) > 0 && got[len(got)-1] == "" {
		got = got[:len(got)-1]
	}
	if want := []string{"older-hidden", hiddenScreen}; !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}

func TestApplyAttachBootstrapMessagePreservesStyledPaneHistory(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(singlePane20x3())
	red := ansi.BasicColor(1)

	applyAttachBootstrapMessage(cr, attachBootstrapMessage{
		msg: &proto.Message{
			Type:    proto.MsgTypePaneHistory,
			PaneID:  1,
			History: []string{"base"},
			StyledHistory: []proto.StyledLine{{
				Text: "base",
				Cells: []proto.Cell{
					{Char: "b", Width: 1, Style: uv.Style{Fg: red}},
					{Char: "a", Width: 1},
					{Char: "s", Width: 1},
					{Char: "e", Width: 1},
				},
			}},
		},
	})

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-1")
	}
	cm.HandleInput([]byte{'g'})
	cell := cm.ViewportCellAt(0, 0)
	if cell.Style.Fg == nil {
		t.Fatal("copy-mode scrollback cell should preserve its foreground color")
	}
	assertSameColor(t, cell.Style.Fg, red)
}

func TestReadImmediateAttachCorrectionReturnsErrorOnConnectionClose(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	// Send a valid bootstrap (layout + pane output), then close immediately
	// so the correction loop gets a read error (not a timeout).
	layout := singlePane20x3()
	go func() {
		_ = writeProtoMessages(
			serverConn,
			&proto.Message{Type: proto.MsgTypeLayout, Layout: layout},
			&proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hi")},
		)
		_ = serverConn.Close()
	}()

	err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), NewClientRenderer(20, 4))
	if err == nil {
		// EOF here means the server vanished before the client reached the
		// normal message loop or saw an explicit exit path.
		t.Fatal("expected error from closed connection during correction window")
	}
}

func TestReadImmediateAttachCorrectionEndsOnUnknownMessageType(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	layout := singlePane20x3()
	go func() {
		_ = writeProtoMessages(
			serverConn,
			&proto.Message{Type: proto.MsgTypeLayout, Layout: layout},
			&proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hi")},
			// Unknown message type during correction window ends the phase gracefully.
			&proto.Message{Type: proto.MsgTypeBell},
		)
	}()

	if err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), NewClientRenderer(20, 4)); err != nil {
		t.Fatalf("readAttachBootstrap returned error for unknown correction message: %v", err)
	}
}

func TestReadAttachBootstrapPaneReplaysBranches(t *testing.T) {
	t.Parallel()

	t.Run("returns immediately when no outputs remain", func(t *testing.T) {
		t.Parallel()

		conn := &stubAttachConn{}
		remaining, err := readAttachBootstrapPaneReplays(conn, proto.NewReader(conn), NewClientRenderer(20, 4), 0, time.Second)
		if err != nil {
			t.Fatalf("readAttachBootstrapPaneReplays() error = %v, want nil", err)
		}
		if remaining != 0 {
			t.Fatalf("remaining outputs = %d, want 0", remaining)
		}
	})

	t.Run("propagates deadline setup errors", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("deadline boom")
		conn := &stubAttachConn{setReadDeadlineErr: wantErr}
		remaining, err := readAttachBootstrapPaneReplays(
			conn,
			proto.NewReader(conn),
			NewClientRenderer(20, 4),
			1,
			time.Second,
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("readAttachBootstrapPaneReplays() error = %v, want %v", err, wantErr)
		}
		if remaining != 1 {
			t.Fatalf("remaining outputs = %d, want 1", remaining)
		}
	})

	t.Run("propagates read errors", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("read boom")
		conn := &stubAttachConn{readErr: wantErr}
		remaining, err := readAttachBootstrapPaneReplays(
			conn,
			proto.NewReader(conn),
			NewClientRenderer(20, 4),
			1,
			time.Second,
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("readAttachBootstrapPaneReplays() error = %v, want %v", err, wantErr)
		}
		if remaining != 1 {
			t.Fatalf("remaining outputs = %d, want 1", remaining)
		}
	})

	t.Run("applies layouts during replay", func(t *testing.T) {
		t.Parallel()

		serverConn, clientConn := net.Pipe()
		t.Cleanup(func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		})

		cr := NewClientRenderer(20, 4)
		go func() {
			_ = writeProtoMessages(
				serverConn,
				&proto.Message{Type: proto.MsgTypeLayout, Layout: singlePane20x3()},
				&proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hello")},
			)
			_ = serverConn.Close()
		}()

		remaining, err := readAttachBootstrapPaneReplays(clientConn, proto.NewReader(clientConn), cr, 1, time.Second)
		if err != nil {
			t.Fatalf("readAttachBootstrapPaneReplays() error = %v, want nil", err)
		}
		if remaining != 0 {
			t.Fatalf("remaining outputs = %d, want 0", remaining)
		}
		if got := strings.Split(cr.renderer.CapturePaneText(1, false), "\n")[0]; got != "hello" {
			t.Fatalf("pane text after replay = %q, want %q", got, "hello")
		}
	})
}

func TestReadAttachBootstrapPropagatesPaneReplaySetupError(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	wantErr := errors.New("deadline boom")
	wrappedConn := &deadlineErrorConn{Conn: clientConn, setReadDeadlineErr: wantErr}
	go func() {
		_ = writeProtoMessages(serverConn, &proto.Message{Type: proto.MsgTypeLayout, Layout: singlePane20x3()})
		_ = serverConn.Close()
	}()

	err := readAttachBootstrap(wrappedConn, proto.NewReader(wrappedConn), NewClientRenderer(20, 4))
	if !errors.Is(err, wantErr) {
		t.Fatalf("readAttachBootstrap() error = %v, want %v", err, wantErr)
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
				_ = writeProtoMessages(serverConn, tt.messages...)
				_ = serverConn.Close()
			}()

			err := readAttachBootstrap(clientConn, proto.NewReader(clientConn), NewClientRenderer(20, 4))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("readAttachBootstrap() error = %v, want nil", err)
				}
				return
			}
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
	if attach.AttachMode != proto.AttachModeInteractive {
		t.Fatalf("attach mode = %v, want %v", attach.AttachMode, proto.AttachModeInteractive)
	}
	if attach.AttachCapabilities == nil || !attach.AttachCapabilities.CursorMetadata || !attach.AttachCapabilities.KittyKeyboard {
		t.Fatalf("attach capabilities = %+v, want negotiated ghostty features", attach.AttachCapabilities)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneHistory, PaneID: 1, History: []string{"hist-1", "hist-2"}})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("界界界界界\r\nnext")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("peer")})
	h.output.waitContains(t, render.AltScreenEnter)
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

	h.writeInput(t, []byte("\x1b[99;5u"))
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeInput && string(msg.Input) == "\x03"
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
	h.output.waitContains(t, "? close")
	h.output.waitContains(t, "q panes")
	h.output.waitContains(t, "\\ root-vsplit")

	h.writeInput(t, []byte{0x01, 'M'})
	h.output.waitContains(t, "No binding")

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
	h.stderr.waitContains(t, "detached: session exited")
	h.output.waitContains(t, render.AltScreenExit)
}

func TestRunSessionRenameWindowPromptHandlesTypeKeys(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv, so t.Parallel() would panic.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)

	h.writeInput(t, []byte{0x01, ','})
	h.output.waitContains(t, "rename-window")

	h.send(t, &proto.Message{Type: proto.MsgTypeCaptureRequest, CmdArgs: []string{"--format", "json"}})
	capture := h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCaptureResponse
	})
	if !strings.Contains(capture.CmdOutput, `"prompt": "rename-window"`) {
		t.Fatalf("capture output = %q, want rename-window prompt state", capture.CmdOutput)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeTypeKeys, Input: []byte("logs\r")})
	h.waitMessage(t, func(msg *proto.Message) bool {
		return msg.Type == proto.MsgTypeCommand && msg.CmdName == "rename-window" && len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "logs"
	})

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
}

func TestRunSessionRenameWindowPromptBellPaths(t *testing.T) {
	// Not parallel: the subtests below use newRunSessionHarness, so t.Parallel() would panic.
	t.Run("prefix binding bells when no active window can be resolved", func(t *testing.T) {
		// Not parallel: newRunSessionHarness uses t.Setenv, so t.Parallel() would panic.
		h := newRunSessionHarness(t, func(int) (int, int, error) {
			return 80, 24, nil
		})

		attach := h.waitAttach(t)
		if attach.Type != proto.MsgTypeAttach {
			t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
		}

		snap := sessionLayoutSnapshot(h.session)
		snap.ActiveWindowID = 99
		h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: snap})
		h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
		h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
		h.output.waitContains(t, render.AltScreenEnter)

		h.writeInput(t, []byte{0x01, ','})
		h.output.waitContains(t, "\a")

		h.send(t, &proto.Message{Type: proto.MsgTypeExit})
		if err := h.waitRunResult(t); err != nil {
			t.Fatalf("RunSession() = %v, want nil", err)
		}
	})

	t.Run("invalid prompt input rings bell", func(t *testing.T) {
		// Not parallel: newRunSessionHarness uses t.Setenv, so t.Parallel() would panic.
		h := newRunSessionHarness(t, func(int) (int, int, error) {
			return 80, 24, nil
		})

		attach := h.waitAttach(t)
		if attach.Type != proto.MsgTypeAttach {
			t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
		}

		h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
		h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
		h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
		h.output.waitContains(t, render.AltScreenEnter)

		h.writeInput(t, []byte{0x01, ','})
		h.output.waitContains(t, "rename-window")

		h.send(t, &proto.Message{Type: proto.MsgTypeTypeKeys, Input: []byte{0x1b, '[', 'A'}})
		h.output.waitContains(t, "\a")

		h.send(t, &proto.Message{Type: proto.MsgTypeExit})
		if err := h.waitRunResult(t); err != nil {
			t.Fatalf("RunSession() = %v, want nil", err)
		}
	})
}

func TestRunSessionDoesNotEnterAltScreenBeforeAttachBootstrapReady(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	if h.output.waitContainsWithin(render.AltScreenEnter, 150*time.Millisecond) {
		t.Fatalf("RunSession should not enter alt screen before bootstrap is ready, got output %q", h.output.snapshot())
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
}

func TestRunSessionRendersLayoutWhenAttachBootstrapPaneOutputStalls(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})

	if !h.output.waitContainsWithin("[pane-1]", time.Second) {
		t.Fatalf("RunSession should render the layout even when attach bootstrap pane replay stalls, got output %q", h.output.snapshot())
	}

	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
}

func TestRunSessionReturnsRawModeErrorAfterBootstrap(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	_ = h.tty.Close()

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("pane-1")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("pane-2")})

	err := h.waitRunResult(t)
	if err == nil || !strings.Contains(err.Error(), "raw mode") {
		t.Fatalf("RunSession() error = %v, want raw mode failure", err)
	}
}

func TestRunSessionFallsBackToDefaultTermSize(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv, so t.Parallel() would panic.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 0, 0, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	if attach.Cols != proto.DefaultTermCols || attach.Rows != proto.DefaultTermRows {
		t.Fatalf("attach size = %dx%d, want %dx%d", attach.Cols, attach.Rows, proto.DefaultTermCols, proto.DefaultTermRows)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
}

func TestRunSessionDetachFlushesPendingInput(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	if attach.AttachMode != proto.AttachModeInteractive {
		t.Fatalf("attach mode = %v, want %v", attach.AttachMode, proto.AttachModeInteractive)
	}
	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)
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
	h.stderr.waitContains(t, "detached: client requested detach")

	t.Run("rejects legacy keys config", func(t *testing.T) {
		assertRunSessionRejectsLegacyKeysConfig(t)
	})
}

func TestRunSessionPrintsDetachReasonWhenConnectionLost(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout/os.Stderr.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)

	h.closeConn()

	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
	h.stderr.waitContains(t, "detached: connection lost")
}

func TestRunSessionEnablesPprofEndpointWhenConfigured(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[debug]\npprof = true\n"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", configPath, err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: sessionLayoutSnapshot(h.session)})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 1, PaneData: []byte("left")})
	h.send(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 2, PaneData: []byte("right")})
	h.output.waitContains(t, render.AltScreenEnter)

	body := fetchUnixHTTP(t, PprofSocketPath(h.session), "/debug/pprof/goroutine?debug=2", 5*time.Second)
	if !strings.Contains(body, "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", body)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})
	if err := h.waitRunResult(t); err != nil {
		t.Fatalf("RunSession() = %v, want nil", err)
	}
	if _, err := os.Lstat(PprofSocketPath(h.session)); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%q) after exit = %v, want not exist", PprofSocketPath(h.session), err)
	}
}

func TestRunSessionReturnsClientPprofSetupError(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[debug]\npprof = true\n"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", configPath, err)
	}
	t.Setenv("AMUX_CONFIG", configPath)
	t.Setenv("AMUX_NO_WATCH", "1")

	session := "client-pprof-setup-error"
	sockPath := PprofProcessSocketPath(session, os.Getpid())
	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(sockPath), err)
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
	}()

	err = RunSession(session, func(int) (int, int, error) {
		return 80, 24, nil
	})
	if err == nil || !strings.Contains(err.Error(), "enabling client pprof debug endpoint failed") {
		t.Fatalf("RunSession() error = %v, want client pprof setup failure", err)
	}
}

func TestRunSessionWrapsAttachBootstrapProtocolError(t *testing.T) {
	// Not parallel: newRunSessionHarness uses t.Setenv and swaps os.Stdin/os.Stdout/os.Stderr.
	h := newRunSessionHarness(t, func(int) (int, int, error) {
		return 80, 24, nil
	})

	attach := h.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	h.send(t, &proto.Message{Type: proto.MsgTypeExit})

	err := h.waitRunResult(t)
	if err == nil {
		t.Fatal("RunSession() error = nil, want attach bootstrap protocol error")
	}
	if !strings.Contains(err.Error(), "attach failed: protocol error") {
		t.Fatalf("RunSession() error = %v, want attach failed protocol error", err)
	}
}

func assertRunSessionRejectsLegacyKeysConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	oldHome, hadHome := os.LookupEnv("HOME")
	oldNoWatch, hadNoWatch := os.LookupEnv("AMUX_NO_WATCH")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("Setenv HOME: %v", err)
	}
	if err := os.Setenv("AMUX_NO_WATCH", "1"); err != nil {
		t.Fatalf("Setenv AMUX_NO_WATCH: %v", err)
	}
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", oldHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadNoWatch {
			_ = os.Setenv("AMUX_NO_WATCH", oldNoWatch)
		} else {
			_ = os.Unsetenv("AMUX_NO_WATCH")
		}
	})

	configDir := filepath.Join(home, ".config", "amux")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[keys]\nprefix = \"C-b\"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := RunSession("legacy-keys", func(int) (int, int, error) {
		return 80, 24, nil
	})
	if err == nil {
		t.Fatal("RunSession() should reject legacy keys config")
	}
	if got, want := err.Error(), `loading config: unsupported config section "keys"`; got != want {
		t.Fatalf("RunSession() error = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(proto.SocketPath("legacy-keys")); !os.IsNotExist(statErr) {
		t.Fatalf("RunSession should fail before starting a server, stat error = %v", statErr)
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
	t.Parallel()

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
	t.Parallel()

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
