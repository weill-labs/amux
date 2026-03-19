package server

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestEnqueueCommandMutationReturnsOnSessionShutdown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}

	resultCh := make(chan commandMutationResult, 1)
	go func() {
		resultCh <- sess.enqueueCommandMutation(func(*Session) commandMutationResult {
			return commandMutationResult{output: "unreachable\n"}
		})
	}()

	waitUntil(t, func() bool {
		return len(sess.sessionEvents) == 1
	})

	close(sess.sessionEventDone)

	select {
	case res := <-resultCh:
		if !errors.Is(res.err, errSessionShuttingDown) {
			t.Fatalf("command mutation error = %v, want %v", res.err, errSessionShuttingDown)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueueCommandMutation did not return after shutdown")
	}
}

func TestQueuedCommandRenameWindow(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	w := newTestWindowWithPanes(t, sess, 1, "window-1", newTestPane(sess, 1, "pane-1"))
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = w.Panes()
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "rename-window", "renamed")

	if res.cmdErr != "" {
		t.Fatalf("rename-window error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Renamed window to renamed") {
		t.Fatalf("rename-window output = %q", res.output)
	}
	if w.Name != "renamed" {
		t.Fatalf("window name = %q, want %q", w.Name, "renamed")
	}
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandResizeWindow(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	w1 := newTestWindowWithPanes(t, sess, 1, "window-1", newTestPane(sess, 1, "pane-1"))
	w2 := newTestWindowWithPanes(t, sess, 2, "window-2", newTestPane(sess, 2, "pane-2"))
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = append(w1.Panes(), w2.Panes()...)
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "resize-window", "120", "40")

	if res.cmdErr != "" {
		t.Fatalf("resize-window error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Resized to 120x40") {
		t.Fatalf("resize-window output = %q", res.output)
	}
	for _, w := range []*mux.Window{w1, w2} {
		if w.Width != 120 || w.Height != 39 {
			t.Fatalf("%s size = %dx%d, want 120x39", w.Name, w.Width, w.Height)
		}
	}
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandFocusAcrossWindows(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w1 := newTestWindowWithPanes(t, sess, 1, "window-1", p1)
	w2 := newTestWindowWithPanes(t, sess, 2, "window-2", p2)
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = []*mux.Pane{p1, p2}
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "focus", "pane-2")

	if res.cmdErr != "" {
		t.Fatalf("focus error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Focused pane-2") {
		t.Fatalf("focus output = %q", res.output)
	}
	if sess.ActiveWindowID != w2.ID || w2.ActivePane.ID != p2.ID {
		t.Fatalf("expected focus to move to window %d pane %d", w2.ID, p2.ID)
	}
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandToggleMinimize(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := newTestWindowWithPanes(t, sess, 1, "window-1", p1, p2)
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "toggle-minimize")

	if res.cmdErr != "" {
		t.Fatalf("toggle-minimize error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Minimized pane-2") {
		t.Fatalf("toggle-minimize output = %q", res.output)
	}
	if !p2.Meta.Minimized {
		t.Fatal("expected active pane to be minimized")
	}
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandNewWindow(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "new-window", "--name", "second")

	if res.cmdErr != "" {
		t.Fatalf("new-window error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Created second") {
		t.Fatalf("new-window output = %q", res.output)
	}

	waitUntil(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.Windows) == 2 && sess.ActiveWindowID == sess.Windows[1].ID && len(sess.Panes) == 2
	})
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandSpawnLocal(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "spawn", "--name", "worker-1", "--task", "build")

	if res.cmdErr != "" {
		t.Fatalf("spawn error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Spawned worker-1") {
		t.Fatalf("spawn output = %q", res.output)
	}

	waitUntil(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.Panes) == 2
	})
	sess.mu.Lock()
	found := false
	for _, p := range sess.Panes {
		if p.Meta.Name == "worker-1" && p.Meta.Task == "build" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected spawned pane metadata to be present")
	}
	if sess.generation.Load() <= before {
		t.Fatal("expected layout generation to increment")
	}
	sess.mu.Unlock()
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandKillOrphanPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	w := newTestWindowWithPanes(t, sess, 1, "window-1", p1)
	orphan := newTestPane(sess, 2, "orphan-pane")
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, orphan}
	sess.mu.Unlock()

	before := sess.generation.Load()
	res := runTestCommand(t, srv, sess, "kill", "orphan-pane")

	if res.cmdErr != "" {
		t.Fatalf("kill error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Killed orphan-pane") {
		t.Fatalf("kill output = %q", res.output)
	}

	sess.mu.Lock()
	if sess.hasPane(orphan.ID) {
		sess.mu.Unlock()
		t.Fatal("expected orphan pane to be removed")
	}
	if len(sess.Windows) != 1 || sess.Windows[0].PaneCount() != 1 {
		sess.mu.Unlock()
		t.Fatal("expected window layout to remain intact")
	}
	if sess.generation.Load() <= before {
		sess.mu.Unlock()
		t.Fatal("expected layout generation to increment")
	}
	sess.mu.Unlock()
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedCommandInjectProxyAndUnsplice(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.mu.Unlock()

	beforeInject := sess.generation.Load()
	injectRes := runTestCommand(t, srv, sess, "_inject-proxy", "fake-host")
	if injectRes.cmdErr != "" {
		t.Fatalf("inject-proxy error: %s", injectRes.cmdErr)
	}
	if !strings.Contains(injectRes.output, "Injected proxy pane") {
		t.Fatalf("inject-proxy output = %q", injectRes.output)
	}
	if sess.generation.Load() <= beforeInject {
		t.Fatal("expected layout generation to increment after inject")
	}

	var proxyID uint32
	sess.mu.Lock()
	for _, p := range sess.Panes {
		if p.IsProxy() && p.Meta.Host == "fake-host" {
			proxyID = p.ID
		}
	}
	sess.mu.Unlock()
	if proxyID == 0 {
		t.Fatal("expected injected proxy pane to exist")
	}

	beforeUnsplice := sess.generation.Load()
	unspliceRes := runTestCommand(t, srv, sess, "unsplice", "fake-host")
	if unspliceRes.cmdErr != "" {
		t.Fatalf("unsplice error: %s", unspliceRes.cmdErr)
	}
	if !strings.Contains(unspliceRes.output, "Unspliced fake-host") {
		t.Fatalf("unsplice output = %q", unspliceRes.output)
	}

	waitUntil(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if sess.hasPane(proxyID) {
			return false
		}
		for _, p := range sess.Panes {
			if !p.IsProxy() && p.ID != p1.ID {
				return true
			}
		}
		return false
	})
	if sess.generation.Load() <= beforeUnsplice {
		t.Fatal("expected layout generation to increment after unsplice")
	}
	assertSessionLayoutConsistent(t, sess)
}

func TestQueuedPreparedRemotePaneInsert(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	w := newTestWindowWithPanes(t, sess, 1, "window-1", p1)
	sess.mu.Lock()
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.mu.Unlock()

	proxy := mux.NewProxyPane(2, mux.PaneMeta{
		Name:  "pane-2",
		Host:  "gpu-server",
		Color: config.CatppuccinMocha[1],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.mu.Lock()
		defer sess.mu.Unlock()

		if err := sess.insertPreparedPaneIntoActiveWindowLocked(proxy, mux.SplitVertical, false); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          "inserted\n",
			broadcastLayout: true,
		}
	})
	if res.err != nil {
		t.Fatalf("enqueueCommandMutation insert error: %v", res.err)
	}

	sess.mu.Lock()
	if len(sess.Panes) != 2 {
		sess.mu.Unlock()
		t.Fatalf("expected 2 panes, got %d", len(sess.Panes))
	}
	if !sess.hasPane(proxy.ID) {
		sess.mu.Unlock()
		t.Fatal("expected prepared proxy pane to be registered")
	}
	if w.Root.FindPane(proxy.ID) == nil {
		sess.mu.Unlock()
		t.Fatal("expected prepared proxy pane to be inserted into active window")
	}
	sess.mu.Unlock()
	assertSessionLayoutConsistent(t, sess)
}

