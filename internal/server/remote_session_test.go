package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestRemoteStateChangeEventUpdatesRemoteSessionAndProxyPanes(t *testing.T) {
	t.Parallel()

	for _, state := range []proto.ConnState{
		proto.Connecting,
		proto.Connected,
		proto.Reconnecting,
		proto.Disconnected,
	} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			proxy := newProxyPane(2, mux.PaneMeta{
				Name:   "pane-2",
				Host:   "gpu",
				Remote: string(proto.Connected),
			}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
				return len(data), nil
			})
			window := newTestWindowWithPanes(t, sess, 1, "main", proxy)
			setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, proxy)

			mustSessionMutation(t, sess, func(sess *Session) {
				sess.remoteSessions["gpu"] = NewRemoteSession("gpu", RemoteSessionConnect)
			})

			mustSessionMutation(t, sess, func(sess *Session) {
				remoteStateChangeEvent{hostName: "gpu", state: state}.handle(sess)
			})

			snap := mustSessionQuery(t, sess, func(sess *Session) struct {
				sessionState string
				proxyState   string
			} {
				return struct {
					sessionState string
					proxyState   string
				}{
					sessionState: string(sess.remoteSessions["gpu"].State),
					proxyState:   sess.findPaneByID(proxy.ID).Meta.Remote,
				}
			})
			if snap.sessionState != string(state) {
				t.Fatalf("remote session state = %q, want %q", snap.sessionState, state)
			}
			if snap.proxyState != string(state) {
				t.Fatalf("proxy pane state = %q, want %q", snap.proxyState, state)
			}
		})
	}
}

func TestConnectAndDisconnectRemoteSessionLifecycle(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	localPane := newTestPane(sess, 1, "pane-1")
	localWindow := newTestWindowWithPanes(t, sess, 1, "local", localPane)
	setSessionLayoutForTest(t, sess, localWindow.ID, []*mux.Window{localWindow}, localPane)
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.windowCounter.Store(localWindow.ID)
		sess.counter.Store(localPane.ID)
	})
	installTestPaneTransport(t, sess, &stubPaneTransport{
		hostStatusByName: map[string]proto.ConnState{"gpu": proto.Connected},
	}, nil)

	layout := &proto.LayoutSnapshot{
		ActiveWindowID: 5,
		Windows: []proto.WindowSnapshot{{
			ID:           5,
			Name:         "remote",
			ActivePaneID: 7,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 7,
			},
			Panes: []proto.PaneSnapshot{{
				ID:   7,
				Name: "pane-7",
				Task: "shell",
			}},
		}},
	}

	mustSessionMutation(t, sess, func(sess *Session) {
		if err := sess.connectRemoteSession("gpu", layout, RemoteSessionConnect, 0, false); err != nil {
			t.Fatalf("connectRemoteSession() error = %v", err)
		}
	})

	connected := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount    int
		windowCount  int
		hostPaneName string
		state        string
	} {
		var hostPaneName string
		for _, pane := range sess.Panes {
			if pane.Meta.Host == "gpu" {
				hostPaneName = pane.Meta.Name
				break
			}
		}
		return struct {
			paneCount    int
			windowCount  int
			hostPaneName string
			state        string
		}{
			paneCount:    len(sess.Panes),
			windowCount:  len(sess.Windows),
			hostPaneName: hostPaneName,
			state:        string(sess.remoteSessions["gpu"].State),
		}
	})
	if connected.paneCount != 2 {
		t.Fatalf("pane count after connect = %d, want 2", connected.paneCount)
	}
	if connected.windowCount != 2 {
		t.Fatalf("window count after connect = %d, want 2", connected.windowCount)
	}
	if connected.hostPaneName != "pane-7" {
		t.Fatalf("remote proxy pane name = %q, want %q", connected.hostPaneName, "pane-7")
	}
	if connected.state != string(proto.Connected) {
		t.Fatalf("remote session state = %q, want %q", connected.state, proto.Connected)
	}

	mustSessionMutation(t, sess, func(sess *Session) {
		if err := sess.disconnectRemoteSession("gpu"); err != nil {
			t.Fatalf("disconnectRemoteSession() error = %v", err)
		}
	})

	disconnected := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount   int
		windowCount int
		hasRemote   bool
	} {
		hasRemote := false
		for _, pane := range sess.Panes {
			if pane.Meta.Host == "gpu" {
				hasRemote = true
				break
			}
		}
		_, stillConnected := sess.remoteSessions["gpu"]
		return struct {
			paneCount   int
			windowCount int
			hasRemote   bool
		}{
			paneCount:   len(sess.Panes),
			windowCount: len(sess.Windows),
			hasRemote:   hasRemote || stillConnected,
		}
	})
	if disconnected.paneCount != 1 {
		t.Fatalf("pane count after disconnect = %d, want 1", disconnected.paneCount)
	}
	if disconnected.windowCount != 1 {
		t.Fatalf("window count after disconnect = %d, want 1", disconnected.windowCount)
	}
	if disconnected.hasRemote {
		t.Fatal("remote session state should be removed after disconnect")
	}
}

