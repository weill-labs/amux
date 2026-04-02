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