func newCommandTestSession(t *testing.T) (*Server, *Session, func()) {
	t.Helper()

	sess := newSession("test-command-queue")
	stopCrashCheckpointLoop(t, sess)
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	cleanup := func() {
		sess.shutdown.Store(true)
		sess.mu.Lock()
		panes := append([]*mux.Pane(nil), sess.Panes...)
		sess.mu.Unlock()
		for _, p := range panes {
			p.Close()
		}
		stopSessionBackgroundLoops(t, sess)
	}
	return srv, sess, cleanup
}

func newTestPane(sess *Session, id uint32, name string) *mux.Pane {
	return mux.NewProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
}

func newTestWindowWithPanes(t *testing.T, sess *Session, id uint32, name string, panes ...*mux.Pane) *mux.Window {
	t.Helper()
	if len(panes) == 0 {
		t.Fatal("need at least one pane")
	}

	w := mux.NewWindow(panes[0], 80, 23)
	w.ID = id
	w.Name = name
	for _, pane := range panes[1:] {
		if _, err := w.Split(mux.SplitHorizontal, pane); err != nil {
			t.Fatalf("Split: %v", err)
		}
	}
	return w
}

func runTestCommand(t *testing.T, srv *Server, sess *Session, name string, args ...string) struct {
	output string
	cmdErr string
} {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	cc := NewClientConn(serverConn)

	results := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		for {
			msg, err := ReadMsg(clientConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCmdResult {
				results <- struct {
					output string
					cmdErr string
				}{output: msg.CmdOutput, cmdErr: msg.CmdErr}
				return
			}
		}
	}()

	go cc.handleCommand(srv, sess, &Message{
		Type:    MsgTypeCommand,
		CmdName: name,
		CmdArgs: args,
	})

	select {
	case res := <-results:
		return res
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s result", name)
	}
	return struct {
		output string
		cmdErr string
	}{}
}
