package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type attachPaneProtocolHarness struct {
	srv     *Server
	sess    *Session
	pane1   *mux.Pane
	pane2   *mux.Pane
	writes1 chan []byte
	writes2 chan []byte
}

func newAttachPaneProtocolHarness(t *testing.T) *attachPaneProtocolHarness {
	t.Helper()

	sess := newSession(t.Name())
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		sess.shutdown.Store(true)
		stopSessionBackgroundLoops(t, sess)
		for _, pane := range append([]*mux.Pane(nil), sess.Panes...) {
			if pane != nil {
				_ = pane.Close()
				_ = pane.WaitClosed()
			}
		}
	})

	writes1 := make(chan []byte, 4)
	writes2 := make(chan []byte, 4)
	pane1 := newAttachPaneProtocolPane(sess, 1, writes1)
	pane2 := newAttachPaneProtocolPane(sess, 2, writes2)
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	return &attachPaneProtocolHarness{
		srv:     &Server{sessions: map[string]*Session{sess.Name: sess}},
		sess:    sess,
		pane1:   pane1,
		pane2:   pane2,
		writes1: writes1,
		writes2: writes2,
	}
}

func newSingleAttachPaneProtocolHarness(t *testing.T) *attachPaneProtocolHarness {
	t.Helper()

	sess := newSession(t.Name())
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		sess.shutdown.Store(true)
		stopSessionBackgroundLoops(t, sess)
		for _, pane := range append([]*mux.Pane(nil), sess.Panes...) {
			if pane != nil {
				_ = pane.Close()
				_ = pane.WaitClosed()
			}
		}
	})

	writes := make(chan []byte, 4)
	pane := newAttachPaneProtocolPane(sess, 1, writes)
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	return &attachPaneProtocolHarness{
		srv:     &Server{sessions: map[string]*Session{sess.Name: sess}},
		sess:    sess,
		pane1:   pane,
		writes1: writes,
	}
}

func newAttachPaneProtocolPane(sess *Session, id uint32, writes chan<- []byte) *mux.Pane {
	return sess.ownPane(newProxyPane(id, mux.PaneMeta{
		Name:  fmt.Sprintf(mux.PaneNameFormat, id),
		Host:  mux.DefaultHost,
		Color: config.AccentColor(id - 1),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		writes <- append([]byte(nil), data...)
		return len(data), nil
	}))
}

func startAttachPaneProtocolConn(t *testing.T, h *attachPaneProtocolHarness, paneID uint32) net.Conn {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.srv.handleConn(serverConn)
	}()
	t.Cleanup(func() {
		clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("server connection did not close")
		}
	})

	if err := writeMsgOnConn(clientConn, &Message{
		Type:    MsgTypeAttachPane,
		Session: h.sess.Name,
		PaneID:  paneID,
	}); err != nil {
		t.Fatalf("write attach pane: %v", err)
	}
	waitForScopedClient(t, h.sess, paneID)
	return clientConn
}

func waitForScopedClient(t *testing.T, sess *Session, paneID uint32) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mustSessionQuery(t, sess, func(sess *Session) bool {
			for _, cc := range sess.ensureClientManager().snapshotClients() {
				if cc.isScopedToPane(paneID) {
					return true
				}
			}
			return false
		}) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for scoped client on pane %d", paneID)
}

func expectProtocolError(t *testing.T, conn net.Conn, contains string) {
	t.Helper()

	msg := readAttachPaneMsgWithTimeout(t, conn)
	if msg.Type != MsgTypeCmdResult {
		t.Fatalf("message type = %v, want CmdResult", msg.Type)
	}
	if !strings.Contains(msg.CmdErr, contains) {
		t.Fatalf("CmdErr = %q, want substring %q", msg.CmdErr, contains)
	}
}

func readAttachPaneMsgWithTimeout(t *testing.T, conn net.Conn) *Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() {
		if err := conn.SetReadDeadline(time.Time{}); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("reset read deadline: %v", err)
		}
	}()

	msg, err := readMsgOnConn(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	return msg
}

func expectConnectionClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		if strings.Contains(err.Error(), "closed") {
			return
		}
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() {
		if err := conn.SetReadDeadline(time.Time{}); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("reset read deadline: %v", err)
		}
	}()

	msg, err := readMsgOnConn(conn)
	if err == nil {
		t.Fatalf("ReadMsg succeeded with %+v, want closed connection", msg)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("connection stayed open")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "closed") {
		return
	}
	t.Fatalf("ReadMsg error = %v, want closed connection", err)
}

func TestMsgTypeListPanesReturnsLeafPaneSnapshot(t *testing.T) {
	t.Parallel()

	h := newAttachPaneProtocolHarness(t)
	orphan := newAttachPaneProtocolPane(h.sess, 99, make(chan []byte, 1))
	mustSessionMutation(t, h.sess, func(sess *Session) {
		sess.Panes = append(sess.Panes, orphan)
	})

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.srv.handleConn(serverConn)
	}()

	if err := writeMsgOnConn(clientConn, &Message{Type: MsgTypeListPanes, Session: h.sess.Name}); err != nil {
		t.Fatalf("write list panes: %v", err)
	}
	msg := readAttachPaneMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("message type = %v, want Layout", msg.Type)
	}
	if msg.Layout == nil {
		t.Fatal("layout is nil")
	}
	got := paneSnapshotIDs(msg.Layout.Panes)
	want := []uint32{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layout pane IDs = %v, want %v", got, want)
	}
	expectConnectionClosed(t, clientConn)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("list panes connection did not close")
	}
}

func TestScopedPaneProtocolRejectsMissingSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *Message
	}{
		{name: "list panes", msg: &Message{Type: MsgTypeListPanes, Session: "missing"}},
		{name: "attach pane", msg: &Message{Type: MsgTypeAttachPane, Session: "missing", PaneID: 1}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := &Server{sessions: map[string]*Session{}}
			serverConn, clientConn := net.Pipe()
			t.Cleanup(func() { clientConn.Close() })
			done := make(chan struct{})
			go func() {
				defer close(done)
				srv.handleConn(serverConn)
			}()

			if err := writeMsgOnConn(clientConn, tt.msg); err != nil {
				t.Fatalf("write message: %v", err)
			}
			expectProtocolError(t, clientConn, "no session")
			expectConnectionClosed(t, clientConn)

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("missing session connection did not close")
			}
		})
	}
}

func TestMsgTypeListPanesRejectsSessionWithoutLayout(t *testing.T) {
	t.Parallel()

	sess := newSession(t.Name())
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		sess.shutdown.Store(true)
		stopSessionBackgroundLoops(t, sess)
	})
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleConn(serverConn)
	}()

	if err := writeMsgOnConn(clientConn, &Message{Type: MsgTypeListPanes, Session: sess.Name}); err != nil {
		t.Fatalf("write list panes: %v", err)
	}
	expectProtocolError(t, clientConn, "no layout")
	expectConnectionClosed(t, clientConn)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("no-layout connection did not close")
	}
}

func TestMsgTypeAttachPaneScopesOutputAndInput(t *testing.T) {
	t.Parallel()

	h := newAttachPaneProtocolHarness(t)
	conn := startAttachPaneProtocolConn(t, h, h.pane2.ID)

	h.pane1.FeedOutput([]byte("pane-1-leak"))
	h.pane2.FeedOutput([]byte("pane-2-visible"))
	msg := readAttachPaneMsgWithTimeout(t, conn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("message type = %v, want PaneOutput", msg.Type)
	}
	if msg.PaneID != h.pane2.ID || string(msg.PaneData) != "pane-2-visible" {
		t.Fatalf("pane output = pane %d %q, want pane 2 visible output", msg.PaneID, msg.PaneData)
	}

	if err := writeMsgOnConn(conn, &Message{Type: MsgTypeInputPane, PaneID: h.pane2.ID, PaneData: []byte("targeted")}); err != nil {
		t.Fatalf("write scoped input: %v", err)
	}
	select {
	case got := <-h.writes2:
		if string(got) != "targeted" {
			t.Fatalf("pane 2 input = %q, want targeted", got)
		}
	case <-time.After(time.Second):
		t.Fatal("targeted input did not reach pane 2")
	}
	select {
	case got := <-h.writes1:
		t.Fatalf("pane 1 received unexpected input %q", got)
	default:
	}
}

