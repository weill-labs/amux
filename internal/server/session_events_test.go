package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func TestHandleAttachAndResizeThroughSessionQueue(t *testing.T) {
	t.Parallel()

	sess := newSession("test-attach-resize")
	stopCrashCheckpointLoop(t, sess)
	pane := newProxyPane(1, mux.PaneMeta{
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
	serverConn, peerConn := net.Pipe()
	defer peerConn.Close()

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

	msg := readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}
	if msg.Layout == nil || msg.Layout.Width != 80 || msg.Layout.Height != 23 {
		t.Fatalf("initial layout size = %dx%d, want 80x23", msg.Layout.Width, msg.Layout.Height)
	}

	msg = readMsgWithTimeout(t, peerConn)
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
	readUntil(t, peerConn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == 80 && msg.Layout.Height == 23
	})

	if err := WriteMsg(peerConn, &Message{Type: MsgTypeResize, Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("WriteMsg resize: %v", err)
	}

	resized := readUntil(t, peerConn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == 100 && msg.Layout.Height == 29
	})
	if resized.Layout.ActiveWindowID != w.ID {
		t.Fatalf("active window id = %d, want %d", resized.Layout.ActiveWindowID, w.ID)
	}

	if err := WriteMsg(peerConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit after detach")
	}
}

func TestHandleAttachEventEmitsClientConnect(t *testing.T) {
	t.Parallel()

	sess := newSession("test-attach-connect-event")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventClientConnect}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	cc := &clientConn{ID: "client-1", inputIdle: true}
	attachRes := sess.enqueueAttachClient(&Server{}, cc, 80, 24)
	if attachRes.err != nil {
		t.Fatalf("enqueueAttachClient: %v", attachRes.err)
	}

	select {
	case data := <-res.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Type != EventClientConnect || ev.ClientID != cc.ID {
			t.Fatalf("connect event = %+v, want client-connect for %s", ev, cc.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("client-connect event was not emitted")
	}
}

func TestHandleAttachSendsPaneHistoryBeforePaneOutput(t *testing.T) {
	t.Parallel()

	sess := newSession("test-attach-history")
	stopCrashCheckpointLoop(t, sess)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 2, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	pane.FeedOutput([]byte("line-1\r\nline-2\r\nline-3\r\nline-4\r\n"))

	w := mux.NewWindow(pane, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	serverConn, peerConn := net.Pipe()
	defer peerConn.Close()

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

	if msg := readMsgWithTimeout(t, peerConn); msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}

	msg := readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneHistory {
		t.Fatalf("second message type = %v, want pane history", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("history pane id = %d, want %d", msg.PaneID, pane.ID)
	}
	if len(msg.History) == 0 || msg.History[0] != "line-1" {
		t.Fatalf("history = %#v, want oldest line retained", msg.History)
	}

	msg = readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("third message type = %v, want pane output", msg.Type)
	}
	if !bytes.Contains(msg.PaneData, []byte("line-4")) {
		t.Fatalf("pane output = %q, want latest screen content", msg.PaneData)
	}

	readUntil(t, peerConn, func(msg *Message) bool {
		return msg.Type == MsgTypeLayout && msg.Layout != nil &&
			msg.Layout.Width == 80 && msg.Layout.Height == 23
	})

	if err := WriteMsg(peerConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit after detach")
	}
}

func TestResetCommandBroadcastsClearedHistoryAndBlankScreen(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "pane-1")
	pane.SetRetainedHistory([]string{"base-1", "base-2"})
	pane.FeedOutput([]byte("line-1\r\nline-2\r\nline-3"))

	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"

	serverConn, peerConn := net.Pipe()
	cc := newClientConn(serverConn)
	cc.ID = "client-1"
	defer cc.Close()
	defer peerConn.Close()
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.Windows = []*mux.Window{w}
		sess.ActiveWindowID = w.ID
		sess.Panes = []*mux.Pane{pane}
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}
	})

	res := runTestCommand(t, srv, sess, "reset", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("reset cmdErr = %q", res.cmdErr)
	}
	if !strings.Contains(res.output, "Reset pane-1") {
		t.Fatalf("reset output = %q, want reset confirmation", res.output)
	}

	msg := readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneHistory {
		t.Fatalf("first broadcast type = %v, want pane history", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("history pane id = %d, want %d", msg.PaneID, pane.ID)
	}
	if len(msg.History) != 0 {
		t.Fatalf("history after reset = %#v, want empty", msg.History)
	}

	msg = readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("second broadcast type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != pane.ID {
		t.Fatalf("pane output id = %d, want %d", msg.PaneID, pane.ID)
	}

	emu := mux.NewVTEmulatorWithDrainAndScrollback(80, 22, mux.DefaultScrollbackLines)
	if _, err := emu.Write(msg.PaneData); err != nil {
		t.Fatalf("emulator write: %v", err)
	}
	if got := mux.EmulatorContentLines(emu); len(got) != 22 || got[0] != "" {
		t.Fatalf("broadcast reset screen = %#v, want blank rows", got)
	}
}

