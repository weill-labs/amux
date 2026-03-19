package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInstallRefusesCrossCheckoutOverwrite(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	if err := os.WriteFile(metaPath, []byte("source_repo=/tmp/other-checkout\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", "scripts/build-install.sh", dest)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cross-checkout install to fail\n%s", out)
	}
	if !strings.Contains(string(out), "refusing to overwrite") {
		t.Fatalf("expected refusal message, got:\n%s", out)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to remain absent, stat err=%v", dest, statErr)
	}
}

func TestBuildInstallForceOverridesCrossCheckoutMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := repoRoot(t)
	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	if err := os.WriteFile(metaPath, []byte("source_repo=/tmp/other-checkout\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", "scripts/build-install.sh", dest)
	cmd.Env = append(os.Environ(), "AMUX_INSTALL_FORCE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forced install failed: %v\n%s", err, out)
	}

	if _, statErr := os.Stat(dest); statErr != nil {
		t.Fatalf("installed binary missing: %v", statErr)
	}

	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if !strings.Contains(string(meta), "source_repo="+repoRoot) {
		t.Fatalf("expected updated source_repo in metadata, got:\n%s", meta)
	}
}

func TestBuildInstallRewritesInvalidMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := repoRoot(t)
	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	if err := os.WriteFile(metaPath, []byte("not-valid-metadata\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", "scripts/build-install.sh", dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install with invalid metadata failed: %v\n%s", err, out)
	}

	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if !strings.Contains(string(meta), "source_repo="+repoRoot) {
		t.Fatalf("expected metadata rewrite, got:\n%s", meta)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		t.Fatalf("repo root: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
