//go:build !race

package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

func findCrashPaneState(t *testing.T, cp *checkpoint.CrashCheckpoint, name string) checkpoint.CrashPaneState {
	t.Helper()

	for _, ps := range cp.PaneStates {
		if ps.Meta.Name == name {
			return ps
		}
	}
	t.Fatalf("pane state for %q not found", name)
	return checkpoint.CrashPaneState{}
}

func newCaptureTestClient(t *testing.T) (*clientConn, net.Conn, func()) {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	cc := newClientConn(serverConn)
	cleanup := func() {
		cc.Close()
		_ = peerConn.Close()
	}
	return cc, peerConn, cleanup
}

func TestCrashCheckpointBuildAndWrite(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	startedAt := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	sess.startedAt = startedAt
	sess.counter.Store(2)
	sess.windowCounter.Store(1)
	sess.generation.Store(7)

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	pane2.Meta.Minimized = true
	pane1.FeedOutput([]byte("hello from pane-1\n"))
	pane2.FeedOutput([]byte("hello from pane-2\n"))

	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	cp := sess.buildCrashCheckpoint()
	if cp == nil {
		t.Fatal("buildCrashCheckpoint() = nil")
	}
	if cp.SessionName != sess.Name {
		t.Fatalf("SessionName = %q, want %q", cp.SessionName, sess.Name)
	}
	if cp.Counter != 2 || cp.WindowCounter != 1 || cp.Generation != 7 {
		t.Fatalf("unexpected checkpoint counters: %#v", cp)
	}
	if len(cp.PaneStates) != 2 {
		t.Fatalf("PaneStates = %d, want 2", len(cp.PaneStates))
	}
	if cp.Layout.ActiveWindowID != window.ID {
		t.Fatalf("Layout.ActiveWindowID = %d, want %d", cp.Layout.ActiveWindowID, window.ID)
	}

	pane1State := findCrashPaneState(t, cp, "pane-1")
	if !pane1State.IsProxy || !strings.Contains(pane1State.Screen, "hello from pane-1") {
		t.Fatalf("pane-1 state = %#v, want proxy screen snapshot", pane1State)
	}

	pane2State := findCrashPaneState(t, cp, "pane-2")
	if !pane2State.Meta.Minimized || pane2State.Cols == 0 || pane2State.Rows == 0 {
		t.Fatalf("pane-2 minimized state = %#v, want minimized dimensions", pane2State)
	}

	sess.writeCrashCheckpoint()
	path := checkpoint.CrashCheckpointPathTimestamped(sess.Name, startedAt)
	saved, err := checkpoint.ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash(%s): %v", path, err)
	}
	if saved.SessionName != sess.Name || len(saved.PaneStates) != 2 {
		t.Fatalf("saved checkpoint = %#v", saved)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%s): %v", path, err)
	}
	sess.shutdown.Store(true)
	sess.writeCrashCheckpoint()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("writeCrashCheckpoint() while shutting down should not recreate %s, err=%v", path, err)
	}
}

