package server

import (
	"net"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

func TestMouseCommandUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "mouse")
	if got := res.cmdErr; got != "usage: mouse [--client <id>] [--timeout <duration>] (press <x> <y> | motion <x> <y> | release <x> <y> | click <x> <y> | click <pane> [--status-line] | drag <pane> --to <pane>)" {
		t.Fatalf("mouse usage error = %q", got)
	}
}

func TestCmdMouseClickStatusLineUsesClientSizedLayout(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	p2 := newProxyPane(2, mux.PaneMeta{
		Name:  "pane-2",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(1),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	clientServerConn, clientPeerConn := net.Pipe()
	defer clientServerConn.Close()
	defer clientPeerConn.Close()

	client := newClientConn(clientServerConn)
	client.ID = "client-1"
	client.cols = 40
	client.rows = 12
	client.inputIdle = true
	client.uiGeneration = 1
	client.initTypeKeyQueue()
	defer client.Close()

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(client)
	})

	layout := visibleMouseLayoutForClient(w, client.cols, client.rows)
	wantX, wantY := mouseStatusCenterForLayout(t, layout, p1.ID)

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "mouse", "--client", client.ID, "click", "pane-1", "--status-line")

	press := readMsgWithTimeout(t, clientPeerConn)
	if press.Type != MsgTypeTypeKeys || string(press.Input) != ansi.MouseSgr(0, wantX-1, wantY-1, false) {
		t.Fatalf("press message = %#v, want SGR press at (%d,%d)", press, wantX, wantY)
	}

	release := readMsgWithTimeoutDuration(t, clientPeerConn, time.Second)
	if release.Type != MsgTypeTypeKeys || string(release.Input) != ansi.MouseSgr(0, wantX-1, wantY-1, true) {
		t.Fatalf("release message = %#v, want SGR release at (%d,%d)", release, wantX, wantY)
	}

	sess.enqueueUIEvent(client, protoUIEventInputBusy)
	sess.enqueueUIEvent(client, protoUIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Sent 2 mouse events via client client-1\n" {
		t.Fatalf("mouse output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mouse click command did not return")
	}
}

func TestCmdMouseDragPaneToPaneSendsStatusPressMotionAndRelease(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	p2 := newProxyPane(2, mux.PaneMeta{
		Name:  "pane-2",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(1),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	clientServerConn, clientPeerConn := net.Pipe()
	defer clientServerConn.Close()
	defer clientPeerConn.Close()

	client := newClientConn(clientServerConn)
	client.ID = "client-1"
	client.cols = 80
	client.rows = 24
	client.inputIdle = true
	client.uiGeneration = 1
	client.initTypeKeyQueue()
	defer client.Close()

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(client)
	})

	layout := visibleMouseLayoutForClient(w, client.cols, client.rows)
	startX, startY := mouseStatusCenterForLayout(t, layout, p2.ID)
	endX, endY := mousePaneCenterForLayout(t, layout, p1.ID)

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "mouse", "--client", client.ID, "drag", "pane-2", "--to", "pane-1")

	press := readMsgWithTimeout(t, clientPeerConn)
	if press.Type != MsgTypeTypeKeys || string(press.Input) != ansi.MouseSgr(0, startX-1, startY-1, false) {
		t.Fatalf("press message = %#v, want SGR press at (%d,%d)", press, startX, startY)
	}

	motion := readMsgWithTimeoutDuration(t, clientPeerConn, time.Second)
	if motion.Type != MsgTypeTypeKeys || string(motion.Input) != ansi.MouseSgr(32, endX-1, endY-1, false) {
		t.Fatalf("motion message = %#v, want SGR motion at (%d,%d)", motion, endX, endY)
	}

	release := readMsgWithTimeoutDuration(t, clientPeerConn, time.Second)
	if release.Type != MsgTypeTypeKeys || string(release.Input) != ansi.MouseSgr(0, endX-1, endY-1, true) {
		t.Fatalf("release message = %#v, want SGR release at (%d,%d)", release, endX, endY)
	}

	sess.enqueueUIEvent(client, protoUIEventInputBusy)
	sess.enqueueUIEvent(client, protoUIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Sent 3 mouse events via client client-1\n" {
		t.Fatalf("mouse output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mouse drag command did not return")
	}
}

const (
	protoUIEventInputBusy = "input-busy"
	protoUIEventInputIdle = "input-idle"
)

func visibleMouseLayoutForClient(w *mux.Window, cols, rows int) *mux.LayoutCell {
	layoutHeight := rows - render.GlobalBarHeight
	if w.ZoomedPaneID != 0 {
		return mux.NewLeafByID(w.ZoomedPaneID, 0, 0, cols, layoutHeight)
	}
	layout := mux.CloneLayout(w.Root)
	if layout == nil {
		return nil
	}
	if w.Width != cols || w.Height != layoutHeight {
		layout.ResizeAll(cols, layoutHeight)
	}
	return layout
}

func mouseStatusCenterForLayout(t *testing.T, layout *mux.LayoutCell, paneID uint32) (int, int) {
	t.Helper()

	cell := layout.FindByPaneID(paneID)
	if cell == nil {
		t.Fatalf("pane %d missing from layout", paneID)
	}
	return cell.X + cell.W/2 + 1, cell.Y + 1
}

func mousePaneCenterForLayout(t *testing.T, layout *mux.LayoutCell, paneID uint32) (int, int) {
	t.Helper()

	cell := layout.FindByPaneID(paneID)
	if cell == nil {
		t.Fatalf("pane %d missing from layout", paneID)
	}
	return cell.X + cell.W/2 + 1, cell.Y + cell.H/2 + 1
}