func TestConnectRemoteSessionRejectsModeMismatch(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	mustSessionMutation(t, sess, func(sess *Session) {
		rs := NewRemoteSession("gpu", RemoteSessionTakeover)
		rs.PlaceholderPane = 42
		sess.remoteSessions["gpu"] = rs
	})

	layout := &proto.LayoutSnapshot{
		ActiveWindowID: 1,
		Windows: []proto.WindowSnapshot{{
			ID:           1,
			Name:         "remote",
			ActivePaneID: 7,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 7,
			},
			Panes: []proto.PaneSnapshot{{
				ID:   7,
				Name: "pane-7",
			}},
		}},
	}

	var err error
	mustSessionMutation(t, sess, func(sess *Session) {
		err = sess.connectRemoteSession("gpu", layout, RemoteSessionConnect, 0, false)
	})
	if err == nil || !strings.Contains(err.Error(), `remote host "gpu" already uses takeover mode`) {
		t.Fatalf("connectRemoteSession() error = %v, want mode mismatch", err)
	}

	snap := mustSessionQuery(t, sess, func(sess *Session) struct {
		mode        RemoteSessionMode
		placeholder uint32
	} {
		rs := sess.remoteSessions["gpu"]
		return struct {
			mode        RemoteSessionMode
			placeholder uint32
		}{
			mode:        rs.Mode,
			placeholder: rs.PlaceholderPane,
		}
	})
	if snap.mode != RemoteSessionTakeover {
		t.Fatalf("remote session mode = %q, want %q", snap.mode, RemoteSessionTakeover)
	}
	if snap.placeholder != 42 {
		t.Fatalf("placeholder pane = %d, want 42", snap.placeholder)
	}
}

func TestRemoteLayoutEventLogsApplyErrors(t *testing.T) {
	t.Parallel()

	logger, buf := newAuditTestLogger()
	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.logger = logger

	mustSessionMutation(t, sess, func(sess *Session) {
		rs := NewRemoteSession("gpu", RemoteSessionTakeover)
		rs.PlaceholderPane = 99
		sess.remoteSessions["gpu"] = rs
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		remoteLayoutEvent{
			hostName: "gpu",
			layout: &proto.LayoutSnapshot{
				ActiveWindowID: 1,
				Windows: []proto.WindowSnapshot{{
					ID:           1,
					Name:         "remote",
					ActivePaneID: 7,
					Root: proto.CellSnapshot{
						X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 7,
					},
					Panes: []proto.PaneSnapshot{{
						ID:   7,
						Name: "pane-7",
					}},
				}},
			},
		}.handle(sess)
	})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "remote layout apply failed") {
		t.Fatalf("warn log = %q, want remote layout apply failed", logOutput)
	}
	if !strings.Contains(logOutput, "remote_layout_apply") {
		t.Fatalf("warn log = %q, want remote_layout_apply event", logOutput)
	}
	if !strings.Contains(logOutput, "takeover pane 99 not found") {
		t.Fatalf("warn log = %q, want apply error text", logOutput)
	}
}
