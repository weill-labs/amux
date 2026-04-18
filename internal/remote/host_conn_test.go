package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
	transportssh "github.com/weill-labs/amux/internal/transport/ssh"
)

// testInActor runs fn inside the HostConn actor goroutine and waits for it
// to complete. This is the sole mechanism for tests to inspect or mutate
// actor-owned state safely.
type testInActorEvent struct {
	fn   func(*HostConn)
	done chan struct{}
}

func (e testInActorEvent) handle(hc *HostConn) {
	e.fn(hc)
	close(e.done)
}

func testInActor(hc *HostConn, fn func(*HostConn)) {
	done := make(chan struct{})
	hc.enqueue(testInActorEvent{fn: fn, done: done})
	<-done
}

func TestBuildEnsureServerCmd(t *testing.T) {
	t.Parallel()

	cmd := transportssh.BuildEnsureServerCmd("/tmp/amux-1000/main", "main@myhost")

	if !strings.Contains(cmd, `[ ! -S /tmp/amux-1000/main ]`) {
		t.Error("command should check socket existence")
	}
	if !strings.Contains(cmd, "${AMUX_BIN:-") {
		t.Error("command should check AMUX_BIN env var first")
	}
	if !strings.Contains(cmd, "~/.local/bin/amux") {
		t.Error("command should try ~/.local/bin/amux as fallback")
	}
	if !strings.Contains(cmd, "install-terminfo") {
		t.Error("command should install terminfo before starting the remote server")
	}
	if !strings.Contains(cmd, `_server main@myhost`) {
		t.Error("command should pass session name to _server")
	}
	if !strings.Contains(cmd, "command -v amux") {
		t.Error("command should fall back to amux in PATH")
	}
}

func TestNormalizeAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "bare hostname", addr: "myhost", want: "myhost:22"},
		{name: "bare IP", addr: "10.0.0.1", want: "10.0.0.1:22"},
		{name: "with port", addr: "10.0.0.1:2222", want: "10.0.0.1:2222"},
		{name: "with default port", addr: "myhost:22", want: "myhost:22"},
		{name: "IPv6 bare", addr: "::1", want: "::1:22"},
		{name: "IPv6 bracketed with port", addr: "[::1]:22", want: "[::1]:22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeAddr(tt.addr); got != tt.want {
				t.Errorf("normalizeAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestManagedSessionName(t *testing.T) {
	t.Parallel()

	name := ManagedSessionName("main")
	hostname, _ := os.Hostname()

	if !strings.HasPrefix(name, "main@") {
		t.Errorf("ManagedSessionName should start with session name, got %q", name)
	}
	if !strings.HasSuffix(name, "@"+hostname) {
		t.Errorf("ManagedSessionName should end with @hostname, got %q", name)
	}
}

func TestSocketPath(t *testing.T) {
	t.Parallel()

	path := socketPath("1000", "main@myhost")
	if path != "/tmp/amux-1000/main@myhost" {
		t.Errorf("socketPath = %q, want /tmp/amux-1000/main@myhost", path)
	}
}

func TestNewHostConn(t *testing.T) {
	t.Parallel()

	cfg := config.Host{Type: "remote", Address: "10.0.0.1", User: "ubuntu"}
	var mu sync.Mutex
	called := false
	hc := NewHostConn("test-host", cfg, "abc1234",
		func(uint32, []byte) {},
		func(uint32, string) {},
		func(string, ConnState) {
			mu.Lock()
			called = true
			mu.Unlock()
		},
	)
	defer hc.Close()

	if hc.name != "test-host" {
		t.Errorf("name = %q, want test-host", hc.name)
	}
	if hc.buildHash != "abc1234" {
		t.Errorf("buildHash = %q, want abc1234", hc.buildHash)
	}
	if hc.State() != Disconnected {
		t.Errorf("initial state = %q, want disconnected", hc.State())
	}

	// setState through actor should trigger callback
	testInActor(hc, func(hc *HostConn) {
		hc.setState(Connecting)
	})

	mu.Lock()
	c := called
	mu.Unlock()
	if !c {
		t.Error("onStateChange callback not called")
	}
	if hc.State() != Connecting {
		t.Errorf("state after setState = %q, want connecting", hc.State())
	}
}

func TestHostConnStateTransitions(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var transitions []ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) {
			mu.Lock()
			transitions = append(transitions, s)
			mu.Unlock()
		},
	)
	defer hc.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.setState(Connecting)
		hc.setState(Connected)
		hc.setState(Reconnecting)
		hc.setState(Disconnected)
	})

	mu.Lock()
	defer mu.Unlock()
	want := []ConnState{Connecting, Connected, Reconnecting, Disconnected}
	if len(transitions) != len(want) {
		t.Fatalf("got %d transitions, want %d", len(transitions), len(want))
	}
	for i, s := range transitions {
		if s != want[i] {
			t.Errorf("transition[%d] = %q, want %q", i, s, want[i])
		}
	}
}

