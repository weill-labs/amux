package config

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
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
	defaultUser := defaultHostUser()

	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "configured user wins", host: "myhost", want: "admin"},
		{name: "missing host falls back to shared default user", host: "unknown", want: defaultUser},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cfg.HostUser(tt.host); got != tt.want {
				t.Errorf("HostUser(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestDefaultHostUserResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		envUser     string
		currentUser func() (*user.User, error)
		want        string
	}{
		{
			name:    "uses current user when lookup succeeds",
			envUser: "",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: "alice"}, nil
			},
			want: "alice",
		},
		{
			name:    "falls back to USER when lookup fails",
			envUser: "builder",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "builder",
		},
		{
			name:    "returns empty when lookup fails and USER is unset",
			envUser: "",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := defaultHostUserWith(tt.currentUser, func(key string) string {
				if key != "USER" {
					t.Fatalf("lookup env key = %q, want USER", key)
				}
				return tt.envUser
			}, func(error) {})
			if got != tt.want {
				t.Fatalf("defaultHostUserWith() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHostAddress(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Hosts: map[string]Host{
			"myhost": {Address: "10.0.0.5"},
		},
	}

	tests := []struct {
		host string
		want string
	}{
		{"myhost", "10.0.0.5"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			if got := cfg.HostAddress(tt.host); got != tt.want {
				t.Errorf("HostAddress(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestHostColor(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Hosts: map[string]Host{
			"myhost": {Color: "f38ba8"},
		},
	}

	if got := cfg.HostColor("myhost"); got != "f38ba8" {
		t.Fatalf("HostColor(%q) = %q, want %q", "myhost", got, "f38ba8")
	}

	want := ColorForHost("unknown")
	if got := cfg.HostColor("unknown"); got != want {
		t.Fatalf("HostColor(%q) = %q, want %q", "unknown", got, want)
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

func TestLoadScrollbackLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("scrollback_lines = 2048\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ScrollbackLines == nil || *cfg.ScrollbackLines != 2048 {
		t.Fatalf("ScrollbackLines = %v, want 2048", cfg.ScrollbackLines)
	}
	if got := cfg.EffectiveScrollbackLines(); got != 2048 {
		t.Fatalf("EffectiveScrollbackLines() = %d, want 2048", got)
	}
}

func TestLoadDebugPprof(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[debug]\npprof = true\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.PprofEnabled() {
		t.Fatal("PprofEnabled() = false, want true")
	}
}

func TestLoadRejectsZeroScrollbackLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "zero scrollback",
			content: "scrollback_lines = 0\n",
			wantErr: "scrollback_lines must be >= 1",
		},
		{
			name: "keys section",
			content: `
[keys]
prefix = "C-b"
`,
			wantErr: `unsupported config section "keys"`,
		},
		{
			name: "keys bind subsection",
			content: `
[keys.bind]
s = "split"
`,
			wantErr: `unsupported config section "keys"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load should reject invalid config")
			}
			if got, want := err.Error(), tt.wantErr; got != want {
				t.Fatalf("Load() error = %q, want %q", got, want)
			}
		})
	}
}

func TestEffectiveScrollbackLinesDefaultsWhenUnset(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	if got := cfg.EffectiveScrollbackLines(); got != proto.DefaultScrollbackLines {
		t.Fatalf("EffectiveScrollbackLines() = %d, want %d", got, proto.DefaultScrollbackLines)
	}
}
