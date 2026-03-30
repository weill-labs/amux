package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestPrepareRemotePaneUsesConfiguredTransportHostColor(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{createPaneRemote: 77}
	installTestPaneTransport(t, sess, transport, func(hostName string) string {
		if hostName == "dev" {
			return "123abc"
		}
		return "ffffff"
	})

	pane, err := sess.prepareRemotePane("dev", 80, 23)
	if err != nil {
		t.Fatalf("prepareRemotePane: %v", err)
	}
	if pane.Meta.Host != "dev" {
		t.Fatalf("pane host = %q, want dev", pane.Meta.Host)
	}
	if pane.Meta.Color != "123abc" {
		t.Fatalf("pane color = %q, want 123abc", pane.Meta.Color)
	}
	if pane.Meta.Remote != string(proto.Connected) {
		t.Fatalf("pane remote state = %q, want %q", pane.Meta.Remote, proto.Connected)
	}
	if len(transport.createPaneCalls) != 1 {
		t.Fatalf("CreatePane calls = %d, want 1", len(transport.createPaneCalls))
	}
	call := transport.createPaneCalls[0]
	if call.hostName != "dev" || call.sessionName != sess.Name {
		t.Fatalf("CreatePane call = %+v, want host dev and session %q", call, sess.Name)
	}
}

func TestServerSetupPaneTransportInstallsTransportOnSessions(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
	srv.SetupPaneTransport(func(hostName string) string {
		if hostName == "dev" {
			return "beaded"
		}
		return "faded0"
	}, func(PaneTransportHooks) proto.PaneTransport {
		return transport
	})

	if sess.RemoteManager != transport {
		t.Fatal("SetupPaneTransport should install the transport on the session")
	}
}

func TestConfigurePaneTakeoverInstallsPaneTransportForProxyWrites(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &stubPaneTransport{}
	sess.configurePaneTakeover(transport)

	if sess.RemoteManager != transport {
		t.Fatal("configurePaneTakeover should install takeover transports that also satisfy PaneTransport")
	}

	n, err := sess.remoteWriteOverride(42)([]byte("pwd\n"))
	if err != nil {
		t.Fatalf("remoteWriteOverride: %v", err)
	}
	if n != len("pwd\n") {
		t.Fatalf("remoteWriteOverride wrote %d bytes, want %d", n, len("pwd\n"))
	}
	if len(transport.sendInputCalls) != 1 {
		t.Fatalf("SendInput calls = %d, want 1", len(transport.sendInputCalls))
	}
	call := transport.sendInputCalls[0]
	if call.localPaneID != 42 || string(call.data) != "pwd\n" {
		t.Fatalf("SendInput call = %+v, want pane 42 with pwd input", call)
	}
}
