package server

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

func TestConnectionLogKeepsLastEntriesInOrder(t *testing.T) {
	t.Parallel()

	log := newConnectionLog(3)
	base := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		log.Append(ConnectionLogEntry{
			Timestamp:        base.Add(time.Duration(i) * time.Second),
			Event:            "attach",
			ClientID:         fmt.Sprintf("client-%d", i),
			Cols:             80 + i,
			Rows:             24 + i,
			DisconnectReason: "",
		})
	}

	got := log.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len(snapshot) = %d, want 3", len(got))
	}

	for i, wantID := range []string{"client-3", "client-4", "client-5"} {
		if got[i].ClientID != wantID {
			t.Fatalf("snapshot[%d].ClientID = %q, want %q", i, got[i].ClientID, wantID)
		}
	}
}

func TestConnectionLogAttachAndExplicitDetach(t *testing.T) {
	t.Parallel()

	sess, srv, pane := newConnectionLogAttachSession(t, "test-connection-log-explicit-detach")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleAttach(serverConn, &Message{
			Type:    MsgTypeAttach,
			Session: sess.Name,
			Cols:    90,
			Rows:    30,
		})
	}()

	drainAttachBootstrap(t, clientConn, pane.ID, 90, 30)

	if err := WriteMsg(clientConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit after explicit detach")
	}

	entries, err := sess.queryConnectionLog()
	if err != nil {
		t.Fatalf("queryConnectionLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(connection log) = %d, want 2", len(entries))
	}

	attach := entries[0]
	if attach.Event != "attach" {
		t.Fatalf("attach event = %q, want attach", attach.Event)
	}
	if attach.ClientID != "client-1" {
		t.Fatalf("attach client id = %q, want client-1", attach.ClientID)
	}
	if attach.Cols != 90 || attach.Rows != 30 {
		t.Fatalf("attach size = %dx%d, want 90x30", attach.Cols, attach.Rows)
	}
	if attach.Timestamp.IsZero() {
		t.Fatal("attach timestamp should be set")
	}
	if attach.DisconnectReason != "" {
		t.Fatalf("attach disconnect reason = %q, want empty", attach.DisconnectReason)
	}

	detach := entries[1]
	if detach.Event != "detach" {
		t.Fatalf("detach event = %q, want detach", detach.Event)
	}
	if detach.ClientID != "client-1" {
		t.Fatalf("detach client id = %q, want client-1", detach.ClientID)
	}
	if detach.Cols != 90 || detach.Rows != 30 {
		t.Fatalf("detach size = %dx%d, want 90x30", detach.Cols, detach.Rows)
	}
	if detach.Timestamp.IsZero() {
		t.Fatal("detach timestamp should be set")
	}
	if detach.DisconnectReason != "client detach" {
		t.Fatalf("detach reason = %q, want %q", detach.DisconnectReason, "client detach")
	}
}

func TestConnectionLogRecordsClosedConnectionReason(t *testing.T) {
	t.Parallel()

	sess, srv, pane := newConnectionLogAttachSession(t, "test-connection-log-closed-conn")
	serverConn, clientConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleAttach(serverConn, &Message{
			Type:    MsgTypeAttach,
			Session: sess.Name,
			Cols:    70,
			Rows:    20,
		})
	}()

	drainAttachBootstrap(t, clientConn, pane.ID, 70, 20)
	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit after connection close")
	}

	entries, err := sess.queryConnectionLog()
	if err != nil {
		t.Fatalf("queryConnectionLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(connection log) = %d, want 2", len(entries))
	}
	if got := entries[1].DisconnectReason; got != "connection closed" {
		t.Fatalf("disconnect reason = %q, want %q", got, "connection closed")
	}
}

func TestCmdConnectionLogFormatsEntriesAndEmptyState(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-connection-log-empty")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, nil, cmdConnectionLog)
		if got := msg.CmdOutput; got != "No client connections recorded.\n" {
			t.Fatalf("empty output = %q", got)
		}
	})

	t.Run("formatted rows", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-connection-log-command")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		base := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.connectionLog = newConnectionLog(100)
			sess.connectionLog.Append(ConnectionLogEntry{
				Timestamp:        base,
				Event:            "attach",
				ClientID:         "client-1",
				Cols:             80,
				Rows:             24,
				DisconnectReason: "",
			})
			sess.connectionLog.Append(ConnectionLogEntry{
				Timestamp:        base.Add(time.Second),
				Event:            "detach",
				ClientID:         "client-1",
				Cols:             80,
				Rows:             24,
				DisconnectReason: "client detach",
			})
			return struct{}{}
		})

		msg := runOneShotCommand(t, sess, nil, cmdConnectionLog)
		for _, want := range []string{
			"TS",
			"EVENT",
			"CLIENT",
			"COLS",
			"ROWS",
			"REASON",
			"2026-03-22T12:00:00Z",
			"attach",
			"detach",
			"client-1",
			"client detach",
		} {
			if !strings.Contains(msg.CmdOutput, want) {
				t.Fatalf("cmdConnectionLog missing %q:\n%s", want, msg.CmdOutput)
			}
		}
		if !strings.Contains(msg.CmdOutput, "-\n") && !strings.Contains(msg.CmdOutput, " -") {
			t.Fatalf("attach row should render placeholder reason:\n%s", msg.CmdOutput)
		}
	})
}

func newConnectionLogAttachSession(t *testing.T, name string) (*Session, *Server, *mux.Pane) {
	t.Helper()

	sess := newSession(name)
	stopCrashCheckpointLoop(t, sess)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	return sess, srv, pane
}

func drainAttachBootstrap(t *testing.T, conn net.Conn, paneID uint32, cols, rows int) {
	t.Helper()

	first := readMsgWithTimeout(t, conn)
	if first.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", first.Type)
	}
	if first.Layout == nil || first.Layout.Width != cols || first.Layout.Height != rows-render.GlobalBarHeight {
		t.Fatalf("initial layout size = %+v, want %dx%d", first.Layout, cols, rows-render.GlobalBarHeight)
	}

	output := readUntil(t, conn, func(msg *Message) bool {
		return msg.Type == MsgTypePaneOutput && msg.PaneID == paneID
	})
	if output.PaneID != paneID {
		t.Fatalf("pane output id = %d, want %d", output.PaneID, paneID)
	}

	readUntil(t, conn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == cols && msg.Layout.Height == rows-render.GlobalBarHeight
	})
}
