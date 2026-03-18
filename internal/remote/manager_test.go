package remote

import (
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