func TestPaneOutputCallbackEnqueuesOutputNotifications(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-output")
	stopCrashCheckpointLoop(t, sess)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  "local",
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	sess.Panes = []*mux.Pane{pane}

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventOutput}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	waitCh := sess.enqueuePaneOutputSubscribe(pane.ID)
	defer sess.enqueuePaneOutputUnsubscribe(pane.ID, waitCh)

	sess.paneOutputCallback()(pane.ID, []byte("hello"), 1)

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

func TestCommandMutationBroadcastsLayoutBeforeQueuedPaneOutput(t *testing.T) {
	t.Parallel()

	sess := newSession("test-command-layout-before-output")
	t.Cleanup(func() { stopSessionBackgroundLoops(t, sess) })

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })

	w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })

	cc := newClientConn(serverConn)
	t.Cleanup(cc.Close)
	sess.ensureClientManager().setClientsForTest(cc)

	res := sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.enqueuePaneOutput(pane.ID, []byte("queued-output"), 1)
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		t.Fatalf("enqueueCommandMutation error = %v", res.err)
	}

	first := readMsgWithTimeout(t, peerConn)
	if first.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout before pane output", first.Type)
	}
	if first.Layout == nil || first.Layout.Width != 80 || first.Layout.Height != 23 {
		t.Fatalf("layout = %+v, want 80x23 snapshot", first.Layout)
	}

	second := readMsgWithTimeout(t, peerConn)
	if second.Type != MsgTypePaneOutput {
		t.Fatalf("second message type = %v, want pane output", second.Type)
	}
	if second.PaneID != pane.ID || string(second.PaneData) != "queued-output" {
		t.Fatalf("pane output = pane %d %q, want pane %d queued-output", second.PaneID, string(second.PaneData), pane.ID)
	}
}

func TestHandleAttachBroadcastsResizeLayoutBeforeQueuedPaneOutput(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newAttachTestPane(sess, 1, "pane-1", 80, 23)
	w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	w.ID = 1
	w.Name = "window-1"
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		_ = peerConn.Close()
	})

	existing := newClientConn(serverConn)
	existing.ID = "client-1"
	existing.cols = 80
	existing.rows = 24
	t.Cleanup(existing.Close)

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(existing)
		sess.hadClient = true
		sess.ensureClientManager().setSizeOwnerForTest(existing)
		return struct{}{}
	})

	attachConn, replay, paused, release, done := startPausedAttach(t, srv, sess, 120, 40)
	t.Cleanup(func() {
		release()
		_ = attachConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			// Best-effort cleanup for the red path: the failure this test cares
			// about is message ordering on the already-attached peer.
		}
	})

	readInitialAttachReplay(t, attachConn, replay)
	waitForPause(t, paused)

	pane.FeedOutput([]byte("resize-redraw"))

	first := readMsgWithTimeout(t, peerConn)
	if first.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout before queued pane output", first.Type)
	}
	if first.Layout == nil || first.Layout.Width != 120 || first.Layout.Height != 39 {
		t.Fatalf("layout = %+v, want 120x39 snapshot", first.Layout)
	}

	second := readMsgWithTimeout(t, peerConn)
	if second.Type != MsgTypePaneOutput {
		t.Fatalf("second message type = %v, want pane output", second.Type)
	}
	if second.PaneID != pane.ID || string(second.PaneData) != "resize-redraw" {
		t.Fatalf("pane output = pane %d %q, want pane %d resize-redraw", second.PaneID, string(second.PaneData), pane.ID)
	}
}

