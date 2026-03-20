package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func envWithHome(home string) []string {
	env := append([]string{}, os.Environ()...)
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			env[i] = "HOME=" + home
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "HOME="+home)
	}
	return env
}

func TestBuildInstallInstallsTerminfo(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "amux")

	cmd := exec.Command("bash", "scripts/build-install.sh", dest)
	cmd.Env = envWithHome(home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build install failed: %v\n%s", err, out)
	}

	verify := exec.Command("infocmp", "-A", filepath.Join(home, ".terminfo"), "amux")
	verify.Env = envWithHome(home)
	termOut, err := verify.CombinedOutput()
	if err != nil {
		t.Fatalf("infocmp amux failed: %v\n%s", err, termOut)
	}
	if !strings.Contains(string(termOut), "amux") {
		t.Fatalf("infocmp output missing amux entry:\n%s", termOut)
	}
}

func TestBuildInstallRefusesCrossCheckoutOverwrite(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	if err := os.WriteFile(metaPath, []byte("source_repo=/tmp/other-checkout\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", "scripts/build-install.sh", dest)
	cmd.Env = envWithHome(t.TempDir())
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
	cmd.Env = append(envWithHome(t.TempDir()), "AMUX_INSTALL_FORCE=1")
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
	cmd.Env = envWithHome(t.TempDir())
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