func TestCaptureQueueLifecycle(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	cc1, client1, cleanup1 := newCaptureTestClient(t)
	defer cleanup1()
	cc2, client2, cleanup2 := newCaptureTestClient(t)
	defer cleanup2()

	req1 := &captureRequest{id: 1, client: cc1, args: []string{"--format", "json"}, reply: make(chan *Message, 1)}
	req2 := &captureRequest{id: 2, client: cc2, args: []string{"pane-2"}, reply: make(chan *Message, 1)}
	req3 := &captureRequest{id: 3, client: cc1, args: []string{"pane-3"}, reply: make(chan *Message, 1)}

	firstEnqueueDone := make(chan error, 1)
	go func() {
		firstEnqueueDone <- sess.enqueueCaptureRequest(req1)
	}()
	if got := readCaptureRequestForTest(t, client1); got.Type != MsgTypeCaptureRequest {
		t.Fatalf("req1 message type = %v, want %v", got.Type, MsgTypeCaptureRequest)
	}
	if err := <-firstEnqueueDone; err != nil {
		t.Fatalf("enqueueCaptureRequest(req1): %v", err)
	}

	if err := sess.enqueueCaptureRequest(req2); err != nil {
		t.Fatalf("enqueueCaptureRequest(req2): %v", err)
	}
	if err := sess.enqueueCaptureRequest(req3); err != nil {
		t.Fatalf("enqueueCaptureRequest(req3): %v", err)
	}

	sess.cancelCaptureRequest(req2.id)
	stateAfterQueueCancel := mustSessionQuery(t, sess, func(sess *Session) struct {
		current uint64
		queue   []uint64
	} {
		var queue []uint64
		for _, req := range sess.captureQueue {
			queue = append(queue, req.id)
		}
		current := uint64(0)
		if sess.captureCurrent != nil {
			current = sess.captureCurrent.id
		}
		return struct {
			current uint64
			queue   []uint64
		}{current: current, queue: queue}
	})
	if stateAfterQueueCancel.current != req1.id || len(stateAfterQueueCancel.queue) != 1 || stateAfterQueueCancel.queue[0] != req3.id {
		t.Fatalf("capture state after queue cancel = %#v", stateAfterQueueCancel)
	}

	promoteDone := make(chan struct{})
	go func() {
		sess.cancelCaptureRequest(req1.id)
		close(promoteDone)
	}()
	if got := readCaptureRequestForTest(t, client1); len(got.CmdArgs) != 1 || got.CmdArgs[0] != "pane-3" {
		t.Fatalf("promoted capture request = %#v, want pane-3", got)
	}
	<-promoteDone

	stateAfterPromote := mustSessionQuery(t, sess, func(sess *Session) struct {
		current uint64
		queue   int
	} {
		current := uint64(0)
		if sess.captureCurrent != nil {
			current = sess.captureCurrent.id
		}
		return struct {
			current uint64
			queue   int
		}{current: current, queue: len(sess.captureQueue)}
	})
	if stateAfterPromote.current != req3.id || stateAfterPromote.queue != 0 {
		t.Fatalf("capture state after promote = %#v", stateAfterPromote)
	}

	sess.cancelCaptureRequest(req3.id)
	finalState := mustSessionQuery(t, sess, func(sess *Session) struct {
		hasCurrent bool
		queue      int
	} {
		return struct {
			hasCurrent bool
			queue      int
		}{hasCurrent: sess.captureCurrent != nil, queue: len(sess.captureQueue)}
	})
	if finalState.hasCurrent || finalState.queue != 0 {
		t.Fatalf("final capture state = %#v, want empty queue", finalState)
	}

	_ = client2
}

func TestHandleTakeoverFailureWithoutRemoteManager(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	var writes bytes.Buffer
	sshPane := newRecordingPane(sess, 1, "ssh-pane", &writes)
	window := newTestWindowWithPanes(t, sess, 1, "main", sshPane)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{sshPane}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	req := mux.TakeoverRequest{
		Host:       "gpu-box",
		SSHAddress: "127.0.0.1:22",
		Panes:      []mux.TakeoverPane{{ID: 7, Name: "pane-7", Cols: 80, Rows: 23}},
	}

	sess.handleTakeover(srv, sshPane.ID, req)

	wantAck := mux.FormatTakeoverAck(remote.ManagedSessionName(sess.Name)) + "\n"
	if got := writes.String(); got != wantAck {
		t.Fatalf("takeover ack = %q, want %q", got, wantAck)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		notice       string
		takeoverBusy bool
		dormant      bool
	} {
		pane := sess.findPaneByID(sshPane.ID)
		return struct {
			notice       string
			takeoverBusy bool
			dormant      bool
		}{
			notice:       sess.notice,
			takeoverBusy: sess.takenOverPanes[sshPane.ID],
			dormant:      pane != nil && pane.Meta.Dormant,
		}
	})
	if !strings.Contains(state.notice, "remote manager unavailable") {
		t.Fatalf("session notice = %q, want remote manager failure", state.notice)
	}
	if state.takeoverBusy {
		t.Fatal("takenOverPanes flag should be cleared after failure")
	}
	if state.dormant {
		t.Fatal("ssh pane should remain visible after failed takeover")
	}
}

