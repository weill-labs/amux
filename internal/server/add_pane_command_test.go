package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestCommandSpawnSpiralLocalKeepsFocus(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()

	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}

	res := runTestCommand(t, srv, sess, "spawn", "--spiral", "--name", "spiral-2")
	if res.cmdErr != "" || !strings.Contains(res.output, "Spawned spiral-2") {
		t.Fatalf("spawn --spiral result = %#v", res)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount int
		activeID  uint32
		inLayout  bool
	} {
		pane := sess.findPaneByID(2)
		return struct {
			paneCount int
			activeID  uint32
			inLayout  bool
		}{
			paneCount: len(sess.Panes),
			activeID:  sess.activeWindow().ActivePane.ID,
			inLayout:  pane != nil && pane.Meta.Name == "spiral-2" && sess.activeWindow().Root.FindPane(pane.ID) != nil,
		}
	})
	if state.paneCount != 2 {
		t.Fatalf("pane count = %d, want 2", state.paneCount)
	}
	if state.activeID != p1.ID {
		t.Fatalf("active pane = %d, want %d", state.activeID, p1.ID)
	}
	if !state.inLayout {
		t.Fatal("new pane should be registered and in layout")
	}
}

func TestCommandAddPaneIsUnknown(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "add-pane", "pane-1")
	if res.cmdErr != "unknown command: add-pane" {
		t.Fatalf("add-pane unknown command error = %q", res.cmdErr)
	}
}

func TestCommandSpawnSpiralRequiresWindow(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "spawn", "--spiral")
	if res.cmdErr != "no window" {
		t.Fatalf("spawn --spiral without window error = %q", res.cmdErr)
	}
}

func TestCommandSpawnSpiralRejectsMissingInheritedPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newAddPaneTestProxyPane(1, "pane-1")
	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID

	res := runTestCommand(t, srv, sess, "spawn", "--spiral")
	if res.cmdErr != "pane 1 not found" {
		t.Fatalf("spawn --spiral missing inherited pane error = %q", res.cmdErr)
	}
}

func TestCommandSpawnSpiralExplicitHostUsesRemotePath(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()

	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	installTestPaneTransport(t, sess, &stubPaneTransport{
		createPaneErr: fmt.Errorf("connecting to dev: SSH dial"),
	}, config.ColorForHost)

	res := runTestCommand(t, srv, sess, "spawn", "--spiral", "--host", "dev")
	if !strings.Contains(res.cmdErr, "connecting to dev:") {
		t.Fatalf("spawn --spiral --host error = %q, want remote connect failure", res.cmdErr)
	}

	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 1 {
		t.Fatalf("pane count after failed remote spiral spawn = %d, want 1", got)
	}
}

func TestCommandSpawnSpiralInheritsProxyHost(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	proxy := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  "gpu-server",
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(proxy, 80, 24)
	w.ID = 1
	w.Name = "main"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{proxy}
	installTestPaneTransport(t, sess, &stubPaneTransport{
		createPaneErr: fmt.Errorf("connecting to gpu-server: SSH dial"),
	}, config.ColorForHost)

	res := runTestCommand(t, srv, sess, "spawn", "--spiral")
	if !strings.Contains(res.cmdErr, "connecting to gpu-server:") {
		t.Fatalf("spawn --spiral inherited-host error = %q, want gpu-server remote connect failure", res.cmdErr)
	}
}

func TestCommandSpawnSpiralRejectsInvalidLayout(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newAddPaneTestProxyPane(1, "pane-1")
	p2 := newAddPaneTestProxyPane(2, "pane-2")
	p3 := newAddPaneTestProxyPane(3, "pane-3")
	w := mux.NewWindow(p1, 80, 24)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(mux.SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2, p3}

	res := runTestCommand(t, srv, sess, "spawn", "--spiral")
	if res.cmdErr != "add-pane requires a canonical spiral layout prefix for 3 panes" {
		t.Fatalf("spawn --spiral invalid-layout error = %q", res.cmdErr)
	}

	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 3 {
		t.Fatalf("pane count after rejected spiral spawn = %d, want 3", got)
	}
}

func newAddPaneTestProxyPane(id uint32, name string) *mux.Pane {
	return mux.NewProxyPaneWithScrollback(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.AccentColor(id - 1),
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
}
