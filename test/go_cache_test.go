package test

import (
	"path/filepath"
	"testing"
)

func TestGoBuildEnvUsesIsolatedCache(t *testing.T) {
	t.Parallel()

	binPath := filepath.Join(t.TempDir(), "bin", "amux")
	env := goBuildEnv([]string{"PATH=/bin", "GOCACHE=/shared"}, binPath)
	want := childGoCachePath(filepath.Dir(binPath))

	if got := issueMetaEnvValue(env, "GOCACHE"); got != want {
		t.Fatalf("GOCACHE = %q, want %q", got, want)
	}
}

func TestIssueMetaScriptEnvUsesPerTestGoCache(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	env := issueMetaScriptEnv(tempDir)
	want := childGoCachePath(tempDir)

	if got := issueMetaEnvValue(env, "GOCACHE"); got != want {
		t.Fatalf("GOCACHE = %q, want %q", got, want)
	}
}
