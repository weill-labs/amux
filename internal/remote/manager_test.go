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

	if s := m.HostStatus("dev"); s != Disconnected {
		t.Errorf("HostStatus(dev) = %q, want disconnected", s)
	}

	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	defer hc.Close()
	testInActor(hc, func(hc *HostConn) { hc.state = Connected })
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

	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	defer hc.Close()
	testInActor(hc, func(hc *HostConn) { hc.state = Connected })
	m.mu.Lock()
	m.hosts["dev"] = hc
	m.mu.Unlock()

	status := m.AllHostStatus()

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

	if s := m.ConnStatusForPane(42); s != "" {
		t.Errorf("ConnStatusForPane(42) = %q, want empty", s)
	}

	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	defer hc.Close()
	testInActor(hc, func(hc *HostConn) { hc.state = Connected })
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
	defer hc.Close()
	hc.RegisterPane(42, 100)

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

	testInActor(hc, func(hc *HostConn) {
		if _, ok := hc.localToRemote[42]; ok {
			t.Error("HostConn localToRemote[42] should be deleted")
		}
	})

	m.RemovePane(42) // no-op
}

func TestManagerCreatePaneErrors(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev":   {Type: "remote", Address: "10.0.0.1"},
		"local": {Type: "local"},
	}}
	m := NewManager(cfg, "hash")
	m.SetCallbacks(nil, nil, nil)

	_, err := m.CreatePane("unknown", 1, "default")
	if err == nil {
		t.Error("CreatePane with unknown host should error")
	}

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
	hc2 := NewHostConn("prod", cfg.Hosts["prod"], "hash", nil, nil, nil)
	testInActor(hc1, func(hc *HostConn) { hc.state = Connected })
	testInActor(hc2, func(hc *HostConn) { hc.state = Connected })

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

	if err := m.DisconnectHost("unknown"); err == nil {
		t.Error("DisconnectHost unknown should error")
	}
	if err := m.ReconnectHost("unknown", "default"); err == nil {
		t.Error("ReconnectHost unknown should error")
	}

	hc := NewHostConn("dev", cfg.Hosts["dev"], "hash", nil, nil, nil)
	defer hc.Close()
	testInActor(hc, func(hc *HostConn) { hc.state = Connected })
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
	m.DeployToAddress("host", "10.0.0.1:22", "ubuntu")
}

func TestDeployToAddressDeployDisabled(t *testing.T) {
	t.Parallel()

	f := false
	cfg := &config.Config{Hosts: map[string]config.Host{
		"dev": {Type: "remote", Address: "10.0.0.1", Deploy: &f},
	}}
	m := NewManager(cfg, "abc1234")
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
	m.DeployToAddress("test-host", ts.Addr, "testuser")

	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(uploaded); err != nil {
		t.Errorf("expected binary at %s after DeployToAddress: %v", uploaded, err)
	}
}

func TestDeployToAddressHostNotInConfig(t *testing.T) {
	ts := startTestSSH(t)

	cfg := &config.Config{Hosts: map[string]config.Host{}}
	m := NewManager(cfg, "deployhash")

	fakeHome := t.TempDir()
	sshDir := filepath.Join(fakeHome, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("creating .ssh dir: %v", err)
	}

	keyData, err := os.ReadFile(ts.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), keyData, 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("SSH_AUTH_SOCK", "")

	m.DeployToAddress("unknown-host", ts.Addr, "testuser")

	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(uploaded); err != nil {
		t.Errorf("expected binary at %s after DeployToAddress (host not in config): %v", uploaded, err)
	}
}

func TestDeployToAddressBuildSSHConfigError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")

	cfg := &config.Config{Hosts: map[string]config.Host{
		"noauth": {Type: "remote", Address: "127.0.0.1:22"},
	}}
	m := NewManager(cfg, "somehash")
	m.DeployToAddress("noauth", "127.0.0.1:22", "testuser")
}

func TestFindHostByAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hosts     map[string]config.Host
		sshAddr   string
		wantName  string
		wantFound bool
	}{
		{
			name:      "match by address",
			hosts:     map[string]config.Host{"gpu-box": {Type: "remote", Address: "10.0.0.5"}},
			sshAddr:   "10.0.0.5:22",
			wantName:  "gpu-box",
			wantFound: true,
		},
		{
			name:      "match by name fallback",
			hosts:     map[string]config.Host{"10.0.0.5": {Type: "remote"}},
			sshAddr:   "10.0.0.5:22",
			wantName:  "10.0.0.5",
			wantFound: true,
		},
		{
			name:      "no match",
			hosts:     map[string]config.Host{"gpu-box": {Type: "remote", Address: "10.0.0.5"}},
			sshAddr:   "10.0.0.99:22",
			wantName:  "",
			wantFound: false,
		},
		{
			name:      "skip local hosts",
			hosts:     map[string]config.Host{"local-dev": {Type: "local", Address: "10.0.0.5"}},
			sshAddr:   "10.0.0.5:22",
			wantName:  "",
			wantFound: false,
		},
		{
			name:      "normalize port on match",
			hosts:     map[string]config.Host{"gpu-box": {Type: "remote", Address: "10.0.0.5:22"}},
			sshAddr:   "10.0.0.5",
			wantName:  "gpu-box",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{Hosts: tt.hosts}
			m := NewManager(cfg, "hash")
			gotName, _, gotFound := m.findHostByAddress(tt.sshAddr)
			if gotFound != tt.wantFound {
				t.Errorf("found = %v, want %v", gotFound, tt.wantFound)
			}
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
		})
	}
}