func TestMsgTypeAttachPaneRejectsRestrictedMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *Message
	}{
		{name: "attach", msg: &Message{Type: MsgTypeAttach}},
		{name: "attach pane again", msg: &Message{Type: MsgTypeAttachPane, PaneID: 2}},
		{name: "command", msg: &Message{Type: MsgTypeCommand, CmdName: "split"}},
		{name: "resize", msg: &Message{Type: MsgTypeResize, Cols: 120, Rows: 40}},
		{name: "untargeted input", msg: &Message{Type: MsgTypeInput, Input: []byte("x")}},
		{name: "ui event", msg: &Message{Type: MsgTypeUIEvent, UIEvent: proto.UIEventInputBusy}},
		{name: "capture response", msg: &Message{Type: MsgTypeCaptureResponse, CmdOutput: "{}"}},
		{name: "list panes", msg: &Message{Type: MsgTypeListPanes}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newAttachPaneProtocolHarness(t)
			conn := startAttachPaneProtocolConn(t, h, h.pane2.ID)

			if err := writeMsgOnConn(conn, tt.msg); err != nil {
				t.Fatalf("write restricted message: %v", err)
			}
			expectProtocolError(t, conn, "restricted pane connection")
			expectConnectionClosed(t, conn)
		})
	}
}

func TestMsgTypeAttachPaneRejectsOtherPaneInputWithoutClosing(t *testing.T) {
	t.Parallel()

	h := newAttachPaneProtocolHarness(t)
	conn := startAttachPaneProtocolConn(t, h, h.pane2.ID)

	if err := writeMsgOnConn(conn, &Message{Type: MsgTypeInputPane, PaneID: h.pane1.ID, PaneData: []byte("wrong")}); err != nil {
		t.Fatalf("write other pane input: %v", err)
	}
	expectProtocolError(t, conn, "pane 1")

	select {
	case got := <-h.writes1:
		t.Fatalf("pane 1 received unexpected input %q", got)
	default:
	}
	h.pane2.FeedOutput([]byte("still-live"))
	msg := readAttachPaneMsgWithTimeout(t, conn)
	if msg.Type != MsgTypePaneOutput || msg.PaneID != h.pane2.ID || string(msg.PaneData) != "still-live" {
		t.Fatalf("message after nonfatal reject = %+v, want pane 2 output", msg)
	}
}

func TestMsgTypeAttachPanePaneExitSendsExitAndCloses(t *testing.T) {
	t.Parallel()

	h := newAttachPaneProtocolHarness(t)
	conn := startAttachPaneProtocolConn(t, h, h.pane2.ID)

	h.sess.enqueuePaneExit(h.pane2.ID, "exit 0")
	msg := readAttachPaneMsgWithTimeout(t, conn)
	if msg.Type != MsgTypeExit {
		t.Fatalf("message type = %v, want Exit", msg.Type)
	}
	expectConnectionClosed(t, conn)
}

func TestMsgTypeAttachPaneLastPaneExitSendsExitAndCloses(t *testing.T) {
	t.Parallel()

	h := newSingleAttachPaneProtocolHarness(t)
	conn := startAttachPaneProtocolConn(t, h, h.pane1.ID)

	h.sess.enqueuePaneExit(h.pane1.ID, "exit 0")
	msg := readAttachPaneMsgWithTimeout(t, conn)
	if msg.Type != MsgTypeExit {
		t.Fatalf("message type = %v, want Exit", msg.Type)
	}
	expectConnectionClosed(t, conn)
}

func TestMsgTypeAttachPaneRejectsMissingPane(t *testing.T) {
	t.Parallel()

	h := newAttachPaneProtocolHarness(t)

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.srv.handleConn(serverConn)
	}()

	if err := writeMsgOnConn(clientConn, &Message{
		Type:    MsgTypeAttachPane,
		Session: h.sess.Name,
		PaneID:  99,
	}); err != nil {
		t.Fatalf("write missing attach pane: %v", err)
	}
	expectProtocolError(t, clientConn, "pane 99")
	expectConnectionClosed(t, clientConn)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("missing pane connection did not close")
	}
}

func paneSnapshotIDs(panes []proto.PaneSnapshot) []uint32 {
	ids := make([]uint32, 0, len(panes))
	for _, pane := range panes {
		ids = append(ids, pane.ID)
	}
	return ids
}
