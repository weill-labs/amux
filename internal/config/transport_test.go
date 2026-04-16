package config

import "testing"

func TestHostTransportDefaultsToSSH(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	if got := cfg.HostTransport("builder"); got != "ssh" {
		t.Fatalf("HostTransport(%q) = %q, want ssh", "builder", got)
	}
}

func TestHostTransportUsesConfiguredValue(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Hosts: map[string]Host{
			"builder": {Transport: "ssh"},
		},
	}
	if got := cfg.HostTransport("builder"); got != "ssh" {
		t.Fatalf("HostTransport(%q) = %q, want ssh", "builder", got)
	}
}
