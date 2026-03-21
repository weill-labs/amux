package server

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClientConnQueuesBroadcastsDuringBootstrap(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)
	cc.startBootstrap()

	layout := &Message{
		Type: MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{
			Width:  80,
			Height: 23,
		},
	}
	cc.sendBroadcast(layout)
	cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("live-output")}, 7, 9)

	assertNoClientMessage(t, clientConn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(map[uint32]uint64{7: 5})
	}()

	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypeLayout {
		t.Fatalf("first message type = %v, want layout", msg.Type)
	}
	if msg.Layout == nil || msg.Layout.Width != 80 || msg.Layout.Height != 23 {
		t.Fatalf("layout = %+v, want 80x23", msg.Layout)
	}

	msg = readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("second message type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != 7 || string(msg.PaneData) != "live-output" {
		t.Fatalf("pane output = pane %d %q, want pane 7 live-output", msg.PaneID, string(msg.PaneData))
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}
}

func TestClientConnDropsStaleQueuedPaneOutputAfterBootstrap(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)
	cc.startBootstrap()
	cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 3, PaneData: []byte("stale")}, 3, 5)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(map[uint32]uint64{3: 5})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}

	assertNoClientMessage(t, clientConn)

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 3, PaneData: []byte("fresh")}, 3, 6)
	}()
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("sendPaneOutput blocked before client read")
	}
	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("message type = %v, want pane output", msg.Type)
	}
	if string(msg.PaneData) != "fresh" {
		t.Fatalf("pane output = %q, want fresh", string(msg.PaneData))
	}
}

func TestClientConnSendPaneOutputDoesNotBlockOnUnreadClient(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 9, PaneData: []byte("hello")}, 9, 1)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sendPaneOutput blocked on unread client")
	}

	msg := readMsgWithTimeout(t, clientConn)
	if msg.Type != MsgTypePaneOutput {
		t.Fatalf("message type = %v, want pane output", msg.Type)
	}
	if msg.PaneID != 9 || string(msg.PaneData) != "hello" {
		t.Fatalf("pane output = pane %d %q, want pane 9 hello", msg.PaneID, string(msg.PaneData))
	}
}

func TestClientConnBootstrappingStateTracksLifecycle(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	if cc.isBootstrapping() {
		t.Fatal("new client should not report bootstrapping")
	}

	cc.startBootstrap()
	if !cc.isBootstrapping() {
		t.Fatal("startBootstrap should report bootstrapping")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.finishBootstrap(nil)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishBootstrap did not return")
	}

	if cc.isBootstrapping() {
		t.Fatal("finishBootstrap should clear bootstrapping state")
	}
}

func TestClientConnInputDoesNotBlockOnBusySessionActor(t *testing.T) {
	t.Parallel()

	sess := newSession("test-client-input-fast-path")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	writes := make(chan []byte, 1)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) {
		writes <- append([]byte(nil), data...)
		return len(data), nil
	})
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.Windows = []*mux.Window{w}
		sess.ActiveWindowID = w.ID
		sess.Panes = []*mux.Pane{pane}
		return commandMutationResult{}
	})
	if res.err != nil {
		t.Fatalf("setup session: %v", res.err)
	}

	release := make(chan struct{})
	blocker := blockingSessionEvent{entered: make(chan struct{}), release: release}
	if !sess.enqueueEvent(blocker) {
		t.Fatal("enqueue blocking event")
	}
	select {
	case <-blocker.entered:
	case <-time.After(time.Second):
		t.Fatal("blocking event did not start")
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	t.Cleanup(func() { serverConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.readLoop(&Server{}, sess)
	}()

	if err := WriteMsg(clientConn, &Message{Type: MsgTypeInput, Input: []byte("hello")}); err != nil {
		t.Fatalf("WriteMsg input: %v", err)
	}

	select {
	case got := <-writes:
		if string(got) != "hello" {
			t.Fatalf("input write = %q, want hello", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("input blocked on busy session actor")
	}

	close(release)
	if err := WriteMsg(clientConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not exit after detach")
	}
}

func TestClientConnActiveInputPaneForWriteSwitchesSessionSizeToLatestClient(t *testing.T) {
	t.Parallel()

	sess := newSession("test-client-input-latest-size")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"

	owner := NewClientConn(discardConn{})
	t.Cleanup(owner.Close)
	owner.ID = "client-1"
	owner.cols = 80
	owner.rows = 24

	cc := NewClientConn(discardConn{})
	t.Cleanup(cc.Close)
	cc.ID = "client-2"
	cc.cols = 60
	cc.rows = 20

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.Windows = []*mux.Window{w}
		sess.ActiveWindowID = w.ID
		sess.Panes = []*mux.Pane{pane}
		sess.clients = []*ClientConn{owner, cc}
		sess.sizeClient.Store(owner)
		return commandMutationResult{}
	})
	if res.err != nil {
		t.Fatalf("setup session: %v", res.err)
	}

	if got := cc.activeInputPaneForWrite(sess); got != pane {
		t.Fatalf("activeInputPaneForWrite = %v, want pane-1", got)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		width  int
		height int
		owner  *ClientConn
	} {
		w := sess.ActiveWindow()
		return struct {
			width  int
			height int
			owner  *ClientConn
		}{
			width:  w.Width,
			height: w.Height,
			owner:  sess.currentSizeClient(),
		}
	})
	if state.width != 60 || state.height != 19 {
		t.Fatalf("active window size = %dx%d, want 60x19", state.width, state.height)
	}
	if state.owner != cc {
		t.Fatalf("size owner = %v, want client-2", state.owner)
	}
}

