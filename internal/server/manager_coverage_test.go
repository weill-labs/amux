package server

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestWaiterManagerCoverageHelpersAndDelegates(t *testing.T) {
	t.Parallel()

	t.Run("nil receivers return zero values", func(t *testing.T) {
		t.Parallel()

		sessValue := &Session{}
		waiters := sessValue.ensureWaiters()
		if waiters == nil {
			t.Fatal("ensureWaiters() = nil")
		}
		if got := sessValue.ensureWaiters(); got != waiters {
			t.Fatal("ensureWaiters() should reuse the existing manager")
		}

		var mgr *waiterManager
		if got := mgr.clipboardGeneration(); got != 0 {
			t.Fatalf("clipboardGeneration() = %d, want 0", got)
		}
		if got := mgr.hookGeneration(); got != 0 {
			t.Fatalf("hookGeneration() = %d, want 0", got)
		}
		if got := mgr.outputSubscriberCount(1); got != 0 {
			t.Fatalf("outputSubscriberCount() = %d, want 0", got)
		}
		if mgr.paneOutputWaiterRegistered(1) {
			t.Fatal("paneOutputWaiterRegistered() = true, want false")
		}
		mgr.removePane(1)

		var sess *Session
		if got := sess.clipboardGeneration(); got != 0 {
			t.Fatalf("Session.clipboardGeneration() = %d, want 0", got)
		}
		if got := sess.hookGeneration(); got != 0 {
			t.Fatalf("Session.hookGeneration() = %d, want 0", got)
		}
	})

	t.Run("pane helpers and retained hook history are isolated", func(t *testing.T) {
		t.Parallel()

		mgr := newWaiterManager()
		if got := mgr.retainedHookResults(); len(got) != 0 {
			t.Fatalf("retainedHookResults() len = %d, want 0", len(got))
		}

		if got := mgr.paneExistsAndMatches(nil, "needle"); got.exists || got.matched || got.ch != nil {
			t.Fatalf("paneExistsAndMatches(nil) = %+v, want zero value", got)
		}

		pane := newProxyPane(1, mux.PaneMeta{
			Name:  "pane-1",
			Host:  mux.DefaultHost,
			Color: "f5e0dc",
		}, 80, 23, nil, nil, func(data []byte) (int, error) {
			return len(data), nil
		})
		pane.FeedOutput([]byte("hello waiter\r\n"))

		got := mgr.paneExistsAndMatches(pane, "waiter")
		if !got.exists || !got.matched {
			t.Fatalf("paneExistsAndMatches(pane) = %+v, want existing match", got)
		}

		mgr.setHookStateForTest(3, []hookResultRecord{{Generation: 4, Event: "on-idle"}})
		retained := mgr.retainedHookResults()
		retained[0].Event = "mutated"
		if mgr.hookResults[0].Event != "on-idle" {
			t.Fatalf("retainedHookResults() should return a copy, got %q", mgr.hookResults[0].Event)
		}

		mgr.addPaneOutputSubscriber(1)
		if !mgr.paneOutputWaiterRegistered(1) {
			t.Fatal("paneOutputWaiterRegistered() = false, want true")
		}
		if got := mgr.outputSubscriberCount(1); got != 1 {
			t.Fatalf("outputSubscriberCount() = %d, want 1", got)
		}
		mgr.removePane(1)
		if mgr.paneOutputWaiterRegistered(1) {
			t.Fatal("paneOutputWaiterRegistered() = true after removePane")
		}
		if got := mgr.outputSubscriberCount(1); got != 0 {
			t.Fatalf("outputSubscriberCount() after removePane = %d, want 0", got)
		}
	})

	t.Run("session delegates cover clipboard and hook wrappers", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-waiter-manager-delegates")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		clipCh := make(chan string, 1)
		hookCh := make(chan hookResultRecord, 1)
		record := hookResultRecord{
			Generation: 3,
			Event:      "on-idle",
			PaneID:     1,
			PaneName:   "pane-1",
		}

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			waiters := sess.ensureWaiters()
			waiters.clipboardWaiters[1] = clipboardWaiter{afterGen: 0, reply: clipCh}
			waiters.hookWaiters[1] = hookWaiter{
				afterGen:  0,
				eventName: "on-idle",
				paneID:    1,
				paneName:  "pane-1",
				reply:     hookCh,
			}
			waiters.setHookStateForTest(2, []hookResultRecord{record})
			return struct{}{}
		})

		sess.notifyClipboardWaiters(1, "clip-data")
		select {
		case got := <-clipCh:
			if got != "clip-data" {
				t.Fatalf("notifyClipboardWaiters() = %q, want clip-data", got)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for clipboard waiter")
		}

		sess.notifyHookWaiters(record)
		select {
		case got := <-hookCh:
			if got != record {
				t.Fatalf("notifyHookWaiters() = %+v, want %+v", got, record)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for hook waiter")
		}

		matched, ok := sess.matchHookResult(2, "on-idle", 1, "pane-1")
		if !ok || matched != record {
			t.Fatalf("matchHookResult() = (%+v, %v), want (%+v, true)", matched, ok, record)
		}

		matched, ok = sess.waitHook(2, "on-idle", "pane-1", time.Second)
		if !ok || matched != record {
			t.Fatalf("waitHook() = (%+v, %v), want (%+v, true)", matched, ok, record)
		}

		matched, ok = sess.ensureWaiters().waitHook(sess, 2, "on-idle", "pane-1", time.Second)
		if !ok || matched != record {
			t.Fatalf("waiterManager.waitHook() = (%+v, %v), want (%+v, true)", matched, ok, record)
		}
	})

	t.Run("begin pane output wait returns empty start for missing panes", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-waiter-manager-missing-pane")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		start, err := sess.ensureWaiters().beginPaneOutputWait(sess, 99, "needle")
		if err != nil {
			t.Fatalf("beginPaneOutputWait() error = %v", err)
		}
		if start.exists || start.matched || start.ch != nil {
			t.Fatalf("beginPaneOutputWait() = %+v, want zero value", start)
		}
	})
}