func TestPrepareRemotePaneAndInsertPreparedPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	if _, err := sess.prepareRemotePane(srv, "dev", 80, 23); err == nil || err.Error() != "no remote hosts configured" {
		t.Fatalf("prepareRemotePane without manager error = %v, want no remote hosts configured", err)
	}

	sess.SetupRemoteManager(&config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "127.0.0.1:1"},
	}}, "build-hash")

	pane, err := sess.prepareRemotePane(srv, "dev", 80, 23)
	if err == nil || !strings.Contains(err.Error(), "connecting to dev:") {
		t.Fatalf("prepareRemotePane remote error = %v, want remote connect failure", err)
	}
	if pane != nil {
		t.Fatalf("prepareRemotePane returned pane = %#v, want nil on connect failure", pane)
	}

	if got := sess.RemoteManager.ConnStatusForPane(sess.counter.Load()); got != "" {
		t.Fatalf("ConnStatusForPane after failed prepare = %q, want empty", got)
	}

	prepared := newStandaloneProxyPane(9, "pane-9")
	if err := sess.insertPreparedPaneIntoActiveWindow(prepared, mux.SplitHorizontal, false, false); err == nil || err.Error() != "no window" {
		t.Fatalf("insertPreparedPaneIntoActiveWindow without window error = %v, want no window", err)
	}

	base := newStandaloneProxyPane(1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", base)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{base}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	if err := sess.insertPreparedPaneIntoActiveWindow(prepared, mux.SplitVertical, true, false); err != nil {
		t.Fatalf("insertPreparedPaneIntoActiveWindow success path: %v", err)
	}

	inserted := mustSessionQuery(t, sess, func(sess *Session) bool {
		return sess.findPaneByID(prepared.ID) != nil && sess.activeWindow().Root.FindPane(prepared.ID) != nil
	})
	if !inserted {
		t.Fatal("prepared pane should be inserted into session and layout")
	}
}

func TestInsertPreparedPaneIntoActiveWindowKeepFocusPreservesZoomAndFocus(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	prepared := newStandaloneProxyPane(3, "pane-3")

	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	window.ActivePane = pane1
	window.ZoomedPaneID = pane1.ID
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	if err := sess.insertPreparedPaneIntoActiveWindow(prepared, mux.SplitVertical, false, true); err != nil {
		t.Fatalf("insertPreparedPaneIntoActiveWindow keepFocus: %v", err)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeID uint32
		zoomedID uint32
		hasPane  bool
	} {
		w := sess.activeWindow()
		return struct {
			activeID uint32
			zoomedID uint32
			hasPane  bool
		}{
			activeID: w.ActivePane.ID,
			zoomedID: w.ZoomedPaneID,
			hasPane:  w.Root.FindPane(prepared.ID) != nil,
		}
	})
	if state.activeID != pane1.ID || state.zoomedID != pane1.ID || !state.hasPane {
		t.Fatalf("keepFocus prepared pane insert state = %+v, want active pane-1, zoomed pane-1, pane present", state)
	}
}

func TestServerHandleConnAndSetupRemoteManager(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	srv.SetupRemoteManager(&config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "127.0.0.1:22"},
	}}, "build-hash")
	if sess.RemoteManager == nil {
		t.Fatal("SetupRemoteManager should install a manager on the session")
	}

	serverConn, peerConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		srv.handleConn(serverConn)
		close(done)
	}()

	if err := proto.WriteMsg(peerConn, &Message{Type: MsgTypeCommand, CmdName: "status"}); err != nil {
		t.Fatalf("WriteMsg command: %v", err)
	}
	if err := peerConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	resp, err := ReadMsg(peerConn)
	if err != nil {
		t.Fatalf("ReadMsg response: %v", err)
	}
	if resp.Type != MsgTypeCmdResult || !strings.Contains(resp.CmdOutput, "windows: 0, panes: 0 total") {
		t.Fatalf("one-shot response = %#v", resp)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleConn did not return after one-shot command")
	}

	serverConn2, clientConn2 := net.Pipe()
	done2 := make(chan struct{})
	go func() {
		srv.handleConn(serverConn2)
		close(done2)
	}()
	if err := proto.WriteMsg(clientConn2, &Message{Type: MsgTypeRender}); err != nil {
		t.Fatalf("WriteMsg invalid type: %v", err)
	}
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("handleConn did not close unknown message type")
	}
	_ = peerConn.Close()
	_ = clientConn2.Close()
}

func TestServerWriteCrashCheckpointSkipsEmptySession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sess.startedAt = time.Date(2026, time.March, 22, 12, 30, 0, 0, time.UTC)
	sess.writeCrashCheckpoint()

	path := checkpoint.CrashCheckpointPathTimestamped(sess.Name, sess.startedAt)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty session should not create crash checkpoint %s, err=%v", path, err)
	}
}
