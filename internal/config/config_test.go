package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	t.Parallel()
	cfg, err := Load("/nonexistent/path/hosts.toml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(cfg.Hosts) != 0 {
		t.Errorf("expected empty hosts, got %d", len(cfg.Hosts))
	}
}

func TestLoadValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.toml")
	content := `
[hosts.lambda-a100]
type = "remote"
user = "ubuntu"
address = "150.136.64.231"
project_dir = "~/Project"
gpu = "A100"
color = "f38ba8"

[hosts.macbook]
type = "local"
color = "a6e3a1"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(cfg.Hosts))
	}

	h := cfg.Hosts["lambda-a100"]
	if h.Type != "remote" {
		t.Errorf("expected type remote, got %s", h.Type)
	}
	if h.User != "ubuntu" {
		t.Errorf("expected user ubuntu, got %s", h.User)
	}
	if h.Color != "f38ba8" {
		t.Errorf("expected color f38ba8, got %s", h.Color)
	}
}

func TestColorForHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		hostA    string
		hostB    string
		wantSame bool
	}{
		{"same host is deterministic", "my-server", "my-server", true},
		{"different hosts differ", "my-server", "other-server", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := ColorForHost(tt.hostA)
			b := ColorForHost(tt.hostB)
			if (a == b) != tt.wantSame {
				t.Errorf("ColorForHost(%q)=%s, ColorForHost(%q)=%s, wantSame=%v",
					tt.hostA, a, tt.hostB, b, tt.wantSame)
			}
		})
	}
}

func TestHostUser(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Hosts: map[string]Host{
			"myhost": {User: "admin"},
		},
	}

	tests := []struct {
		host string
		want string
	}{
		{"myhost", "admin"},
		{"unknown", "ubuntu"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			if got := cfg.HostUser(tt.host); got != tt.want {
				t.Errorf("HostUser(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestAutoAssignColor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.toml")
	content := `
[hosts.no-color-host]
type = "local"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	h := cfg.Hosts["no-color-host"]
	if h.Color == "" {
		t.Error("expected auto-assigned color, got empty")
	}
}