func TestWaiterManagerTimeoutSeesStateRecordedWithoutNotification(t *testing.T) {
	t.Parallel()

	t.Run("clipboard timeout observes updated generation", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-waiter-clipboard-timeout")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		sess.waiters.setClipboardStateForTest(1, "old")

		done := make(chan struct{})
		var payload string
		var ok bool
		go func() {
			payload, ok = sess.waitClipboard(1, 50*time.Millisecond)
			close(done)
		}()

		waitUntil(t, func() bool {
			return mustSessionQuery(t, sess, func(sess *Session) bool {
				return sess.waiters.clipboardWaiterRegistered(1)
			})
		})

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.waiters.setClipboardStateForTest(2, "late")
			return struct{}{}
		})

		select {
		case <-done:
			if !ok || payload != "late" {
				t.Fatalf("waitClipboard() = (%q, %v), want (late, true)", payload, ok)
			}
		case <-time.After(time.Second):
			t.Fatal("waitClipboard() did not return")
		}
	})

	t.Run("hook timeout observes retained history", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-waiter-hook-timeout")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		sess.waiters.setHookStateForTest(1, nil)

		done := make(chan struct{})
		var record hookResultRecord
		var ok bool
		go func() {
			record, ok = sess.waitHookForPane(1, "on-idle", 1, "pane-1", 50*time.Millisecond)
			close(done)
		}()

		waitUntil(t, func() bool {
			return mustSessionQuery(t, sess, func(sess *Session) bool {
				return sess.waiters.hookWaiterRegistered(1, "on-idle", 1, "pane-1")
			})
		})

		want := hookResultRecord{
			Generation: 2,
			Event:      "on-idle",
			PaneID:     1,
			PaneName:   "pane-1",
			Success:    true,
		}
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.waiters.setHookStateForTest(1, []hookResultRecord{want})
			return struct{}{}
		})

		select {
		case <-done:
			if !ok || record != want {
				t.Fatalf("waitHookForPane() = (%+v, %v), want (%+v, true)", record, ok, want)
			}
		case <-time.After(time.Second):
			t.Fatal("waitHookForPane() did not return")
		}
	})
}

