package mux

import (
	"strings"
	"testing"
)

func TestPaneCommandEnvOverridesInheritedAmuxVars(t *testing.T) {
	t.Parallel()

	env := paneCommandEnv([]string{
		"PATH=/usr/bin:/bin",
		"TERM=dumb",
		"AMUX_PANE=34",
		"AMUX_SESSION=outer",
	}, 7, "inner")

	want := map[string]string{
		"PATH":         "/usr/bin:/bin",
		"TERM":         "amux",
		"AMUX_PANE":    "7",
		"AMUX_SESSION": "inner",
	}

	for key, value := range want {
		prefix := key + "="
		count := 0
		for _, entry := range env {
			if strings.HasPrefix(entry, prefix) {
				count++
				if got := strings.TrimPrefix(entry, prefix); got != value {
					t.Fatalf("%s = %q, want %q", key, got, value)
				}
			}
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1 in %v", key, count, env)
		}
	}
}

func TestPaneCommandEnvStripsOuterTerminalVars(t *testing.T) {
	t.Parallel()

	env := paneCommandEnv([]string{
		"PATH=/usr/bin:/bin",
		"TERM=xterm-ghostty",
		"TERM_PROGRAM=ghostty",
		"TERM_PROGRAM_VERSION=1.5.0",
	}, 1, "default")

	stripped := []string{"TERM_PROGRAM", "TERM_PROGRAM_VERSION"}
	for _, key := range stripped {
		prefix := key + "="
		for _, entry := range env {
			if strings.HasPrefix(entry, prefix) {
				t.Fatalf("%s should be stripped but found: %s", key, entry)
			}
		}
	}
}

func TestPaneExecCommandSetsPWDForWorkingDir(t *testing.T) {
	t.Parallel()

	cmd := paneExecCommand("/bin/sh", 7, "inner", "/tmp/amux-pane-dir", "")

	if cmd.Dir != "/tmp/amux-pane-dir" {
		t.Fatalf("cmd.Dir = %q, want /tmp/amux-pane-dir", cmd.Dir)
	}
	assertEnvValue(t, cmd.Env, "PWD", "/tmp/amux-pane-dir")
}

func TestEnvWithValueReplacesDuplicateEntries(t *testing.T) {
	t.Parallel()

	env := envWithValue([]string{
		"PATH=/bin",
		"PWD=/old",
		"PWD=/older",
	}, "PWD", "/new")

	assertEnvValue(t, env, "PWD", "/new")
}

func assertEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()

	prefix := key + "="
	count := 0
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		count++
		if got := strings.TrimPrefix(entry, prefix); got != want {
			t.Fatalf("%s = %q, want %q in %v", key, got, want, env)
		}
	}
	if count != 1 {
		t.Fatalf("%s count = %d, want 1 in %v", key, count, env)
	}
}
