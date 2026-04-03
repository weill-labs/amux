package test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hermeticMainEnv() []string {
	env := append([]string{}, os.Environ()...)
	for _, key := range []string{
		"AMUX_MAIN_HELPER",
		"AMUX_PANE",
		"AMUX_SESSION",
		"TMUX",
		"SSH_CONNECTION",
		"SSH_CLIENT",
		"SSH_TTY",
		"TERM",
	} {
		env = removeEnvKey(env, key)
	}
	return append(env,
		"AMUX_MAIN_HELPER=1",
		"TERM=xterm-256color",
	)
}

func removeEnvKey(env []string, key string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func repoRoot(tb testing.TB) string {
	tb.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("resolve helper file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func repoPath(tb testing.TB, rel string) string {
	tb.Helper()

	return filepath.Join(repoRoot(tb), filepath.FromSlash(rel))
}