func TestCaptureForwarderCoverageHelpers(t *testing.T) {
	t.Parallel()

	t.Run("ensure and idle helper paths are safe", func(t *testing.T) {
		t.Parallel()

		sess := &Session{}
		first := sess.ensureCaptureForwarder()
		if first == nil {
			t.Fatal("ensureCaptureForwarder() = nil")
		}
		if got := sess.ensureCaptureForwarder(); got != first {
			t.Fatal("ensureCaptureForwarder() should reuse the existing manager")
		}

		var sent []uint64
		send := func(req *captureRequest) {
			sent = append(sent, req.id)
		}

		first.startNext(send)
		first.routeResponse(&Message{Type: MsgTypeCaptureResponse}, send)
		sendCaptureRequest(nil, send)

		req := &captureRequest{id: 7, reply: make(chan *Message, 1)}
		req.reply <- &Message{Type: MsgTypeCaptureResponse}
		first.current = req
		first.startNext(send)
		first.routeResponse(&Message{Type: MsgTypeCaptureResponse}, send)

		if len(sent) != 0 {
			t.Fatalf("idle helper paths should not send requests, got %v", sent)
		}
	})

	t.Run("queue lifecycle updates snapshots and cancellations", func(t *testing.T) {
		t.Parallel()

		f := newCaptureForwarder()
		if got := f.nextRequestID(); got != 1 {
			t.Fatalf("nextRequestID() = %d, want 1", got)
		}
		if got := f.nextRequestID(); got != 2 {
			t.Fatalf("nextRequestID() = %d, want 2", got)
		}

		req1 := &captureRequest{id: 1, reply: make(chan *Message, 1)}
		req2 := &captureRequest{id: 2, reply: make(chan *Message, 1)}
		req3 := &captureRequest{id: 3, reply: make(chan *Message, 1)}

		var sent []uint64
		send := func(req *captureRequest) {
			sent = append(sent, req.id)
		}

		f.enqueue(req1, send)
		f.enqueue(req2, send)
		f.enqueue(req3, send)

		if !slices.Equal(sent, []uint64{1}) {
			t.Fatalf("enqueue() sent %v, want [1]", sent)
		}

		state := f.snapshot()
		if !state.hasCurrent || state.currentID != 1 || !slices.Equal(state.queuedIDs, []uint64{2, 3}) || state.queueLen != 2 {
			t.Fatalf("snapshot() = %+v, want current=1 queue=[2 3]", state)
		}

		f.cancel(99, send)
		f.cancel(req2.id, send)

		state = f.snapshot()
		if !state.hasCurrent || state.currentID != 1 || !slices.Equal(state.queuedIDs, []uint64{3}) || state.queueLen != 1 {
			t.Fatalf("snapshot() after queue cancel = %+v, want current=1 queue=[3]", state)
		}

		response := &Message{Type: MsgTypeCaptureResponse, CmdOutput: "ok"}
		f.routeResponse(response, send)
		select {
		case got := <-req1.reply:
			if got != response {
				t.Fatalf("routeResponse() reply = %+v, want %+v", got, response)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for capture reply")
		}

		if !slices.Equal(sent, []uint64{1, 3}) {
			t.Fatalf("routeResponse() promoted sends = %v, want [1 3]", sent)
		}

		f.cancel(req3.id, send)
		state = f.snapshot()
		if state.hasCurrent || state.queueLen != 0 || len(state.queuedIDs) != 0 {
			t.Fatalf("final snapshot() = %+v, want empty forwarder", state)
		}
	})
}

func TestSessionStartNextCaptureRequestWrapper(t *testing.T) {
	t.Parallel()

	sess := newSession("test-start-next-capture-request")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	serverConn, peerConn := net.Pipe()
	cc := newClientConn(serverConn)
	t.Cleanup(func() {
		cc.Close()
		_ = peerConn.Close()
	})

	req := &captureRequest{
		id:     1,
		client: cc,
		args:   []string{"pane-1"},
		reply:  make(chan *Message, 1),
	}
	sess.ensureCaptureForwarder().queue = []*captureRequest{req}

	done := make(chan struct{})
	go func() {
		sess.startNextCaptureRequest()
		close(done)
	}()
	msg := readCaptureRequestForTest(t, peerConn)
	if msg.Type != MsgTypeCaptureRequest {
		t.Fatalf("message type = %v, want capture request", msg.Type)
	}
	if !slices.Equal(msg.CmdArgs, []string{"pane-1"}) {
		t.Fatalf("capture request args = %v, want [pane-1]", msg.CmdArgs)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startNextCaptureRequest() did not return")
	}
}

func TestManagerEnsureAndErrorHelpers(t *testing.T) {
	t.Parallel()

	t.Run("client manager reuses connection log and reports empty first client", func(t *testing.T) {
		t.Parallel()

		sess := &Session{}
		manager := sess.ensureClientManager()
		if manager == nil {
			t.Fatal("ensureClientManager() = nil")
		}
		if got := sess.ensureClientManager(); got != manager {
			t.Fatal("ensureClientManager() should reuse the existing manager")
		}

		var zero clientManager
		log := zero.ensureConnectionLog()
		if log == nil {
			t.Fatal("ensureConnectionLog() = nil")
		}
		if got := zero.ensureConnectionLog(); got != log {
			t.Fatal("ensureConnectionLog() should reuse the existing log")
		}
		if got := zero.firstClient(); got != nil {
			t.Fatalf("firstClient() = %v, want nil", got)
		}
	})

	t.Run("undo manager covers nil and duplicate cleanup paths", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-undo-manager-helpers")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		manager := (&Session{}).ensureUndoManager()
		if manager == nil {
			t.Fatal("ensureUndoManager() = nil")
		}

		timer1 := time.NewTimer(time.Hour)
		timer2 := time.NewTimer(time.Hour)
		defer timer1.Stop()
		defer timer2.Stop()

		manager.pendingCleanupKills[1] = timer1
		manager.closedPaneTimers[1] = timer2
		manager.removePane(1)
		if len(manager.pendingCleanupKills) != 0 || len(manager.closedPaneTimers) != 0 {
			t.Fatalf("removePane() should clear timers, got cleanup=%d closed=%d", len(manager.pendingCleanupKills), len(manager.closedPaneTimers))
		}

		if err := manager.beginPaneCleanupKill(sess, nil, time.Second); err != nil {
			t.Fatalf("beginPaneCleanupKill(nil) error = %v", err)
		}

		pane := newProxyPane(2, mux.PaneMeta{
			Name:  "pane-2",
			Host:  mux.DefaultHost,
			Color: "f2cdcd",
		}, 80, 23, nil, nil, func(data []byte) (int, error) {
			return len(data), nil
		})

		dupTimer := time.NewTimer(time.Hour)
		defer dupTimer.Stop()
		manager.pendingCleanupKills[pane.ID] = dupTimer
		if err := manager.beginPaneCleanupKill(sess, pane, time.Second); err == nil {
			t.Fatal("beginPaneCleanupKill() with duplicate timer should fail")
		}

		before := manager.closedPaneCount()
		manager.trackSoftClosedPane(sess, nil)
		if got := manager.closedPaneCount(); got != before {
			t.Fatalf("trackSoftClosedPane(nil) changed closed pane count from %d to %d", before, got)
		}
	})

	t.Run("input router covers ensure reuse and missing pane errors", func(t *testing.T) {
		t.Parallel()

		zero := &Session{}
		router := zero.ensureInputRouter()
		if router == nil {
			t.Fatal("ensureInputRouter() = nil")
		}
		if got := zero.ensureInputRouter(); got != router {
			t.Fatal("ensureInputRouter() should reuse the existing router")
		}

		cc := newClientConn(discardConn{})
		t.Cleanup(cc.Close)
		sess := newSession("test-input-router-helpers")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)
		if got := router.activeInputPaneForWrite(sess, cc); got != nil {
			t.Fatalf("activeInputPaneForWrite() without target = %v, want nil", got)
		}

		pane := newTestPane(sess, 1, "pane-1")
		stop := make(chan struct{})
		done := make(chan struct{})
		close(stop)
		close(done)
		stopped := &Session{
			sessionEventStop: stop,
			sessionEventDone: done,
			input:            newInputRouter(),
			clientState:      newClientManager(),
		}
		stopped.ensureInputRouter().refreshTarget(pane)

		owner := newClientConn(discardConn{})
		t.Cleanup(owner.Close)
		stopped.ensureClientManager().setClientsForTest(owner, cc)
		stopped.ensureClientManager().setSizeOwnerForTest(owner)

		if got := stopped.ensureInputRouter().activeInputPaneForWrite(stopped, cc); got != nil {
			t.Fatalf("activeInputPaneForWrite() after session stop = %v, want nil", got)
		}

		sess = newSession("test-enqueue-paced-pane-input-missing-pane")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		pane = newTestPane(sess, 2, "pane-2")
		if err := sess.enqueuePacedPaneInput(pane, []encodedKeyChunk{{data: []byte("x")}}); err == nil {
			t.Fatal("enqueuePacedPaneInput() for missing pane should fail")
		}
	})
}