func TestClientConnInputTargetTracksFocusAndWindowChanges(t *testing.T) {
	t.Parallel()

	sess := newSession("test-client-input-target")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane1Writes := make(chan []byte, 1)
	pane2Writes := make(chan []byte, 1)
	pane3Writes := make(chan []byte, 1)
	pane1 := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		pane1Writes <- append([]byte(nil), data...)
		return len(data), nil
	})
	pane2 := newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: "f2cdcd"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		pane2Writes <- append([]byte(nil), data...)
		return len(data), nil
	})
	pane3 := newProxyPane(3, mux.PaneMeta{Name: "pane-3", Host: mux.DefaultHost, Color: "f5c2e7"}, 80, 23, nil, nil, func(data []byte) (int, error) {
		pane3Writes <- append([]byte(nil), data...)
		return len(data), nil
	})

	window1 := mux.NewWindow(pane1, 80, 23)
	window1.ID = 1
	window1.Name = "window-1"
	if _, err := window1.Split(mux.SplitHorizontal, pane2); err != nil {
		t.Fatalf("Split: %v", err)
	}
	window1.FocusPane(pane1)

	window2 := mux.NewWindow(pane3, 80, 23)
	window2.ID = 2
	window2.Name = "window-2"

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.Windows = []*mux.Window{window1, window2}
		sess.ActiveWindowID = window1.ID
		sess.Panes = []*mux.Pane{pane1, pane2, pane3}
		return commandMutationResult{}
	})
	if res.err != nil {
		t.Fatalf("setup session: %v", res.err)
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	t.Cleanup(func() { serverConn.Close() })

	cc := NewClientConn(serverConn)
	t.Cleanup(cc.Close)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.readLoop(&Server{}, sess)
	}()

	assertReadLoopInputWrite(t, clientConn, pane1Writes, "one")

	res = sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		window1.FocusPane(pane2)
		return commandMutationResult{}
	})
	if res.err != nil {
		t.Fatalf("focus pane-2: %v", res.err)
	}
	assertReadLoopInputWrite(t, clientConn, pane2Writes, "two")

	res = sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.ActiveWindowID = window2.ID
		return commandMutationResult{}
	})
	if res.err != nil {
		t.Fatalf("switch window: %v", res.err)
	}
	assertReadLoopInputWrite(t, clientConn, pane3Writes, "three")

	if err := WriteMsg(clientConn, &Message{Type: MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not exit after detach")
	}
}

type blockingSessionEvent struct {
	entered chan struct{}
	release <-chan struct{}
}

func (e blockingSessionEvent) handle(*Session) {
	close(e.entered)
	<-e.release
}

func assertReadLoopInputWrite(t *testing.T, conn net.Conn, writes <-chan []byte, input string) {
	t.Helper()

	if err := WriteMsg(conn, &Message{Type: MsgTypeInput, Input: []byte(input)}); err != nil {
		t.Fatalf("WriteMsg input: %v", err)
	}

	select {
	case got := <-writes:
		if string(got) != input {
			t.Fatalf("input write = %q, want %q", string(got), input)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for input %q to reach pane", input)
	}
}

func assertNoClientMessage(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := ReadMsg(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			t.Fatalf("reset read deadline: %v", err)
		}
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("reset read deadline: %v", err)
	}
	if err != nil {
		t.Fatalf("ReadMsg unexpected error: %v", err)
	}
	t.Fatalf("unexpected message during bootstrap: %+v", msg)
}
