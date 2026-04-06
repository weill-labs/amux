package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestRunNewWindowRetriesTransientInheritedCwdLookup(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := mustCreatePane(t, sess, srv, 80, 23)
	pane.Start()
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	wantDir := t.TempDir()
	resolverCalls := 0
	sess.PaneMetaResolver = func(got *mux.Pane) (string, string) {
		resolverCalls++
		if got != pane {
			t.Fatalf("resolver pane = %p, want %p", got, pane)
		}
		if resolverCalls == 1 {
			return "", ""
		}
		return wantDir, ""
	}

	builtDir := make(chan string, 1)
	sess.localPaneBuilder = func(req localPaneBuildRequest) (*mux.Pane, error) {
		builtDir <- req.meta.Dir
		return defaultLocalPaneBuilder(req)
	}

	res := runTestCommand(t, srv, sess, "new-window")
	if res.cmdErr != "" {
		t.Fatalf("new-window cmdErr = %q", res.cmdErr)
	}

	select {
	case got := <-builtDir:
		if got != wantDir {
			t.Fatalf("new-window inherited dir = %q, want %q", got, wantDir)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for new-window local build dir")
	}

	if resolverCalls < 2 {
		t.Fatalf("PaneMetaResolver calls = %d, want retry after empty cwd", resolverCalls)
	}
}

func TestRunCreatePaneRetriesTransientInheritedCwdLookup(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := mustCreatePane(t, sess, srv, 80, 23)
	pane.Start()
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
	window.LeadPaneID = 0
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	wantDir := t.TempDir()
	resolverCalls := 0
	sess.PaneMetaResolver = func(got *mux.Pane) (string, string) {
		resolverCalls++
		if got != pane {
			t.Fatalf("resolver pane = %p, want %p", got, pane)
		}
		if resolverCalls == 1 {
			return "", ""
		}
		return wantDir, ""
	}

	builtDir := make(chan string, 1)
	sess.localPaneBuilder = func(req localPaneBuildRequest) (*mux.Pane, error) {
		builtDir <- req.meta.Dir
		return defaultLocalPaneBuilder(req)
	}

	res := runTestCommand(t, srv, sess, "split")
	if res.cmdErr != "" {
		t.Fatalf("split cmdErr = %q", res.cmdErr)
	}

	select {
	case got := <-builtDir:
		if got != wantDir {
			t.Fatalf("split inherited dir = %q, want %q", got, wantDir)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for split local build dir")
	}

	if resolverCalls < 2 {
		t.Fatalf("PaneMetaResolver calls = %d, want retry after empty cwd", resolverCalls)
	}
}
