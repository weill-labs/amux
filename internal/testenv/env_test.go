package testenv

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHermeticAmuxEnvScrubsAmuxAndTerminalVars(t *testing.T) {
	for _, key := range blockedEnvKeys {
		t.Setenv(key, "leaked")
	}
	t.Setenv("PATH", "/bin")

	env := HermeticAmuxEnv()

	for _, key := range blockedEnvKeys {
		if key == "TERM" {
			continue
		}
		if value, ok := lookupEnvEntry(env, key); ok {
			t.Fatalf("%s leaked into hermetic env as %q", key, value)
		}
	}
	if value, ok := lookupEnvEntry(env, "PATH"); !ok || value != "/bin" {
		t.Fatalf("PATH = %q, %t; want /bin, true", value, ok)
	}
	if value, ok := lookupEnvEntry(env, "TERM"); !ok || value != "xterm-256color" {
		t.Fatalf("TERM = %q, %t; want xterm-256color, true", value, ok)
	}
}

func TestHermeticMainEnvAddsHelper(t *testing.T) {
	t.Setenv("AMUX_MAIN_HELPER", "leaked")

	env := HermeticMainEnv()

	if value, ok := lookupEnvEntry(env, "AMUX_MAIN_HELPER"); !ok || value != "1" {
		t.Fatalf("AMUX_MAIN_HELPER = %q, %t; want 1, true", value, ok)
	}
}

func TestNewCommandUsesHermeticEnv(t *testing.T) {
	t.Setenv("AMUX_SESSION", "main")

	cmd := NewCommand("amux", "list")

	if filepath.Base(cmd.Path) != "amux" {
		t.Fatalf("Path = %q, want basename amux", cmd.Path)
	}
	if !slices.Equal(cmd.Args, []string{"amux", "list"}) {
		t.Fatalf("Args = %v, want [amux list]", cmd.Args)
	}
	if value, ok := lookupEnvEntry(cmd.Env, "AMUX_SESSION"); ok {
		t.Fatalf("AMUX_SESSION leaked into command env as %q", value)
	}
}

func TestRemoveEnvKeyDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	input := []string{"A=1", "DROP=old", "B=2"}

	got := RemoveEnvKey(input, "DROP")

	if !slices.Equal(got, []string{"A=1", "B=2"}) {
		t.Fatalf("RemoveEnvKey() = %v, want [A=1 B=2]", got)
	}
	if !slices.Equal(input, []string{"A=1", "DROP=old", "B=2"}) {
		t.Fatalf("input mutated to %v", input)
	}
}

func lookupEnvEntry(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}