func TestReconnectTargetUsesStoredConnectAddr(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test-remote", config.Host{Address: "wrong-host"}, "hash", nil, nil, nil)
	defer hc.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.sessionName = "main@test"
		hc.remoteUID = "1000"
		hc.connectAddr = "127.0.0.1:2222"
		hc.takeoverMode = true

		target := hc.reconnectTarget()
		if target.sessionName != "main@test" {
			t.Fatalf("target.sessionName = %q, want main@test", target.sessionName)
		}
		if target.remoteUID != "1000" {
			t.Fatalf("target.remoteUID = %q, want 1000", target.remoteUID)
		}
		if target.connectAddr != "127.0.0.1:2222" {
			t.Fatalf("target.connectAddr = %q, want 127.0.0.1:2222", target.connectAddr)
		}
		if !target.takeover {
			t.Fatal("target.takeover = false, want true")
		}
	})
}

func TestReconnectTargetFallsBackToHostName(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test-remote", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	target := hc.reconnectTarget()
	if target.connectAddr != "test-remote:22" {
		t.Fatalf("target.connectAddr = %q, want test-remote:22", target.connectAddr)
	}
}

func TestRemovePane(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	// Register pane mappings through actor
	hc.RegisterPane(10, 100)
	hc.RegisterPane(20, 200)

	// Remove one mapping
	hc.RemovePane(10)

	// Verify via actor
	testInActor(hc, func(hc *HostConn) {
		if _, ok := hc.localToRemote[10]; ok {
			t.Error("localToRemote[10] should be deleted after RemovePane")
		}
		if _, ok := hc.remoteToLocal[100]; ok {
			t.Error("remoteToLocal[100] should be deleted after RemovePane")
		}
		if hc.localToRemote[20] != 200 {
			t.Errorf("localToRemote[20] = %d, want 200 (should survive)", hc.localToRemote[20])
		}
	})

	// Removing unknown pane should be a no-op
	hc.RemovePane(999)
}

func TestDisconnect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var lastState ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) {
			mu.Lock()
			lastState = s
			mu.Unlock()
		},
	)
	defer hc.Close()

	// Simulate connected state through actor
	testInActor(hc, func(hc *HostConn) {
		hc.state = Connected
	})

	hc.Disconnect()

	mu.Lock()
	s := lastState
	mu.Unlock()
	if s != Disconnected {
		t.Errorf("state after Disconnect = %q, want disconnected", s)
	}
}

func TestHandleDisconnect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var states []ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) {
			mu.Lock()
			states = append(states, s)
			mu.Unlock()
		},
	)
	defer hc.Close()

	// Not connected — readDisconnectEvent should be a no-op
	testInActor(hc, func(hc *HostConn) {
		(readDisconnectEvent{}).handle(hc)
	})
	mu.Lock()
	if len(states) > 0 {
		t.Error("readDisconnectEvent on non-connected should not fire callback")
	}
	mu.Unlock()

	// Simulate Connected state
	testInActor(hc, func(hc *HostConn) {
		hc.state = Connected
	})

	// Fire disconnect event through actor
	testInActor(hc, func(hc *HostConn) {
		(readDisconnectEvent{}).handle(hc)
	})

	if hc.State() != Reconnecting {
		t.Errorf("state after readDisconnectEvent = %q, want reconnecting", hc.State())
	}

	// Second disconnect should be a no-op (already reconnecting)
	mu.Lock()
	countBefore := len(states)
	mu.Unlock()

	testInActor(hc, func(hc *HostConn) {
		(readDisconnectEvent{}).handle(hc)
	})

	mu.Lock()
	countAfter := len(states)
	mu.Unlock()
	if countAfter != countBefore {
		t.Error("duplicate readDisconnectEvent should be a no-op")
	}
}

func TestSendInputDisconnected(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	err := hc.SendInput(42, []byte("hello"))
	if err != nil {
		t.Errorf("SendInput on disconnected = %v, want nil", err)
	}
}

func TestSendResizeDisconnected(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	err := hc.SendResize(42, 80, 24)
	if err != nil {
		t.Errorf("SendResize on disconnected = %v, want nil", err)
	}
}

func TestBufferedInputFlushesAfterConnect(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	hc.BeginInputBuffering()
	hc.RegisterPane(42, 100)
	if err := hc.SendInput(42, []byte("hello")); err != nil {
		t.Fatalf("SendInput while buffering: %v", err)
	}
	if err := hc.SendInput(42, []byte(" world")); err != nil {
		t.Fatalf("SendInput while buffering: %v", err)
	}

	testInActor(hc, func(hc *HostConn) {
		if got := len(hc.pendingInputs); got != 2 {
			t.Fatalf("pendingInputs = %d, want 2", got)
		}
	})

	done := make(chan struct{})
	go func() {
		testInActor(hc, func(hc *HostConn) {
			hc.applyOutcome(&connectOutcome{
				amuxConn:    clientConn,
				amuxReader:  remoteTestReader(clientConn),
				amuxWriter:  remoteTestWriter(clientConn),
				sessionName: "session",
				remoteUID:   "1000",
				takeover:    true,
			})
		})
		close(done)
	}()

	msg, err := remoteTestReader(serverConn).ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg first buffered input: %v", err)
	}
	if msg.Type != proto.MsgTypeInputPane || msg.PaneID != 100 || string(msg.PaneData) != "hello" {
		t.Fatalf("first buffered message = %#v, want input pane 100 hello", msg)
	}

	msg, err = remoteTestReader(serverConn).ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg second buffered input: %v", err)
	}
	if msg.Type != proto.MsgTypeInputPane || msg.PaneID != 100 || string(msg.PaneData) != " world" {
		t.Fatalf("second buffered message = %#v, want input pane 100 world", msg)
	}
	<-done

	testInActor(hc, func(hc *HostConn) {
		if got := len(hc.pendingInputs); got != 0 {
			t.Fatalf("pendingInputs after flush = %d, want 0", got)
		}
		if hc.bufferPendingInputs {
			t.Fatal("bufferPendingInputs = true after successful connect, want false")
		}
	})
}

