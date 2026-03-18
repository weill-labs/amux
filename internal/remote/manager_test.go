package remote

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1"},
	}}

	m := NewManager(cfg, "abc1234")

	if m.Config() != cfg {
		t.Error("Config() should return the original config")
	}
	if m.buildHash != "abc1234" {
		t.Errorf("buildHash = %q, want abc1234", m.buildHash)
	}
}

func TestManagerHostStatus(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev":   {Type: "remote", Address: "10.0.0.1"},
		"local": {Type: "local"},
	}}
	m := NewManager(cfg, "hash")

	// Unknown host returns Disconnected
	if s := m.HostStatus("dev"); s != Disconnected {
		t.Errorf("HostStatus(dev) = %q, want disconnected", s)
	}

	// Simulate a connected host
	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc.state = Connected
	m.mu.Lock()
	m.hosts["dev"] = hc
	m.mu.Unlock()

	if s := m.HostStatus("dev"); s != Connected {
		t.Errorf("HostStatus(dev) = %q, want connected", s)
	}
}

func TestManagerAllHostStatus(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev":   {Type: "remote", Address: "10.0.0.1"},
		"prod":  {Type: "remote", Address: "10.0.0.2"},
		"local": {Type: "local"},
	}}
	m := NewManager(cfg, "hash")

	// Simulate dev as connected
	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc.state = Connected
	m.mu.Lock()
	m.hosts["dev"] = hc
	m.mu.Unlock()

	status := m.AllHostStatus()

	// Should not include local hosts
	if _, ok := status["local"]; ok {
		t.Error("AllHostStatus should not include local hosts")
	}
	if status["dev"] != Connected {
		t.Errorf("status[dev] = %q, want connected", status["dev"])
	}
	if status["prod"] != Disconnected {
		t.Errorf("status[prod] = %q, want disconnected", status["prod"])
	}
}

func TestManagerConnStatusForPane(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1"},
	}}
	m := NewManager(cfg, "hash")

	// Unknown pane returns empty string
	if s := m.ConnStatusForPane(42); s != "" {
		t.Errorf("ConnStatusForPane(42) = %q, want empty", s)
	}

	// Register a host connection and pane mapping
	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc.state = Connected
	m.mu.Lock()
	m.hosts["dev"] = hc
	m.localToHost[42] = "dev"
	m.mu.Unlock()

	if s := m.ConnStatusForPane(42); s != "connected" {
		t.Errorf("ConnStatusForPane(42) = %q, want connected", s)
	}
}

func TestManagerRemovePane(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1"},
	}}
	m := NewManager(cfg, "hash")

	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc.mu.Lock()
	hc.localToRemote[42] = 100
	hc.remoteToLocal[100] = 42
	hc.mu.Unlock()

	m.mu.Lock()
	m.hosts["dev"] = hc
	m.localToHost[42] = "dev"
	m.mu.Unlock()

	m.RemovePane(42)

	m.mu.Lock()
	if _, ok := m.localToHost[42]; ok {
		t.Error("localToHost[42] should be deleted")
	}
	m.mu.Unlock()

	hc.mu.Lock()
	if _, ok := hc.localToRemote[42]; ok {
		t.Error("HostConn localToRemote[42] should be deleted")
	}
	hc.mu.Unlock()

	// Removing again should be a no-op
	m.RemovePane(42)
}

func TestManagerCreatePaneErrors(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev":   {Type: "remote", Address: "10.0.0.1"},
		"local": {Type: "local"},
	}}
	m := NewManager(cfg, "hash")
	m.SetCallbacks(nil, nil, nil)

	// Unknown host
	_, err := m.CreatePane("unknown", 1, "default")
	if err == nil {
		t.Error("CreatePane with unknown host should error")
	}

	// Local host
	_, err = m.CreatePane("local", 1, "default")
	if err == nil {
		t.Error("CreatePane with local host should error")
	}
}

func TestManagerShutdown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev":  {Type: "remote", Address: "10.0.0.1"},
		"prod": {Type: "remote", Address: "10.0.0.2"},
	}}
	m := NewManager(cfg, "hash")

	hc1 := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc1.state = Connected
	hc2 := NewHostConn("prod", cfg.Hosts["prod"], "hash", nil, nil, nil)
	hc2.state = Connected

	m.mu.Lock()
	m.hosts["dev"] = hc1
	m.hosts["prod"] = hc2
	m.mu.Unlock()

	m.Shutdown()

	if hc1.State() != Disconnected {
		t.Errorf("dev state after Shutdown = %q, want disconnected", hc1.State())
	}
	if hc2.State() != Disconnected {
		t.Errorf("prod state after Shutdown = %q, want disconnected", hc2.State())
	}
}

