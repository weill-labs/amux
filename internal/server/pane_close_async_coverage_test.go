package server

import (
	"errors"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestClosePaneAsyncIgnoresNilPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-close-pane-async-nil")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	called := make(chan struct{}, 1)
	sess.paneCloser = func(*mux.Pane) {
		called <- struct{}{}
	}

	sess.closePaneAsync(nil)

	select {
	case <-called:
		t.Fatal("pane closer should not run for nil panes")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOwnPaneNilReturnsNil(t *testing.T) {
	t.Parallel()

	sess := newSession("test-own-pane-nil")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	if got := sess.ownPane(nil); got != nil {
		t.Fatalf("ownPane(nil) = %#v, want nil", got)
	}
}

func TestCleanupFailedPaneMutationRemovesPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-cleanup-failed-pane-mutation")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	pane := newTestPane(sess, 1, "pane-1")
	sess.Panes = []*mux.Pane{pane}

	wantErr := errors.New("boom")
	res := cleanupFailedPaneMutation(sess, pane, wantErr)

	if !errors.Is(res.err, wantErr) {
		t.Fatalf("cleanup err = %v, want %v", res.err, wantErr)
	}
	if len(res.closePanes) != 1 || res.closePanes[0] != pane {
		t.Fatalf("closePanes = %#v, want [%p]", res.closePanes, pane)
	}
	if sess.hasPane(pane.ID) {
		t.Fatal("pane should be removed from the session")
	}
}

func TestCommandSplitLeadPaneCleansUpFailedPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	closedPaneIDs := make(chan uint32, 1)
	sess.paneCloser = func(pane *mux.Pane) {
		closedPaneIDs <- pane.ID
	}

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane pane-1: %v", err)
	}
	p1.Start()
	p2, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane pane-2: %v", err)
	}
	p2.Start()

	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	w.ActivePane = p1
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	res := runTestCommand(t, srv, sess, "split", "pane-1")
	if res.cmdErr != "cannot operate on lead pane" {
		t.Fatalf("split error = %q, want %q", res.cmdErr, "cannot operate on lead pane")
	}

	select {
	case closedID := <-closedPaneIDs:
		if closedID != 3 {
			t.Fatalf("closed pane ID = %d, want 3", closedID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed split pane cleanup")
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount int
		hasPane3  bool
	} {
		return struct {
			paneCount int
			hasPane3  bool
		}{
			paneCount: len(sess.Panes),
			hasPane3:  sess.findPaneByID(3) != nil,
		}
	})
	if state.paneCount != 2 {
		t.Fatalf("pane count after failed split = %d, want 2", state.paneCount)
	}
	if state.hasPane3 {
		t.Fatal("failed split pane should be removed from the session")
	}
}

func TestCommandSpawnAtLeadPaneSucceeds(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	closedPaneIDs := make(chan uint32, 1)
	sess.paneCloser = func(pane *mux.Pane) {
		closedPaneIDs <- pane.ID
	}

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane pane-1: %v", err)
	}
	p1.Start()
	p2, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane pane-2: %v", err)
	}
	p2.Start()

	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	w.ActivePane = p1
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	res := runTestCommand(t, srv, sess, "spawn", "--at", "pane-1", "--name", "worker-1", "--task", "build")
	if res.cmdErr != "" {
		t.Fatalf("spawn error = %q, want success", res.cmdErr)
	}

	select {
	case closedID := <-closedPaneIDs:
		t.Fatalf("pane closer should not run on successful spawn, got pane %d", closedID)
	case <-time.After(50 * time.Millisecond):
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount     int
		hasPane3      bool
		leadWindowID  uint32
		workerWindowID uint32
	} {
		workerWindowID := uint32(0)
		if worker, err := sess.findPaneByRef("worker-1"); err == nil {
			if workerWindow := sess.findWindowByPaneID(worker.ID); workerWindow != nil {
				workerWindowID = workerWindow.ID
			}
		}
		return struct {
			paneCount      int
			hasPane3       bool
			leadWindowID   uint32
			workerWindowID uint32
		}{
			paneCount:      len(sess.Panes),
			hasPane3:       sess.findPaneByID(3) != nil,
			leadWindowID:   sess.findWindowByPaneID(p1.ID).ID,
			workerWindowID: workerWindowID,
		}
	})
	if state.paneCount != 3 {
		t.Fatalf("pane count after spawn = %d, want 3", state.paneCount)
	}
	if !state.hasPane3 {
		t.Fatal("successful spawn should keep the new pane in the session")
	}
	if state.workerWindowID != state.leadWindowID {
		t.Fatalf("worker window ID = %d, want %d", state.workerWindowID, state.leadWindowID)
	}
}

func TestCommandUnspliceOrphanProxyCleansUpFailedPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	closedPaneIDs := make(chan uint32, 1)
	sess.paneCloser = func(pane *mux.Pane) {
		closedPaneIDs <- pane.ID
	}

	p1 := newTestPane(sess, 1, "pane-1")
	orphanProxy := newProxyPane(2, mux.PaneMeta{
		Name:  "pane-2",
		Host:  "fake-host",
		Color: p1.Meta.Color,
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, orphanProxy)

	res := runTestCommand(t, srv, sess, "unsplice", "fake-host")
	if res.cmdErr != `no spliced panes found for host "fake-host"` {
		t.Fatalf("unsplice error = %q, want %q", res.cmdErr, `no spliced panes found for host "fake-host"`)
	}

	select {
	case closedID := <-closedPaneIDs:
		if closedID != 3 {
			t.Fatalf("closed pane ID = %d, want 3", closedID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed unsplice pane cleanup")
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount  int
		hasOrphan  bool
		hasPane3   bool
		activePane uint32
		activeHost string
	} {
		activePane := uint32(0)
		activeHost := ""
		if w := sess.activeWindow(); w != nil && w.ActivePane != nil {
			activePane = w.ActivePane.ID
			activeHost = w.ActivePane.Meta.Host
		}
		return struct {
			paneCount  int
			hasOrphan  bool
			hasPane3   bool
			activePane uint32
			activeHost string
		}{
			paneCount:  len(sess.Panes),
			hasOrphan:  sess.findPaneByID(orphanProxy.ID) != nil,
			hasPane3:   sess.findPaneByID(3) != nil,
			activePane: activePane,
			activeHost: activeHost,
		}
	})
	if state.paneCount != 2 {
		t.Fatalf("pane count after failed unsplice = %d, want 2", state.paneCount)
	}
	if !state.hasOrphan {
		t.Fatal("orphan proxy pane should remain registered after failed unsplice")
	}
	if state.hasPane3 {
		t.Fatal("failed unsplice replacement pane should be removed from the session")
	}
	if state.activePane != p1.ID || state.activeHost != mux.DefaultHost {
		t.Fatalf("active pane after failed unsplice = (%d, %q), want (%d, %q)", state.activePane, state.activeHost, p1.ID, mux.DefaultHost)
	}
}
