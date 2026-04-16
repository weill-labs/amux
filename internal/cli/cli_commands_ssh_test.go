package cli

import (
	"os"
	"testing"

	"github.com/weill-labs/amux/internal/sshutil"
)

func TestResolveCLISSHTargetKeepsExplicitUser(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte(`
[hosts.builder]
user = "deploy"
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	target, err := resolveCLISSHTarget("alice@builder:work")
	if err != nil {
		t.Fatalf("resolveCLISSHTarget() error = %v", err)
	}
	if target.User != "alice" {
		t.Fatalf("resolveCLISSHTarget() user = %q, want alice", target.User)
	}
	if target.Host != "builder" || target.Session != "work" {
		t.Fatalf("resolveCLISSHTarget() = %#v, want builder/work target", target)
	}
}

func TestResolveCLISSHTargetReturnsConfigError(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte("["), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	if _, err := resolveCLISSHTarget("builder"); err == nil {
		t.Fatal("resolveCLISSHTarget() error = nil, want config parse failure")
	}
}

func TestResolveCLISSHTargetUsesDefaultSSHUser(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	target, err := resolveCLISSHTarget("builder:work")
	if err != nil {
		t.Fatalf("resolveCLISSHTarget() error = %v", err)
	}
	wantUser := sshutil.DefaultSSHUser()
	if target.User != wantUser {
		t.Fatalf("resolveCLISSHTarget() user = %q, want %q", target.User, wantUser)
	}
	if target.Host != "builder" || target.Session != "work" {
		t.Fatalf("resolveCLISSHTarget() = %#v, want builder/work target", target)
	}
}
