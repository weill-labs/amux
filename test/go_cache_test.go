package test

import (
	"path/filepath"
	"testing"
)

func TestGoBuildEnvUsesPerBinaryCache(t *testing.T) {
	t.Parallel()

	binPath := filepath.Join(t.TempDir(), "bin", "amux")
	env := goBuildEnv([]string{"PATH=/bin", "GOCACHE=/shared"}, binPath)
	want := filepath.Join(filepath.Dir(binPath), ".gocache")

	if got := issueMetaEnvValue(env, "GOCACHE"); got != want {
		t.Fatalf("GOCACHE = %q, want %q", got, want)
	}
}

func TestIssueMetaScriptEnvUsesPerTestGoCache(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	env := issueMetaScriptEnv(tempDir)
	want := filepath.Join(tempDir, ".gocache")

	if got := issueMetaEnvValue(env, "GOCACHE"); got != want {
		t.Fatalf("GOCACHE = %q, want %q", got, want)
	}
}
