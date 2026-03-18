package remote

import (
	"strings"
	"testing"
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
