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

func TestParseMouseCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    mouseCommandOptions
		wantErr string
	}{
		{
			name: "press coordinates",
			args: []string{"press", "12", "8"},
			want: mouseCommandOptions{
				waitTimeout: defaultMouseCommandTimeout,
				kind:        mouseCommandPress,
				x:           12,
				y:           8,
			},
		},
		{
			name: "motion with timeout",
			args: []string{"--timeout", "250ms", "motion", "9", "4"},
			want: mouseCommandOptions{
				waitTimeout: 250 * time.Millisecond,
				kind:        mouseCommandMotion,
				x:           9,
				y:           4,
			},
		},
		{
			name: "release with client",
			args: []string{"--client", "client-7", "release", "3", "5"},
			want: mouseCommandOptions{
				requestedClientID: "client-7",
				waitTimeout:       defaultMouseCommandTimeout,
				kind:              mouseCommandRelease,
				x:                 3,
				y:                 5,
			},
		},
		{
			name: "click coordinates",
			args: []string{"click", "6", "7"},
			want: mouseCommandOptions{
				waitTimeout: defaultMouseCommandTimeout,
				kind:        mouseCommandClickCoords,
				x:           6,
				y:           7,
			},
		},
		{
			name: "click pane status line",
			args: []string{"click", "pane-2", "--status-line"},
			want: mouseCommandOptions{
				waitTimeout: defaultMouseCommandTimeout,
				kind:        mouseCommandClickPane,
				paneRef:     "pane-2",
				statusLine:  true,
			},
		},
		{
			name: "drag pane to pane",
			args: []string{"drag", "pane-1", "--to", "pane-3"},
			want: mouseCommandOptions{
				waitTimeout:   defaultMouseCommandTimeout,
				kind:          mouseCommandDragPane,
				paneRef:       "pane-1",
				targetPaneRef: "pane-3",
			},
		},
		{name: "missing client value", args: []string{"--client"}, wantErr: "missing value for --client"},
		{name: "missing timeout value", args: []string{"--timeout"}, wantErr: "missing value for --timeout"},
		{name: "invalid timeout", args: []string{"--timeout", "soon", "press", "1", "1"}, wantErr: "invalid timeout: soon"},
		{name: "invalid x coordinate", args: []string{"press", "left", "2"}, wantErr: `mouse: invalid x coordinate "left"`},
		{name: "invalid y coordinate", args: []string{"motion", "2", "down"}, wantErr: `mouse: invalid y coordinate "down"`},
		{name: "zero coordinate", args: []string{"release", "0", "3"}, wantErr: "mouse: x coordinate must be >= 1"},
		{name: "click pane invalid flag", args: []string{"click", "pane-1", "--bogus"}, wantErr: mouseCommandUsage},
		{name: "drag missing to", args: []string{"drag", "pane-1", "pane-2"}, wantErr: mouseCommandUsage},
		{name: "unknown action", args: []string{"hover", "1", "1"}, wantErr: mouseCommandUsage},
		{name: "no action after flags", args: []string{"--client", "client-1"}, wantErr: mouseCommandUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseMouseCommandArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseMouseCommandArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMouseCommandArgs(%v): %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseMouseCommandArgs(%v) = %#v, want %#v", tt.args, got, tt.want)
			}
		})
	}
}

