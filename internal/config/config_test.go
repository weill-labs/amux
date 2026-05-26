package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestLoadMissing(t *testing.T) {
	t.Parallel()
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config")
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

func TestLoadClientLocalEcho(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[client]
local_echo = "always"
local_echo_style = "underline"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.EffectiveLocalEchoMode(); got != "always" {
		t.Fatalf("EffectiveLocalEchoMode() = %q, want always", got)
	}
	if got := cfg.EffectiveLocalEchoStyle(); got != "underline" {
		t.Fatalf("EffectiveLocalEchoStyle() = %q, want underline", got)
	}
}

func TestLoadThemeStatusStyle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[theme]
status_style = "powerline"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.EffectiveStatusStyle(); got != "powerline" {
		t.Fatalf("EffectiveStatusStyle() = %q, want powerline", got)
	}
}

func TestEffectiveStatusStyleDefaultsToCompact(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	if got := cfg.EffectiveStatusStyle(); got != "compact" {
		t.Fatalf("EffectiveStatusStyle() = %q, want compact", got)
	}

	var nilCfg *Config
	if got := nilCfg.EffectiveStatusStyle(); got != "compact" {
		t.Fatalf("nil EffectiveStatusStyle() = %q, want compact", got)
	}
}

func TestLoadRejectsInvalidThemeStatusStyle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[theme]
status_style = "fancy"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `status_style must be one of "compact", "plain", or "powerline"`) {
		t.Fatalf("Load() error = %v, want invalid status_style message", err)
	}
}

func TestLoadThemeIcons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "empty config defaults to unicode",
			content: "",
			want:    ThemeIconsUnicode,
		},
		{
			name: "ascii",
			content: `
[theme]
icons = "ascii"
`,
			want: ThemeIconsASCII,
		},
		{
			name: "unicode",
			content: `
[theme]
icons = "unicode"
`,
			want: ThemeIconsUnicode,
		},
		{
			name: "nerd",
			content: `
[theme]
icons = "nerd"
`,
			want: ThemeIconsNerd,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.EffectiveThemeIcons(); got != tt.want {
				t.Fatalf("EffectiveThemeIcons() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidThemeIcons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name: "unknown value",
			content: `
[theme]
icons = "powerline"
`,
		},
		{
			name: "empty string",
			content: `
[theme]
icons = ""
`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), `theme.icons must be one of "ascii", "unicode", or "nerd"`) {
				t.Fatalf("Load() error = %v, want theme.icons validation error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidClientLocalEcho(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "invalid mode",
			content: `
[client]
local_echo = "maybe"
`,
			want: "local_echo must be one of",
		},
		{
			name: "invalid style",
			content: `
[client]
local_echo_style = "flashy"
`,
			want: "local_echo_style must be one of",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want substring %q", err, tt.want)
			}
		})
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
