package remote

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

func newKeyedHostConn(t *testing.T, address string) *HostConn {
	t.Helper()

	t.Setenv("SSH_AUTH_SOCK", "")
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writeTestKey(t, keyPath)

	return NewHostConn("test-host", config.Host{
		Address:      address,
		User:         "testuser",
		IdentityFile: keyPath,
	}, "", nil, nil, nil)
}

func writeFakeRemoteAmux(t *testing.T, ts *testSSH) {
	t.Helper()

	path := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fake amux dir: %v", err)
	}

	script := `#!/bin/sh
set -eu
case "${1:-}" in
  install-terminfo)
    exit 0
    ;;
  _server)
    session="${2:?missing session}"
    sock="/tmp/amux-$(id -u)/$session"
    mkdir -p "$(dirname "$sock")"
    python3 - "$sock" <<'PY' >/dev/null 2>&1 &
import os, socket, sys, time
sock = sys.argv[1]
try:
    os.unlink(sock)
except FileNotFoundError:
    pass
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.bind(sock)
s.listen(1)
time.sleep(5)
s.close()
PY
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake amux script: %v", err)
	}
}

func TestHostConnEnsureConnectedConcurrentFailure(t *testing.T) {
	hc := newKeyedHostConn(t, "127.0.0.1:1")
	defer hc.Close()

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errs <- hc.EnsureConnected("session")
		}()
	}

	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil || !strings.Contains(err.Error(), "SSH dial") {
			t.Fatalf("EnsureConnected() error = %v, want SSH dial failure", err)
		}
	}

	if got := hc.State(); got != Disconnected {
		t.Fatalf("state after failed connect = %q, want %q", got, Disconnected)
	}
}

func TestHostConnEnsureConnectedForTakeoverErrors(t *testing.T) {
	hc := newKeyedHostConn(t, "127.0.0.1:1")

	err := hc.EnsureConnectedForTakeover("session", "1000", "127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "SSH dial") {
		t.Fatalf("EnsureConnectedForTakeover() error = %v, want SSH dial failure", err)
	}

	hc.Close()
	if err := hc.EnsureConnected("session"); !errors.Is(err, errHostConnClosed) {
		t.Fatalf("EnsureConnected() after Close = %v, want %v", err, errHostConnClosed)
	}
	if err := hc.EnsureConnectedForTakeover("session", "1000", "127.0.0.1:22"); !errors.Is(err, errHostConnClosed) {
		t.Fatalf("EnsureConnectedForTakeover() after Close = %v, want %v", err, errHostConnClosed)
	}
}

func TestEnsureRemoteServerAndWaitForSocket(t *testing.T) {
	ts := startTestSSH(t)
	writeFakeRemoteAmux(t, ts)

	sessionName := "default@test"
	sockPath := socketPath("1000", sessionName)
	actualSockPath := socketPath(strconv.Itoa(os.Getuid()), sessionName)
	_ = os.Remove(sockPath)
	_ = os.Remove(actualSockPath)
	_ = os.MkdirAll(filepath.Dir(sockPath), 0o755)
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(actualSockPath)
	})

	if err := ensureRemoteServer(ts.Client, sockPath, sessionName); err != nil {
		t.Fatalf("ensureRemoteServer: %v", err)
	}
	if err := waitForSocket(ts.Client, actualSockPath, time.Second); err != nil {
		t.Fatalf("waitForSocket after ensureRemoteServer: %v", err)
	}
}

func TestWaitForSocketTimeout(t *testing.T) {
	ts := startTestSSH(t)

	err := waitForSocket(ts.Client, socketPath("1000", "missing"), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not appear") {
		t.Fatalf("waitForSocket() error = %v, want timeout", err)
	}
}

func TestHostConnDoConnectWithAddrReturnsDialErrorAfterRemoteSetup(t *testing.T) {
	ts := startTestSSH(t)
	writeFakeRemoteAmux(t, ts)
	t.Setenv("SSH_AUTH_SOCK", "")

	hc := NewHostConn("test-host", config.Host{
		Address:      ts.Addr,
		User:         "testuser",
		IdentityFile: ts.KeyFile,
	}, "", nil, nil, nil)
	defer hc.Close()

	outcome, err := hc.doConnectWithAddr("default", ts.Addr)
	if err == nil || !strings.Contains(err.Error(), "dialing remote socket") {
		t.Fatalf("doConnectWithAddr() error = %v, want dialing remote socket failure", err)
	}
	if outcome != nil {
		t.Fatalf("doConnectWithAddr() outcome = %#v, want nil", outcome)
	}
}

func TestHostConnDoConnectTakeoverWaitsForSocketThenReturnsDialError(t *testing.T) {
	ts := startTestSSH(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	remoteUID := "1000"
	sessionName := "takeover@test"
	sockPath := socketPath(remoteUID, sessionName)
	_ = os.MkdirAll(filepath.Dir(sockPath), 0o755)
	_ = os.Remove(sockPath)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	defer listener.Close()
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
	})

	hc := NewHostConn("test-host", config.Host{
		Address:      ts.Addr,
		User:         "testuser",
		IdentityFile: ts.KeyFile,
	}, "", nil, nil, nil)
	defer hc.Close()

	outcome, err := hc.doConnectTakeover(sessionName, remoteUID, ts.Addr)
	if err == nil || !strings.Contains(err.Error(), "dialing remote socket") {
		t.Fatalf("doConnectTakeover() error = %v, want dialing remote socket failure", err)
	}
	if outcome != nil {
		t.Fatalf("doConnectTakeover() outcome = %#v, want nil", outcome)
	}
}

func TestHostConnRunCommandAndCreateRemotePaneDialErrors(t *testing.T) {
	ts := startTestSSH(t)

	hc := NewHostConn("test-host", config.Host{}, "", nil, nil, nil)
	defer hc.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.sshClient = ts.Client
		hc.sessionName = "default@test"
		hc.remoteUID = "1000"
	})

	output, err := hc.runCommand("list", nil)
	if err == nil || !strings.Contains(err.Error(), "dialing remote socket") {
		t.Fatalf("runCommand() error = %v, want dialing remote socket failure", err)
	}
	if output != "" {
		t.Fatalf("runCommand() output = %q, want empty", output)
	}

	if _, err := hc.CreateRemotePane(42); err == nil || !strings.Contains(err.Error(), "dialing remote socket") {
		t.Fatalf("CreateRemotePane() error = %v, want dialing remote socket failure", err)
	}
}

func TestHostConnReadLoopHandlesOutputAndDisconnectPaths(t *testing.T) {
	t.Run("routes pane output through layout", func(t *testing.T) {
		outputs := make(chan []byte, 1)
		hc := NewHostConn("test", config.Host{}, "", func(_ uint32, data []byte) {
			outputs <- append([]byte(nil), data...)
		}, nil, nil)
		defer hc.Close()
		hc.RegisterPane(10, 100)

		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()

		done := make(chan struct{})
		go func() {
			hc.readLoop(clientConn)
			close(done)
		}()

		// Send a layout that includes remote pane 100 so the mapping
		// survives layout-based disappearance detection.
		layout := testLayoutSnapshot()
		layout.Panes = append(layout.Panes, proto.PaneSnapshot{ID: 100, Name: "pane-100"})
		layout.Windows[0].Panes = append(layout.Windows[0].Panes, proto.PaneSnapshot{ID: 100, Name: "pane-100"})
		if err := proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeLayout, Layout: layout}); err != nil {
			t.Fatalf("WriteMsg layout: %v", err)
		}
		if err := proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 100, PaneData: []byte("hello")}); err != nil {
			t.Fatalf("WriteMsg pane output: %v", err)
		}

		select {
		case got := <-outputs:
			if string(got) != "hello" {
				t.Fatalf("output = %q, want %q", got, "hello")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for pane output")
		}

		serverConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("readLoop did not exit after connection close")
		}
	})

	t.Run("layout disappearance removes mapped pane", func(t *testing.T) {
		exits := make(chan uint32, 1)
		hc := NewHostConn("test", config.Host{}, "", nil, func(localPaneID uint32, _ string) {
			exits <- localPaneID
		}, nil)
		defer hc.Close()
		hc.RegisterPane(10, 100)
		hc.RegisterPane(20, 200)

		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()

		done := make(chan struct{})
		go func() {
			hc.readLoop(clientConn)
			close(done)
		}()

		layout := testLayoutSnapshot()
		layout.Panes = []proto.PaneSnapshot{{ID: 200, Name: "pane-200"}}
		layout.Windows[0].Panes = []proto.PaneSnapshot{{ID: 200, Name: "pane-200"}}
		layout.ActivePaneID = 200
		layout.Windows[0].ActivePaneID = 200
		if err := proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeLayout, Layout: layout}); err != nil {
			t.Fatalf("WriteMsg layout: %v", err)
		}

		select {
		case got := <-exits:
			if got != 10 {
				t.Fatalf("exit callback local pane = %d, want 10", got)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for pane exit callback after layout disappearance")
		}

		var (
			pane10Present  bool
			remote100Exist bool
			pane20RemoteID uint32
		)
		testInActor(hc, func(hc *HostConn) {
			_, pane10Present = hc.localToRemote[10]
			_, remote100Exist = hc.remoteToLocal[100]
			pane20RemoteID = hc.localToRemote[20]
		})
		if pane10Present {
			t.Fatal("localToRemote[10] should be removed after layout disappearance")
		}
		if remote100Exist {
			t.Fatal("remoteToLocal[100] should be removed after layout disappearance")
		}
		if pane20RemoteID != 200 {
			t.Fatalf("localToRemote[20] = %d, want 200", pane20RemoteID)
		}

		serverConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("readLoop did not exit after connection close")
		}
	})

	t.Run("returns on explicit exit message", func(t *testing.T) {
		hc := NewHostConn("test", config.Host{}, "", nil, nil, nil)
		defer hc.Close()

		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()

		done := make(chan struct{})
		go func() {
			hc.readLoop(clientConn)
			close(done)
		}()

		if err := proto.WriteMsg(serverConn, &proto.Message{Type: proto.MsgTypeExit}); err != nil {
			t.Fatalf("WriteMsg exit: %v", err)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("readLoop did not exit after MsgTypeExit")
		}
	})

	t.Run("returns on partial message read", func(t *testing.T) {
		hc := NewHostConn("test", config.Host{}, "", nil, nil, nil)
		defer hc.Close()

		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()

		done := make(chan struct{})
		go func() {
			hc.readLoop(clientConn)
			close(done)
		}()

		var buf bytes.Buffer
		if err := proto.WriteMsg(&buf, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: 5, PaneData: []byte("hello")}); err != nil {
			t.Fatalf("WriteMsg buffer: %v", err)
		}
		raw := buf.Bytes()
		if _, err := serverConn.Write(raw[:6]); err != nil {
			t.Fatalf("partial write: %v", err)
		}
		serverConn.Close()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("readLoop did not exit after partial read")
		}
	})
}

func TestManagerCreatePaneAndAttachForTakeoverFailures(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writeTestKey(t, keyPath)
	t.Setenv("SSH_AUTH_SOCK", "")

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {
			Type:         "remote",
			Address:      "127.0.0.1:1",
			User:         "testuser",
			IdentityFile: keyPath,
		},
	}}
	mgr := NewManager(cfg, "build-hash")
	mgr.SetCallbacks(nil, nil, nil)
	t.Cleanup(mgr.Shutdown)

	if _, err := mgr.CreatePane("dev", 41, "default"); err == nil || !strings.Contains(err.Error(), "connecting to dev: SSH dial") {
		t.Fatalf("CreatePane() error = %v, want SSH dial failure", err)
	}

	mgr.mu.Lock()
	hostConn := mgr.hosts["dev"]
	localHost := mgr.localToHost[41]
	mgr.mu.Unlock()
	if hostConn == nil || localHost != "dev" {
		t.Fatalf("manager state after CreatePane = host=%v localToHost=%q, want installed host and dev mapping", hostConn != nil, localHost)
	}

	err := mgr.AttachForTakeover("dev", "127.0.0.1:1", "testuser", "1000", "default@test", map[uint32]uint32{
		51: 151,
		52: 152,
	})
	if err == nil || !strings.Contains(err.Error(), "SSH dial") {
		t.Fatalf("AttachForTakeover() error = %v, want SSH dial failure", err)
	}

	mgr.mu.Lock()
	host51 := mgr.localToHost[51]
	host52 := mgr.localToHost[52]
	mgr.mu.Unlock()
	if host51 != "dev" || host52 != "dev" {
		t.Fatalf("takeover mappings = (%q, %q), want both dev", host51, host52)
	}

	testInActor(hostConn, func(hc *HostConn) {
		if hc.localToRemote[51] != 151 || hc.localToRemote[52] != 152 {
			t.Fatalf("localToRemote = %#v, want registered takeover panes", hc.localToRemote)
		}
	})

	if err := mgr.SendInput(51, []byte("hello")); err != nil {
		t.Fatalf("SendInput() = %v, want nil for known pane", err)
	}
	if err := mgr.SendResize(51, 80, 24); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("SendResize() error = %v, want not connected", err)
	}
}

func TestHostConnReconnectAndReconnectDonePaths(t *testing.T) {
	t.Run("public reconnect returns closed error after Close", func(t *testing.T) {
		hc := newKeyedHostConn(t, "127.0.0.1:1")
		hc.Close()

		if err := hc.Reconnect("default"); !errors.Is(err, errHostConnClosed) {
			t.Fatalf("Reconnect() after Close = %v, want %v", err, errHostConnClosed)
		}
	})

	t.Run("reconnect command returns ssh dial failure", func(t *testing.T) {
		hc := newKeyedHostConn(t, "127.0.0.1:1")
		defer hc.Close()

		if err := hc.Reconnect("default"); err == nil || !strings.Contains(err.Error(), "SSH dial") {
			t.Fatalf("Reconnect() error = %v, want SSH dial failure", err)
		}
		if got := hc.State(); got != Disconnected {
			t.Fatalf("state after failed reconnect = %q, want %q", got, Disconnected)
		}
	})

	t.Run("reconnectDoneEvent discards stale outcomes and applies active ones", func(t *testing.T) {
		hc := NewHostConn("test", config.Host{}, "", nil, nil, nil)
		defer hc.Close()

		serverConn1, clientConn1 := net.Pipe()
		defer serverConn1.Close()
		staleDone := make(chan struct{})
		testInActor(hc, func(hc *HostConn) {
			hc.state = Disconnected
			(reconnectDoneEvent{
				outcome: &connectOutcome{amuxConn: clientConn1},
				done:    staleDone,
			}).handle(hc)
		})
		select {
		case <-staleDone:
		case <-time.After(time.Second):
			t.Fatal("stale reconnectDoneEvent did not close done channel")
		}
		if _, err := serverConn1.Write([]byte("x")); err == nil {
			t.Fatal("stale reconnect outcome should close the discarded connection")
		}

		serverConn2, clientConn2 := net.Pipe()
		appliedDone := make(chan struct{})
		testInActor(hc, func(hc *HostConn) {
			hc.state = Reconnecting
			(reconnectDoneEvent{
				outcome: &connectOutcome{
					amuxConn:    clientConn2,
					sessionName: "default@test",
					remoteUID:   "1000",
					connectAddr: "127.0.0.1:22",
				},
				done: appliedDone,
			}).handle(hc)
		})
		select {
		case <-appliedDone:
		case <-time.After(time.Second):
			t.Fatal("active reconnectDoneEvent did not close done channel")
		}
		if got := hc.State(); got != Connected {
			t.Fatalf("state after reconnectDoneEvent = %q, want %q", got, Connected)
		}
		serverConn2.Close()
	})
}
