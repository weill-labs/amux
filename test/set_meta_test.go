package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetaSetSetsTaskAndBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "task=build", "branch=feat/foo")
	if out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "feat/foo") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "build") {
		t.Fatalf("list should show task, got:\n%s", list)
	}
}

func TestMetaSetSetsPRAppendedToBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "branch=main", "pr=42")

	list := h.runCmd("list")
	if !strings.Contains(list, "main") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "#42") {
		t.Fatalf("list should show PR number, got:\n%s", list)
	}
}

func TestMetaSetClearsBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "branch=feat/bar")
	list := h.runCmd("list")
	if !strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be set, got:\n%s", list)
	}

	// Clear branch by setting to empty
	h.runCmd("meta", "set", "pane-1", "branch=")
	list = h.runCmd("list")
	if strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be cleared, got:\n%s", list)
	}
}

func TestMetaSetClearsBranchAcrossIdleRefresh(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithOptions(t, 80, 24, "", false, false, "AMUX_DISABLE_META_REFRESH=0")

	repoDir := filepath.Join(h.home, "clear-branch-repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", repoDir, err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "init", "-b", "meta-branch").CombinedOutput(); err != nil {
		t.Fatalf("git init repo: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.email", "amux-tests@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.name", "amux tests").CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("clear branch\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	h.sendKeys("pane-1", fmt.Sprintf("cd %q && echo META_READY", repoDir), "Enter")
	h.waitFor("pane-1", "META_READY")
	h.waitIdle("pane-1")
	waitForListMetadata(t, h, "meta-branch")

	h.runCmd("meta", "set", "pane-1", "branch=")

	h.sendKeys("pane-1", "printf 'REFRESH\\n'", "Enter")
	h.waitFor("pane-1", "REFRESH")
	h.waitIdle("pane-1")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		list := h.runCmd("list")
		if strings.Contains(list, "meta-branch") {
			t.Fatalf("cleared branch should stay empty across idle refresh, got:\n%s", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMetaSetAllowsGenericKeys(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "bogus=value")
	if out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "bogus=value") {
		t.Fatalf("list should show generic metadata, got:\n%s", list)
	}
}

func TestMetaSetRejectsInvalidFormat(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "noequalssign")
	if !strings.Contains(out, "invalid key=value") {
		t.Fatalf("expected format error, got: %q", out)
	}
}

func TestMetaSetRejectsMissingArgs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set")
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage error, got: %q", out)
	}
}

func TestMetaSetRejectsUnknownPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "no-such-pane", "task=x")
	if out == "" {
		t.Fatal("expected error for unknown pane")
	}
}

func TestListShowsBranchHeader(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	list := h.runCmd("list")
	if !strings.Contains(list, "BRANCH") {
		t.Fatalf("list header should contain BRANCH column, got:\n%s", list)
	}
}