func TestMetaCallbackEnqueuesMetaUpdate(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-callback")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	task := "deploy"
	sess.metaCallback()(pane.ID, mux.MetaUpdate{Task: &task})

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return sess.Panes[0].Meta.Task == "deploy"
		})
	})
}

func TestMetaCallbackIgnoredDuringShutdown(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-shutdown")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	sess.Panes = []*mux.Pane{pane}

	sess.shutdown.Store(true)

	task := "should-not-apply"
	sess.metaCallback()(pane.ID, mux.MetaUpdate{Task: &task})

	// The callback early-returns during shutdown, so the event is never enqueued.
	// Verify by doing a round-trip through the event loop — if the meta event
	// were queued, it would be processed before our query.
	got := mustSessionQuery(t, sess, func(sess *Session) string {
		return sess.Panes[0].Meta.Task
	})
	if got == "should-not-apply" {
		t.Fatal("metaCallback should be suppressed during shutdown")
	}
}

func TestPaneExitCallbackEnqueuesRemoval(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-exit")
	stopCrashCheckpointLoop(t, sess)
	p1 := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: "local", Color: "f5e0dc"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	p2 := newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: "local", Color: "f2cdcd"}, 80, 23, nil, nil, func(data []byte) (int, error) {
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

	sess.paneExitCallback()(p2.ID, "exit 0")

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
			return len(sess.Windows) == 1 && len(sess.Panes) == 1 && sess.activeWindow() != nil
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
	_ = pane.Close()
	_ = pane.WaitClosed()
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
	_ = pane.Close()
	_ = pane.WaitClosed()
}

func TestEnsureInitialWindowReusesOrphanedPanes(t *testing.T) {
	t.Parallel()

	sess := newSession("test-managed-startup-orphans")
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	defer stopSessionBackgroundLoops(t, sess)

	orphans := []*mux.Pane{
		newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil }),
		newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: "f2cdcd"}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil }),
	}
	dormant := newProxyPane(3, mux.PaneMeta{Name: "pane-3", Host: mux.DefaultHost, Color: "b4befe", Dormant: true}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.Panes = append(sess.Panes, orphans...)
		sess.Panes = append(sess.Panes, dormant)
		return struct{}{}
	})

	if err := srv.EnsureInitialWindow(80, 24); err != nil {
		t.Fatalf("EnsureInitialWindow: %v", err)
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		if len(sess.Windows) != 1 {
			t.Fatalf("window count = %d, want 1", len(sess.Windows))
		}
		if len(sess.Panes) != len(orphans)+1 {
			t.Fatalf("pane count = %d, want %d", len(sess.Panes), len(orphans)+1)
		}
		for _, pane := range orphans {
			if sess.findWindowByPaneID(pane.ID) == nil {
				t.Fatalf("pane %d should be rehabilitated into a window", pane.ID)
			}
		}
		if sess.findWindowByPaneID(dormant.ID) != nil {
			t.Fatalf("dormant pane %d should stay out of the recovery window", dormant.ID)
		}
		return struct{}{}
	})
	assertSessionLayoutConsistent(t, sess)
}

func TestRecoverInitialWindowFromOrphansLockedIsNoOpWhenWindowExists(t *testing.T) {
	t.Parallel()

	pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	sess := newSession("test-managed-startup-orphans-noop")
	defer stopSessionBackgroundLoops(t, sess)
	window := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
	window.ID = 1
	window.Name = "window-1"
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	recovered, err := sess.recoverInitialWindowFromOrphansLocked(80, 24)
	if err != nil {
		t.Fatalf("recoverInitialWindowFromOrphansLocked: %v", err)
	}
	if recovered {
		t.Fatal("recoverInitialWindowFromOrphansLocked = true, want false")
	}
}

