package main_test

import (
	"fmt"
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

func envWithHomeAndBranch(t *testing.T, home, branch string, extra ...string) []string {
	t.Helper()

	env := envWithHome(home)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}

	fakeGitDir := t.TempDir()
	fakeGit := filepath.Join(fakeGitDir, "git")
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "--abbrev-ref" && "${3:-}" == "HEAD" ]]; then
	printf '%%s\n' %q
	exit 0
fi

exec %q "$@"
`, branch, gitPath)
	if err := os.WriteFile(fakeGit, []byte(script), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	pathValue := fakeGitDir
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + fakeGitDir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "PATH="+pathValue)
	}

	return append(env, extra...)
}

func TestBuildInstallInstallsTerminfo(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "amux")

	cmd := exec.Command("bash", "scripts/install.sh", dest)
	cmd.Env = envWithHomeAndBranch(t, home, "main")
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

func TestBuildInstallRewritesInvalidMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := repoRoot(t)
	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	if err := os.WriteFile(metaPath, []byte("not-valid-metadata\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", "scripts/install.sh", dest)
	cmd.Env = envWithHomeAndBranch(t, t.TempDir(), "main")
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
