package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

func TestHandleAttachAndResizeThroughSessionQueue(t *testing.T) {
	t.Parallel()

	sess := newSession("test-attach-resize")
	stopCrashCheckpointLoop(t, sess)
	pane := mux.NewProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	pane.FeedOutput([]byte("hello from pane"))

	w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleAttach(serverConn, &Message{
			Type:    MsgTypeAttach,
			Session: sess.Name,
			Cols:    80,
			Rows:    24,
		})
	}()

	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}
	if msg.Layout == nil || msg.Layout.Width != 80 || msg.Layout.Height != 23 {
		t.Fatalf("initial layout size = %dx%d, want 80x23", msg.Layout.Width, msg.Layout.Height)
	}

	msg = readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("second message type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("pane output id = %d, want %d", msg.PaneID, pane.ID)
	}
	if !bytes.Contains(msg.PaneData, []byte("hello from pane")) {
		t.Fatalf("pane output = %q, want rendered content", msg.PaneData)
	}

	// handleAttach broadcasts a post-attach layout after the initial snapshot.
	readUntil(t, clientConn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == 80 && msg.Layout.Height == 23
	})

	if err := WriteMsg(clientConn, &Message{Type: MsgTypeResize, Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("WriteMsg resize: %v", err)
	}

	resized := readUntil(t, clientConn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == 100 && msg.Layout.Height == 29
	})
	if resized.Layout.ActiveWindowID != w.ID {
		t.Fatalf("active window id = %d, want %d", resized.Layout.ActiveWindowID, w.ID)
	}

	if err := WriteMsg(clientConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit after detach")
	}
}

func TestPaneOutputCallbackEnqueuesOutputNotifications(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-output")
	stopCrashCheckpointLoop(t, sess)
	pane := mux.NewProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  "local",
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	sess.Panes = []*mux.Pane{pane}

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventOutput}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	waitCh := sess.enqueuePaneOutputSubscribe(pane.ID)
	defer sess.enqueuePaneOutputUnsubscribe(pane.ID, waitCh)

	sess.paneOutputCallback()(pane.ID, []byte("hello"))

	select {
	case <-waitCh:
	case <-time.After(time.Second):
		t.Fatal("wait-for subscriber was not notified")
	}

	select {
	case data := <-res.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Type != EventOutput || ev.PaneName != "pane-1" || ev.Host != "local" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("output event was not emitted")
	}
}

func TestPaneExitCallbackEnqueuesRemoval(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-exit")
	stopCrashCheckpointLoop(t, sess)
	p1 := mux.NewProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: "local", Color: "f5e0dc"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	p2 := mux.NewProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: "local", Color: "f2cdcd"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	if _, err := w.Split(mux.SplitVertical, p2); err != nil {
		t.Fatalf("Split: %v", err)
	}
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	sess.paneExitCallback()(p2.ID)

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return !sess.hasPane(p2.ID) && w.PaneCount() == 1
		})
	})
}

func TestEnsureInitialWindowCreatesPaneWithoutClient(t *testing.T) {
	t.Parallel()

	sess := newSession("test-managed-startup")
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	defer stopSessionBackgroundLoops(t, sess)

	if err := srv.EnsureInitialWindow(80, 24); err != nil {
		t.Fatalf("EnsureInitialWindow: %v", err)
	}

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return len(sess.Windows) == 1 && len(sess.Panes) == 1 && sess.ActiveWindow() != nil
		})
	})

	pane := mustSessionQuery(t, sess, func(sess *Session) *mux.Pane {
		if sess.ActiveWindowID == 0 {
			t.Fatal("active window id = 0, want non-zero")
		}
		return sess.Panes[0]
	})
	if pane.Meta.Name != "pane-1" {
		t.Fatalf("pane name = %q, want pane-1", pane.Meta.Name)
	}

	sess.shutdown.Store(true)
	pane.Close()
}

func TestEnsureInitialWindowReturnsErrorWithoutSession(t *testing.T) {
	t.Parallel()

	srv := &Server{sessions: map[string]*Session{}}
	if err := srv.EnsureInitialWindow(80, 24); err == nil || err.Error() != "no session" {
		t.Fatalf("EnsureInitialWindow error = %v, want no session", err)
	}
}

func TestEnsureInitialWindowIsNoOpWhenSessionAlreadyInitialized(t *testing.T) {
	t.Parallel()

	sess := newSession("test-managed-startup-noop")
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	defer stopSessionBackgroundLoops(t, sess)

	if err := srv.EnsureInitialWindow(80, 24); err != nil {
		t.Fatalf("first EnsureInitialWindow: %v", err)
	}
	if err := srv.EnsureInitialWindow(120, 40); err != nil {
		t.Fatalf("second EnsureInitialWindow: %v", err)
	}

	pane := mustSessionQuery(t, sess, func(sess *Session) *mux.Pane {
		if len(sess.Windows) != 1 {
			t.Fatalf("window count = %d, want 1", len(sess.Windows))
		}
		if len(sess.Panes) != 1 {
			t.Fatalf("pane count = %d, want 1", len(sess.Panes))
		}
		return sess.Panes[0]
	})

	sess.shutdown.Store(true)
	pane.Close()
}

func TestEnqueueAttachClientReturnsOnSessionShutdown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}

	resultCh := make(chan attachResult, 1)
	go func() {
		resultCh <- sess.enqueueAttachClient(&Server{}, NewClientConn(nil), 80, 24)
	}()

	waitUntil(t, func() bool {
		return len(sess.sessionEvents) == 1
	})

	close(sess.sessionEventDone)

	select {
	case res := <-resultCh:
		if !errors.Is(res.err, errSessionShuttingDown) {
			t.Fatalf("attach error = %v, want %v", res.err, errSessionShuttingDown)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueueAttachClient did not return after shutdown")
	}
}

func readMsgWithTimeout(t *testing.T, conn net.Conn) *Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	msg, err := ReadMsg(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	return msg
}

func readUntil(t *testing.T, conn net.Conn, want func(*Message) bool) *Message {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		msg := readMsgWithTimeout(t, conn)
		if want(msg) {
			return msg
		}
	}
	t.Fatal("timeout waiting for matching message")
	return nil
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
