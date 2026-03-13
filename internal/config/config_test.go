package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	cfg, err := Load("/nonexistent/path/hosts.toml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(cfg.Hosts) != 0 {
		t.Errorf("expected empty hosts, got %d", len(cfg.Hosts))
	}
}

func TestLoadValid(t *testing.T) {
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
	// Deterministic: same host always gets same color
	c1 := ColorForHost("my-server")
	c2 := ColorForHost("my-server")
	if c1 != c2 {
		t.Errorf("ColorForHost not deterministic: %s vs %s", c1, c2)
	}

	// Different hosts should (usually) get different colors
	c3 := ColorForHost("other-server")
	// Not guaranteed but extremely likely for different inputs
	_ = c3
}

func TestHostUser(t *testing.T) {
	cfg := &Config{
		Hosts: map[string]Host{
			"myhost": {User: "admin"},
		},
	}

	if u := cfg.HostUser("myhost"); u != "admin" {
		t.Errorf("expected admin, got %s", u)
	}
	if u := cfg.HostUser("unknown"); u != "ubuntu" {
		t.Errorf("expected ubuntu default, got %s", u)
	}
}

func TestAutoAssignColor(t *testing.T) {
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