func TestCloseConns(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	// With nil connections — should not panic
	testInActor(hc, func(hc *HostConn) {
		hc.closeConns()
		if hc.sshClient != nil {
			t.Error("sshClient should be nil after closeConns")
		}
		if hc.amuxConn != nil {
			t.Error("amuxConn should be nil after closeConns")
		}
	})
}

func TestBuildSSHConfigWithIdentityFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_ed25519")
	writeTestKey(t, keyPath)
	t.Setenv("SSH_AUTH_SOCK", "")

	hc := NewHostConn("test", config.Host{
		IdentityFile: keyPath,
		User:         "testuser",
	}, "hash", nil, nil, nil)
	defer hc.Close()

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig() error: %v", err)
	}
	if cfg.User != "testuser" {
		t.Errorf("User = %q, want testuser", cfg.User)
	}
	if len(cfg.Auth) == 0 {
		t.Error("Auth methods should not be empty")
	}
}

func TestBuildSSHConfigDefaultUser(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_ed25519")
	writeTestKey(t, keyPath)
	t.Setenv("SSH_AUTH_SOCK", "")

	hc := NewHostConn("test", config.Host{
		IdentityFile: keyPath,
	}, "hash", nil, nil, nil)
	defer hc.Close()

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig() error: %v", err)
	}
	wantUser := transportssh.DefaultSSHUser()
	if cfg.User != wantUser {
		t.Errorf("User = %q, want %q (default)", cfg.User, wantUser)
	}
}

func TestBuildSSHConfigNoAuth(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	_, err := hc.buildSSHConfig()
	if err == nil {
		t.Error("buildSSHConfig with no auth should return error")
	}
}

func TestParseSpawnOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    uint32
		wantErr bool
	}{
		{name: "standard format", input: "Spawned remote-1 in pane 5\n", want: 5},
		{name: "high pane ID", input: "Spawned remote-42 in pane 123\n", want: 123},
		{name: "no trailing newline", input: "Spawned remote-1 in pane 7", want: 7},
		{name: "no pane keyword", input: "something else\n", wantErr: true},
		{name: "invalid pane id", input: "Spawned remote-1 in pane nope\n", wantErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "pane 0", input: "pane 0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSpawnOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSpawnOutput(%q) = %d, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseSpawnOutput(%q) error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseSpawnOutput(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestFlushPendingInputsRequeuesAndDisconnectsOnWriteFailure(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	serverConn, clientConn := net.Pipe()
	serverConn.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.state = Connected
		hc.amuxConn = clientConn
		hc.amuxWriter = remoteTestWriter(clientConn)
		hc.localToRemote[42] = 100
		hc.pendingInputs = []pendingPaneInput{
			{localPaneID: 42, data: []byte("hello")},
			{localPaneID: 42, data: []byte(" world")},
		}

		hc.flushPendingInputs()

		if got := len(hc.pendingInputs); got != 2 {
			t.Fatalf("pendingInputs after failed flush = %d, want 2", got)
		}
		if hc.state != Reconnecting {
			t.Fatalf("state after failed flush = %v, want %v", hc.state, Reconnecting)
		}
	})
}

func TestSendInputEventRequeuesAndDisconnectsOnWriteFailure(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)
	defer hc.Close()

	serverConn, clientConn := net.Pipe()
	serverConn.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.state = Connected
		hc.amuxConn = clientConn
		hc.amuxWriter = remoteTestWriter(clientConn)
		hc.localToRemote[42] = 100

		sendInputEvent{localPaneID: 42, data: []byte("hello")}.handle(hc)

		if got := len(hc.pendingInputs); got != 1 {
			t.Fatalf("pendingInputs after failed send = %d, want 1", got)
		}
		if string(hc.pendingInputs[0].data) != "hello" {
			t.Fatalf("requeued input = %q, want %q", hc.pendingInputs[0].data, "hello")
		}
		if hc.state != Reconnecting {
			t.Fatalf("state after failed send = %v, want %v", hc.state, Reconnecting)
		}
	})
}

// writeTestKey generates a temporary ed25519 private key file for testing.
func writeTestKey(t *testing.T, path string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBytes), 0600); err != nil {
		t.Fatal(err)
	}
}
