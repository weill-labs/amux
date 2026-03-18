package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/config"
)

func TestBuildEnsureServerCmd(t *testing.T) {
	t.Parallel()

	cmd := buildEnsureServerCmd("/tmp/amux-1000/default", "default@myhost")

	// Must check for socket before starting
	if !strings.Contains(cmd, `[ ! -S /tmp/amux-1000/default ]`) {
		t.Error("command should check socket existence")
	}

	// Must try ~/.local/bin/amux first (deploy location)
	if !strings.Contains(cmd, "~/.local/bin/amux") {
		t.Error("command should try ~/.local/bin/amux first")
	}

	// Must pass session name to _server
	if !strings.Contains(cmd, `_server default@myhost`) {
		t.Error("command should pass session name to _server")
	}

	// Must fall back to amux in PATH
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

func TestRemoteSessionName(t *testing.T) {
	t.Parallel()

	name := remoteSessionName("default")
	hostname, _ := os.Hostname()

	if !strings.HasPrefix(name, "default@") {
		t.Errorf("remoteSessionName should start with session name, got %q", name)
	}
	if !strings.HasSuffix(name, "@"+hostname) {
		t.Errorf("remoteSessionName should end with @hostname, got %q", name)
	}
}

func TestRemoteSocketPath(t *testing.T) {
	t.Parallel()

	hc := &HostConn{remoteUID: "1000"}
	path := hc.remoteSocketPath("default@myhost")

	if path != "/tmp/amux-1000/default@myhost" {
		t.Errorf("remoteSocketPath = %q, want /tmp/amux-1000/default@myhost", path)
	}
}

func TestNewHostConn(t *testing.T) {
	t.Parallel()

	cfg := config.Host{Type: "remote", Address: "10.0.0.1", User: "ubuntu"}
	called := false
	hc := NewHostConn("test-host", cfg, "abc1234",
		func(uint32, []byte) {},
		func(uint32) {},
		func(string, ConnState) { called = true },
	)

	if hc.name != "test-host" {
		t.Errorf("name = %q, want test-host", hc.name)
	}
	if hc.buildHash != "abc1234" {
		t.Errorf("buildHash = %q, want abc1234", hc.buildHash)
	}
	if hc.State() != Disconnected {
		t.Errorf("initial state = %q, want disconnected", hc.State())
	}

	// setState should trigger callback
	hc.setState(Connecting)
	if !called {
		t.Error("onStateChange callback not called")
	}
	if hc.state != Connecting {
		t.Errorf("state after setState = %q, want connecting", hc.state)
	}
}

func TestHostConnStateTransitions(t *testing.T) {
	t.Parallel()

	var transitions []ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) { transitions = append(transitions, s) },
	)

	hc.setState(Connecting)
	hc.setState(Connected)
	hc.setState(Reconnecting)
	hc.setState(Disconnected)

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

func TestRemovePane(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)

	// Manually register pane mappings (RegisterPane is on another branch)
	hc.mu.Lock()
	hc.localToRemote[10] = 100
	hc.remoteToLocal[100] = 10
	hc.localToRemote[20] = 200
	hc.remoteToLocal[200] = 20
	hc.mu.Unlock()

	// Remove one mapping
	hc.RemovePane(10)

	hc.mu.Lock()
	if _, ok := hc.localToRemote[10]; ok {
		t.Error("localToRemote[10] should be deleted after RemovePane")
	}
	if _, ok := hc.remoteToLocal[100]; ok {
		t.Error("remoteToLocal[100] should be deleted after RemovePane")
	}
	// Other mapping should survive
	if hc.localToRemote[20] != 200 {
		t.Errorf("localToRemote[20] = %d, want 200 (should survive)", hc.localToRemote[20])
	}
	hc.mu.Unlock()

	// Removing unknown pane should be a no-op
	hc.RemovePane(999)
}

func TestDisconnect(t *testing.T) {
	t.Parallel()

	var lastState ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) { lastState = s },
	)

	// Simulate connected state (no real SSH)
	hc.mu.Lock()
	hc.state = Connected
	hc.mu.Unlock()

	hc.Disconnect()

	if lastState != Disconnected {
		t.Errorf("state after Disconnect = %q, want disconnected", lastState)
	}
}

func TestHandleDisconnect(t *testing.T) {
	t.Parallel()

	var lastState ConnState
	hc := NewHostConn("test", config.Host{}, "hash",
		nil, nil,
		func(_ string, s ConnState) { lastState = s },
	)

	// Not connected — should be a no-op
	hc.handleDisconnect()
	if lastState == Reconnecting {
		t.Error("handleDisconnect on non-connected should not transition to Reconnecting")
	}

	// Simulate Connected state
	hc.mu.Lock()
	hc.state = Connected
	hc.mu.Unlock()

	hc.handleDisconnect()

	// Should transition to Reconnecting
	if lastState != Reconnecting {
		t.Errorf("state after handleDisconnect = %q, want reconnecting", lastState)
	}

	// Calling again should be a no-op (already reconnecting)
	lastState = ""
	hc.handleDisconnect()
	if lastState != "" {
		t.Error("duplicate handleDisconnect should be a no-op")
	}
}

func TestSendInputDisconnected(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)

	// No pane mapping, no connection — should silently return nil
	err := hc.SendInput(42, []byte("hello"))
	if err != nil {
		t.Errorf("SendInput on disconnected = %v, want nil", err)
	}
}

func TestSendResizeDisconnected(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)

	// No pane mapping — should silently return nil
	err := hc.SendResize(42, 80, 24)
	if err != nil {
		t.Errorf("SendResize on disconnected = %v, want nil", err)
	}
}

func TestCloseConnsLocked(t *testing.T) {
	t.Parallel()

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)

	// With nil connections — should not panic
	hc.mu.Lock()
	hc.closeConnsLocked()
	hc.mu.Unlock()

	if hc.sshClient != nil {
		t.Error("sshClient should be nil after closeConnsLocked")
	}
	if hc.amuxConn != nil {
		t.Error("amuxConn should be nil after closeConnsLocked")
	}
}

func TestBuildSSHConfigWithIdentityFile(t *testing.T) {
	// Cannot use t.Parallel — uses t.Setenv to clear SSH_AUTH_SOCK.

	// Generate a temp ed25519 key
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_ed25519")
	writeTestKey(t, keyPath)

	// Clear SSH agent so only the key file provides auth
	t.Setenv("SSH_AUTH_SOCK", "")

	hc := NewHostConn("test", config.Host{
		IdentityFile: keyPath,
		User:         "testuser",
	}, "hash", nil, nil, nil)

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
	// Cannot use t.Parallel — uses t.Setenv.

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_ed25519")
	writeTestKey(t, keyPath)
	t.Setenv("SSH_AUTH_SOCK", "")

	hc := NewHostConn("test", config.Host{
		IdentityFile: keyPath,
	}, "hash", nil, nil, nil)

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig() error: %v", err)
	}
	if cfg.User != "ubuntu" {
		t.Errorf("User = %q, want ubuntu (default)", cfg.User)
	}
}

func TestBuildSSHConfigNoAuth(t *testing.T) {
	// Cannot use t.Parallel — uses t.Setenv.

	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir()) // no key files in this temp home

	hc := NewHostConn("test", config.Host{}, "hash", nil, nil, nil)

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