func TestEnsureInitialWindowReturnsOrphanRecoveryError(t *testing.T) {
	t.Parallel()

	sess := newSession("test-managed-startup-orphan-error")
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	defer stopSessionBackgroundLoops(t, sess)

	orphans := []*mux.Pane{
		newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 4, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil }),
		newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: "f2cdcd"}, 4, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil }),
	}
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.Panes = append(sess.Panes, orphans...)
		return struct{}{}
	})

	err := srv.EnsureInitialWindow(4, 24)
	if err == nil {
		t.Fatal("EnsureInitialWindow error = nil, want orphan recovery split error")
	}
	if !strings.Contains(err.Error(), "not enough space to split") {
		t.Fatalf("EnsureInitialWindow error = %q, want split error", err)
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		if len(sess.Windows) != 0 {
			t.Fatalf("window count = %d, want 0", len(sess.Windows))
		}
		for _, pane := range orphans {
			if sess.findWindowByPaneID(pane.ID) != nil {
				t.Fatalf("pane %d should remain orphaned after recovery error", pane.ID)
			}
		}
		return struct{}{}
	})
}

func TestEnsureInitialWindowReturnsPaneCreationError(t *testing.T) {
	t.Setenv("SHELL", "/definitely/missing-shell")

	sess := newSession("test-managed-startup-error")
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	defer stopSessionBackgroundLoops(t, sess)

	if err := srv.EnsureInitialWindow(80, 24); err == nil {
		t.Fatal("EnsureInitialWindow error = nil, want pane creation error")
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		if len(sess.Windows) != 0 {
			t.Fatalf("window count = %d, want 0", len(sess.Windows))
		}
		if len(sess.Panes) != 0 {
			t.Fatalf("pane count = %d, want 0", len(sess.Panes))
		}
		return struct{}{}
	})
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
		resultCh <- sess.enqueueAttachClient(&Server{}, newClientConn(nil), 80, 24)
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

func TestDetachClientEventEmitsDisconnectReason(t *testing.T) {
	t.Parallel()

	sess := newSession("test-detach-disconnect-event")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := &clientConn{ID: "client-1", inputIdle: true}
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}
	})

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventClientDisconnect}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	sess.enqueueDetachClient(cc, DisconnectReasonExplicitDetach)

	select {
	case data := <-res.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Type != EventClientDisconnect || ev.ClientID != cc.ID || ev.Reason != DisconnectReasonExplicitDetach {
			t.Fatalf("disconnect event = %+v, want explicit detach for %s", ev, cc.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("client-disconnect event was not emitted")
	}
}

func TestDisconnectClientsForReloadEmitsDisconnectWithoutLayoutMutation(t *testing.T) {
	t.Parallel()

	sess := newSession("test-reload-disconnect-events")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 100, 29, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 100, 29)
	w.ID = 1
	w.Name = "window-1"

	cc1 := &clientConn{ID: "client-1", cols: 100, rows: 30, inputIdle: true}
	cc2 := &clientConn{ID: "client-2", cols: 80, rows: 24, inputIdle: true}
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.Windows = []*mux.Window{w}
		sess.ActiveWindowID = w.ID
		sess.Panes = []*mux.Pane{pane}
		sess.ensureClientManager().setClientsForTest(cc1, cc2)
		sess.ensureClientManager().setSizeOwnerForTest(cc1)
		return struct{}{}
	})

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventClientDisconnect}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.disconnectClientsForReload([]*clientConn{cc1, cc2})
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("disconnectClientsForReload: %v", err)
	}

	for _, wantID := range []string{cc1.ID, cc2.ID} {
		select {
		case data := <-res.sub.ch:
			var ev Event
			if err := json.Unmarshal(data, &ev); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			if ev.Type != EventClientDisconnect || ev.ClientID != wantID || ev.Reason != DisconnectReasonServerReload {
				t.Fatalf("disconnect event = %+v, want server-reload for %s", ev, wantID)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for reload disconnect event for %s", wantID)
		}
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		clientCount int
		sizeOwner   *clientConn
		width       int
		height      int
		generation  uint64
	} {
		return struct {
			clientCount int
			sizeOwner   *clientConn
			width       int
			height      int
			generation  uint64
		}{
			clientCount: sess.ensureClientManager().clientCount(),
			sizeOwner:   sess.currentSizeClient(),
			width:       sess.activeWindow().Width,
			height:      sess.activeWindow().Height,
			generation:  sess.generation.Load(),
		}
	})
	if state.clientCount != 0 {
		t.Fatalf("client count after reload disconnect = %d, want 0", state.clientCount)
	}
	if state.sizeOwner != nil {
		t.Fatalf("size owner after reload disconnect = %v, want nil", state.sizeOwner)
	}
	if state.width != 100 || state.height != 29 {
		t.Fatalf("window size after reload disconnect = %dx%d, want 100x29", state.width, state.height)
	}
	if state.generation != 0 {
		t.Fatalf("generation after reload disconnect = %d, want 0", state.generation)
	}
}