func TestManagerSendInputUnknownPane(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{Hosts: map[string]config.Host{}}, "hash")

	err := m.SendInput(999, []byte("hello"))
	if err == nil {
		t.Error("SendInput for unknown pane should error")
	}
}

func TestManagerSendResizeUnknownPane(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{Hosts: map[string]config.Host{}}, "hash")

	// Unknown pane — should return nil (silently ignored)
	err := m.SendResize(999, 80, 24)
	if err != nil {
		t.Errorf("SendResize for unknown pane = %v, want nil", err)
	}
}

func TestManagerDisconnectAndReconnectHost(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1"},
	}}
	m := NewManager(cfg, "hash")

	// Disconnect unknown host
	if err := m.DisconnectHost("unknown"); err == nil {
		t.Error("DisconnectHost unknown should error")
	}

	// ReconnectHost unknown host
	if err := m.ReconnectHost("unknown", "default"); err == nil {
		t.Error("ReconnectHost unknown should error")
	}

	// Disconnect a known host
	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	hc.state = Connected
	m.mu.Lock()
	m.hosts["dev"] = hc
	m.mu.Unlock()

	if err := m.DisconnectHost("dev"); err != nil {
		t.Errorf("DisconnectHost(dev) = %v, want nil", err)
	}
	if hc.State() != Disconnected {
		t.Errorf("state after DisconnectHost = %q, want disconnected", hc.State())
	}
}

func TestDeployToAddressEmptyBuildHash(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{}}
	m := NewManager(cfg, "")

	// Empty build hash — should return immediately without SSH dial
	m.DeployToAddress("host", "10.0.0.1:22", "ubuntu")
	// No panic or error = pass
}

func TestDeployToAddressDeployDisabled(t *testing.T) {
	t.Parallel()

	f := false
	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1", Deploy: &f},
	}}
	m := NewManager(cfg, "abc1234")

	// deploy=false in config — should skip without SSH dial
	m.DeployToAddress("dev", "10.0.0.1:22", "ubuntu")
}

func TestDeployToAddressViaSSH(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	cfg := &config.Config{Hosts: map[string]config.Host{
		"test-host": {
			Type:         "remote",
			Address:      ts.Addr,
			User:         "testuser",
			IdentityFile: ts.KeyFile,
		},
	}}
	m := NewManager(cfg, "deployhash")

	// Should SSH to the test server and deploy
	m.DeployToAddress("test-host", ts.Addr, "testuser")

	// Verify binary was uploaded
	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(uploaded); err != nil {
		t.Errorf("expected binary at %s after DeployToAddress: %v", uploaded, err)
	}
}

func TestDeployToAddressHostNotInConfig(t *testing.T) {
	// Cannot use t.Parallel — t.Setenv modifies process env.
	ts := startTestSSH(t)

	// Host "unknown-host" is NOT in the config map — DeployToAddress
	// falls back to constructing a Host from the raw address and user.
	cfg := &config.Config{Hosts: map[string]config.Host{}}
	m := NewManager(cfg, "deployhash")

	// Host not in config → DeployToAddress builds a Host with no IdentityFile,
	// so buildSSHConfig tries default keys + agent. Place the test key at
	// the default SSH key path so it's discovered.
	fakeHome := t.TempDir()
	sshDir := filepath.Join(fakeHome, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("creating .ssh dir: %v", err)
	}

	// Copy the test key to the default location buildSSHConfig checks
	keyData, err := os.ReadFile(ts.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), keyData, 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("SSH_AUTH_SOCK", "") // disable agent so only the key file is used

	m.DeployToAddress("unknown-host", ts.Addr, "testuser")

	// Verify binary was uploaded via the fallback config path
	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(uploaded); err != nil {
		t.Errorf("expected binary at %s after DeployToAddress (host not in config): %v", uploaded, err)
	}
}

func TestDeployToAddressBuildSSHConfigError(t *testing.T) {
	// Cannot use t.Parallel — t.Setenv modifies process env.

	// Point HOME to an empty dir (no default SSH keys) and disable the agent.
	// buildSSHConfig should fail with "no SSH auth methods available".
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")

	cfg := &config.Config{Hosts: map[string]config.Host{
		"noauth": {Type: "remote", Address: "127.0.0.1:22"},
	}}
	m := NewManager(cfg, "somehash")

	// Should hit the buildSSHConfig error path and return without panic.
	m.DeployToAddress("noauth", "127.0.0.1:22", "testuser")
}