func TestMouseCommandHelpers(t *testing.T) {
	t.Parallel()

	t.Run("looksLikeMouseCoordinatePair", func(t *testing.T) {
		t.Parallel()
		if !looksLikeMouseCoordinatePair([]string{"3", "4"}) {
			t.Fatal("numeric coordinates should be detected")
		}
		if looksLikeMouseCoordinatePair([]string{"pane-1", "--status-line"}) {
			t.Fatal("pane refs should not be treated as raw coordinates")
		}
		if looksLikeMouseCoordinatePair([]string{"3"}) {
			t.Fatal("short coordinate lists should not match")
		}
	})

	t.Run("parseMouseCoordinates arity", func(t *testing.T) {
		t.Parallel()
		if _, _, err := parseMouseCoordinates([]string{"1"}); err == nil || err.Error() != mouseCommandUsage {
			t.Fatalf("parseMouseCoordinates short args error = %v, want %q", err, mouseCommandUsage)
		}
	})

	t.Run("buildMouseVisibleLayout guards", func(t *testing.T) {
		t.Parallel()

		if _, err := buildMouseVisibleLayout(&mux.Window{}, 0, 1); err == nil || err.Error() != "client size 0x1 cannot render mouse targets" {
			t.Fatalf("buildMouseVisibleLayout invalid size error = %v", err)
		}

		zoomed := &mux.Window{ZoomedPaneID: 7}
		layout, err := buildMouseVisibleLayout(zoomed, 40, render.GlobalBarHeight+12)
		if err != nil {
			t.Fatalf("buildMouseVisibleLayout zoomed: %v", err)
		}
		if got := layout.CellPaneID(); got != 7 {
			t.Fatalf("zoomed layout pane = %d, want 7", got)
		}

		if _, err := buildMouseVisibleLayout(&mux.Window{}, 40, render.GlobalBarHeight+12); err == nil || err.Error() != "window has no layout root" {
			t.Fatalf("buildMouseVisibleLayout nil root error = %v", err)
		}
	})

	t.Run("paneMouseCenter missing pane", func(t *testing.T) {
		t.Parallel()

		layout := mux.NewLeafByID(1, 0, 0, 40, 10)
		if _, _, err := paneMouseCenter(layout, 2, false); err == nil || err.Error() != "pane 2 is not visible in the current client layout" {
			t.Fatalf("paneMouseCenter missing pane error = %v", err)
		}
	})

	t.Run("mouseChunk validation", func(t *testing.T) {
		t.Parallel()

		if _, err := mouseChunk(ansi.MouseLeft, 0, 1, false, false, 0); err == nil || err.Error() != "mouse coordinates must be >= 1" {
			t.Fatalf("mouseChunk invalid coordinate error = %v", err)
		}
		if _, err := mouseChunk(ansi.MouseButton(255), 1, 1, false, false, 0); err == nil || err.Error() != "unsupported mouse button" {
			t.Fatalf("mouseChunk unsupported button error = %v", err)
		}
	})

	t.Run("mouseChunksForAction errors", func(t *testing.T) {
		t.Parallel()

		layout := mux.NewLeafByID(1, 0, 0, 40, 10)
		if _, err := mouseChunksForAction(layout, mouseCommandOptions{kind: mouseCommandClickCoords}, 0, 0); err == nil || err.Error() != "mouse coordinates must be >= 1" {
			t.Fatalf("mouseChunksForAction click error = %v", err)
		}
		if _, err := mouseChunksForAction(layout, mouseCommandOptions{kind: mouseCommandKind(99)}, 0, 0); err == nil || err.Error() != mouseCommandUsage {
			t.Fatalf("mouseChunksForAction default error = %v", err)
		}
	})
}

func TestQueryMouseClientTargetErrors(t *testing.T) {
	t.Parallel()

	t.Run("no client attached", func(t *testing.T) {
		t.Parallel()

		_, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		if _, err := queryMouseClientTarget(sess, 0, "", "", ""); err == nil || err.Error() != "no client attached" {
			t.Fatalf("queryMouseClientTarget no client error = %v", err)
		}
	})

	t.Run("no active window", func(t *testing.T) {
		t.Parallel()

		_, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		clientServerConn, _ := net.Pipe()
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

		if _, err := queryMouseClientTarget(sess, 0, client.ID, "", ""); err == nil || err.Error() != "no window" {
			t.Fatalf("queryMouseClientTarget no window error = %v", err)
		}
	})

	t.Run("pane outside active window", func(t *testing.T) {
		t.Parallel()

		_, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		p1 := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
			return len(data), nil
		})
		p2 := newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: config.AccentColor(1)}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
			return len(data), nil
		})

		w1 := mux.NewWindow(p1, 80, 23)
		w1.ID = 1
		w1.Name = "main"
		w2 := mux.NewWindow(p2, 80, 23)
		w2.ID = 2
		w2.Name = "other"
		setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1, w2}, p1, p2)

		clientServerConn, _ := net.Pipe()
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

		if _, err := queryMouseClientTarget(sess, 0, client.ID, "pane-2", ""); err == nil || err.Error() != `pane "pane-2" is not in the active window` {
			t.Fatalf("queryMouseClientTarget pane error = %v", err)
		}
		if _, err := queryMouseClientTarget(sess, 0, client.ID, "pane-1", "pane-2"); err == nil || err.Error() != `pane "pane-2" is not in the active window` {
			t.Fatalf("queryMouseClientTarget target pane error = %v", err)
		}
	})
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