func TestEnqueueUIWaitSubscribeAvoidsStaleSnapshotGap(t *testing.T) {
	t.Parallel()

	sess := newSession("test-ui-wait-subscribe")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := &clientConn{ID: "client-1", copyModeShown: true, inputIdle: true}
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}
	})

	stale, err := sess.queryUIClient("", proto.UIEventCopyModeHidden)
	if err != nil {
		t.Fatalf("queryUIClient: %v", err)
	}
	if stale.currentMatch {
		t.Fatal("stale hidden snapshot matched while copy mode was still shown")
	}
	if stale.currentGen != 0 {
		t.Fatalf("stale currentGen = %d, want 0 before UI changes", stale.currentGen)
	}

	sess.enqueueUIEvent(cc, proto.UIEventCopyModeHidden)
	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			clients := sess.ensureClientManager().snapshotClients()
			return len(clients) == 1 && !clients[0].copyModeShown
		})
	})

	naiveSub := sess.enqueueEventSubscribe(eventFilter{
		Types:    []string{proto.UIEventCopyModeHidden},
		ClientID: cc.ID,
	}, false)
	defer sess.enqueueEventUnsubscribe(naiveSub.sub)
	select {
	case <-naiveSub.sub.ch:
		t.Fatal("naive subscribe unexpectedly observed an already-emitted hidden event")
	default:
	}

	atomicSub, err := sess.enqueueUIWaitSubscribe("", proto.UIEventCopyModeHidden)
	if err != nil {
		t.Fatalf("enqueueUIWaitSubscribe: %v", err)
	}
	defer sess.enqueueEventUnsubscribe(atomicSub.sub)
	if atomicSub.clientID != cc.ID {
		t.Fatalf("client id = %q, want %q", atomicSub.clientID, cc.ID)
	}
	if !atomicSub.currentMatch {
		t.Fatal("atomic subscribe should see the already-hidden state")
	}
	if atomicSub.currentGen != 1 {
		t.Fatalf("atomic currentGen = %d, want 1 after hide", atomicSub.currentGen)
	}

	sess.enqueueUIEvent(cc, proto.UIEventCopyModeShown)
	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			clients := sess.ensureClientManager().snapshotClients()
			return len(clients) == 1 && clients[0].copyModeShown
		})
	})

	futureSub, err := sess.enqueueUIWaitSubscribe("", proto.UIEventCopyModeHidden)
	if err != nil {
		t.Fatalf("enqueueUIWaitSubscribe future: %v", err)
	}
	defer sess.enqueueEventUnsubscribe(futureSub.sub)
	if futureSub.currentMatch {
		t.Fatal("hidden state should not match while copy mode is shown")
	}
	if futureSub.currentGen != 2 {
		t.Fatalf("future currentGen = %d, want 2 after show", futureSub.currentGen)
	}

	sess.enqueueUIEvent(cc, proto.UIEventCopyModeHidden)
	select {
	case data := <-futureSub.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Type != proto.UIEventCopyModeHidden || ev.ClientID != cc.ID {
			t.Fatalf("future event = %+v, want copy-mode-hidden on %s", ev, cc.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("atomic subscribe did not receive future hidden event")
	}
}

func TestMetaUpdateEventSetsTaskAndPR(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-update-task-pr")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	task := "deploy"
	pr := "99"
	metaUpdateEvent{paneID: 1, update: mux.MetaUpdate{Task: &task, PR: &pr}}.handle(sess)

	if pane.Meta.Task != "deploy" {
		t.Fatalf("task = %q, want %q", pane.Meta.Task, "deploy")
	}
	if pane.Meta.PR != "99" {
		t.Fatalf("pr = %q, want %q", pane.Meta.PR, "99")
	}
}

func TestMetaUpdateEventSetsBranchManual(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-update-branch")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	branch := "feat/manual"
	metaUpdateEvent{paneID: 1, update: mux.MetaUpdate{Branch: &branch}}.handle(sess)

	if pane.Meta.GitBranch != "feat/manual" {
		t.Fatalf("git_branch = %q, want %q", pane.Meta.GitBranch, "feat/manual")
	}

	// Auto-detect should not override manual branch
	pane.ApplyCwdBranch("/tmp", "auto-branch")
	if pane.Meta.GitBranch != "feat/manual" {
		t.Fatalf("git_branch after auto-detect = %q, want manual to be preserved", pane.Meta.GitBranch)
	}
}

func TestMetaUpdateEventClearsBranch(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-clear-branch")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:      "pane-1",
		Host:      mux.DefaultHost,
		Color:     "f5e0dc",
		GitBranch: "old-branch",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	pane.SetMetaManualBranch(true)
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	empty := ""
	metaUpdateEvent{paneID: 1, update: mux.MetaUpdate{Branch: &empty}}.handle(sess)

	if pane.Meta.GitBranch != "" {
		t.Fatalf("git_branch = %q, want empty after clear", pane.Meta.GitBranch)
	}

	// After clearing, auto-detect should work again
	pane.ApplyCwdBranch("/tmp", "auto-branch")
	if pane.Meta.GitBranch != "auto-branch" {
		t.Fatalf("git_branch = %q, want auto-detect to resume after clear", pane.Meta.GitBranch)
	}
}

func TestMetaUpdateEventIgnoresMissingPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-meta-missing-pane")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	// No panes in session; should not panic
	task := "x"
	metaUpdateEvent{paneID: 999, update: mux.MetaUpdate{Task: &task}}.handle(sess)
}

func TestCwdBranchResultEventUpdatesPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-cwd-branch-result")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	cwdBranchResultEvent{paneID: 1, cwd: "/home/user/repo", branch: "main"}.handle(sess)

	if pane.LiveCwd() != "/home/user/repo" {
		t.Fatalf("liveCwd = %q, want %q", pane.LiveCwd(), "/home/user/repo")
	}
	if pane.Meta.GitBranch != "main" {
		t.Fatalf("git_branch = %q, want %q", pane.Meta.GitBranch, "main")
	}
}

func TestCwdBranchResultEventIgnoresMissingPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-cwd-branch-missing")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	// No panes; should not panic
	cwdBranchResultEvent{paneID: 999, cwd: "/tmp", branch: "main"}.handle(sess)
}

func TestUIEventCmdIncrementsClientGenerationOnlyOnRealChanges(t *testing.T) {
	t.Parallel()

	sess := newSession("test-ui-event-generation")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := &clientConn{ID: "client-1", inputIdle: true}

	uiEventCmd{cc: cc, uiEvent: proto.UIEventInputBusy}.handle(sess)
	if cc.uiGeneration != 1 {
		t.Fatalf("uiGeneration after first busy = %d, want 1", cc.uiGeneration)
	}

	uiEventCmd{cc: cc, uiEvent: proto.UIEventInputBusy}.handle(sess)
	if cc.uiGeneration != 1 {
		t.Fatalf("uiGeneration after duplicate busy = %d, want 1", cc.uiGeneration)
	}

	uiEventCmd{cc: cc, uiEvent: proto.UIEventInputIdle}.handle(sess)
	if cc.uiGeneration != 2 {
		t.Fatalf("uiGeneration after idle = %d, want 2", cc.uiGeneration)
	}
}

func TestUIEventCmdClientFocusAlwaysIncrementsGeneration(t *testing.T) {
	t.Parallel()

	sess := newSession("test-ui-event-client-focus")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := &clientConn{ID: "client-1", inputIdle: true}

	uiEventCmd{cc: cc, uiEvent: proto.UIEventClientFocusGained}.handle(sess)
	if cc.uiGeneration != 1 {
		t.Fatalf("uiGeneration after first client focus = %d, want 1", cc.uiGeneration)
	}

	uiEventCmd{cc: cc, uiEvent: proto.UIEventClientFocusGained}.handle(sess)
	if cc.uiGeneration != 2 {
		t.Fatalf("uiGeneration after second client focus = %d, want 2", cc.uiGeneration)
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
