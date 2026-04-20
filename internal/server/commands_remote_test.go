package server

import (
	"slices"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestRemoteCommandContextFinalizeDisconnect(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	ctx := remoteCommandContext{&CommandContext{Sess: sess}}
	res := ctx.FinalizeDisconnect("gpu")
	if res.Mutate == nil {
		t.Fatal("FinalizeDisconnect() should return a mutation result")
	}

	got := res.Mutate()
	if got.Err != nil {
		t.Fatalf("FinalizeDisconnect() mutate error = %v", got.Err)
	}
	if got.Output != "Disconnected from gpu\n" {
		t.Fatalf("FinalizeDisconnect() output = %q, want %q", got.Output, "Disconnected from gpu\n")
	}
	if !got.BroadcastLayout {
		t.Fatal("FinalizeDisconnect() should broadcast layout updates")
	}
}

func TestRunConnectDefaultsToRemoteMainSession(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	installTestPaneTransport(t, sess, transport, nil)

	res := runConnect(&CommandContext{Srv: srv, Sess: sess, Args: []string{"gpu"}})
	if res.Err != nil {
		t.Fatalf("runConnect() error = %v", res.Err)
	}
	if len(transport.connectHostCalls) != 1 {
		t.Fatalf("ConnectHost calls = %d, want 1", len(transport.connectHostCalls))
	}
	call := transport.connectHostCalls[0]
	if call.hostName != "gpu" {
		t.Fatalf("ConnectHost host = %q, want gpu", call.hostName)
	}
	if call.sessionName != DefaultSessionName {
		t.Fatalf("ConnectHost session = %q, want %q", call.sessionName, DefaultSessionName)
	}
}

func TestRunConnectUsesRequestedRemoteSession(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	installTestPaneTransport(t, sess, transport, nil)

	res := runConnect(&CommandContext{Srv: srv, Sess: sess, Args: []string{"gpu", "--session", "work"}})
	if res.Err != nil {
		t.Fatalf("runConnect() error = %v", res.Err)
	}
	if len(transport.connectHostCalls) != 1 {
		t.Fatalf("ConnectHost calls = %d, want 1", len(transport.connectHostCalls))
	}
	if got := transport.connectHostCalls[0].sessionName; got != "work" {
		t.Fatalf("ConnectHost session = %q, want work", got)
	}
}

func TestRunConnectUsesManagedSessionWhenRequested(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	installTestPaneTransport(t, sess, transport, nil)

	res := runConnect(&CommandContext{Srv: srv, Sess: sess, Args: []string{"gpu", "--session-per-client"}})
	if res.Err != nil {
		t.Fatalf("runConnect() error = %v", res.Err)
	}
	if len(transport.connectHostCalls) != 1 {
		t.Fatalf("ConnectHost calls = %d, want 1", len(transport.connectHostCalls))
	}
	wantSession := managedSessionName(sess.Name)
	if got := transport.connectHostCalls[0].sessionName; got != wantSession {
		t.Fatalf("ConnectHost session = %q, want %q", got, wantSession)
	}
}

func TestRunConnectRejectsConflictingSessionFlags(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	installTestPaneTransport(t, sess, transport, nil)

	res := runConnect(&CommandContext{Srv: srv, Sess: sess, Args: []string{"gpu", "--session", "work", "--session-per-client"}})
	if res.Err == nil {
		t.Fatal("runConnect() error = nil, want conflict error")
	}
	if got := res.Err.Error(); got != "usage: connect <host> [--session <name> | --session-per-client]" {
		t.Fatalf("runConnect() error = %q, want connect usage", got)
	}
	if len(transport.connectHostCalls) != 0 {
		t.Fatalf("ConnectHost calls = %d, want 0", len(transport.connectHostCalls))
	}
}

func TestRunConnectMirrorsExistingRemoteMainSessionLayout(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{
		hostStatusByName: map[string]proto.ConnState{"gpu": proto.Connected},
		connectLayout: &proto.LayoutSnapshot{
			ActiveWindowID: 7,
			Windows: []proto.WindowSnapshot{{
				ID:           7,
				Name:         "remote-main",
				ActivePaneID: 11,
				Root: proto.CellSnapshot{
					X: 0, Y: 0, W: 80, H: 24, IsLeaf: false, Dir: 0,
					Children: []proto.CellSnapshot{
						{X: 0, Y: 0, W: 40, H: 24, IsLeaf: true, Dir: -1, PaneID: 11},
						{X: 40, Y: 0, W: 40, H: 24, IsLeaf: true, Dir: -1, PaneID: 12},
					},
				},
				Panes: []proto.PaneSnapshot{
					{ID: 11, Name: "pane-11"},
					{ID: 12, Name: "remote-main-2"},
				},
			}},
		},
	}
	installTestPaneTransport(t, sess, transport, nil)

	res := runConnect(&CommandContext{Srv: srv, Sess: sess, Args: []string{"gpu"}})
	if res.Err != nil {
		t.Fatalf("runConnect() error = %v", res.Err)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		remoteNames []string
		windowNames []string
	} {
		remoteNames := make([]string, 0, len(sess.Panes))
		for _, pane := range sess.Panes {
			if pane.Meta.Host == "gpu" {
				remoteNames = append(remoteNames, pane.Meta.Name)
			}
		}
		slices.Sort(remoteNames)
		windowNames := make([]string, 0, len(sess.Windows))
		for _, window := range sess.Windows {
			windowNames = append(windowNames, window.Name)
		}
		return struct {
			remoteNames []string
			windowNames []string
		}{
			remoteNames: remoteNames,
			windowNames: windowNames,
		}
	})

	if len(state.remoteNames) != 2 {
		t.Fatalf("remote pane count = %d, want 2", len(state.remoteNames))
	}
	if state.remoteNames[0] != "pane-11" || state.remoteNames[1] != "remote-main-2" {
		t.Fatalf("remote pane names = %v, want [pane-11 remote-main-2]", state.remoteNames)
	}
	if len(state.windowNames) != 1 || state.windowNames[0] != "remote-main@gpu" {
		t.Fatalf("window names = %v, want [remote-main@gpu]", state.windowNames)
	}
}
