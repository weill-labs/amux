package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnWorkerScriptCreatesWorkerPaneWorktreeAndCodexSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "amux36")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	copyIssueMetaFixture(t, repoRoot, "scripts/spawn-worker.sh")

	amuxLogPath := filepath.Join(tempDir, "amux.log")
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeSpawnWorkerAmux(t, tempDir)
	writeFakeSpawnWorkerGit(t, tempDir)

	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts/spawn-worker.sh"), "--parent", "pane-109", "--issue", "LAB-499")
	cmd.Dir = repoRoot
	cmd.Env = issueMetaScriptEnv(tempDir,
		"FAKE_AMUX_LOG="+amuxLogPath,
		"FAKE_AMUX_SPLIT_OUTPUT=Split horizontal: new pane pane-210",
		"FAKE_GIT_LOG="+gitLogPath,
		"FAKE_GIT_TOPLEVEL="+repoRoot,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "pane-210" {
		t.Fatalf("stdout = %q, want %q", out, "pane-210")
	}

	worktreePath := filepath.Join(tempDir, "amux36-lab-499-pane-210")
	wantAmuxLog := strings.Join([]string{
		"split pane-109 --horizontal",
		"send-keys pane-210 cd " + worktreePath + " Enter",
		"send-keys pane-210 codex --yolo Enter",
		"wait vt-idle pane-210",
		"send-keys pane-210 Enter",
		"add-meta pane-210 issue=LAB-499",
	}, "\n")
	if got := readTrimmedFile(t, amuxLogPath); got != wantAmuxLog {
		t.Fatalf("amux log = %q, want %q", got, wantAmuxLog)
	}

	wantGitLog := strings.Join([]string{
		"rev-parse --show-toplevel",
		"worktree add -b lab-499-pane-210 " + worktreePath,
	}, "\n")
	if got := readTrimmedFile(t, gitLogPath); got != wantGitLog {
		t.Fatalf("git log = %q, want %q", got, wantGitLog)
	}
}

func TestSpawnWorkerScriptRequiresParentAndIssue(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "amux36")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	copyIssueMetaFixture(t, repoRoot, "scripts/spawn-worker.sh")

	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts/spawn-worker.sh"), "--parent", "pane-109")
	cmd.Dir = repoRoot
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected usage failure, got success\n%s", out)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), "usage: scripts/spawn-worker.sh --parent <pane> --issue <issue>") {
		t.Fatalf("output = %q, want usage", out)
	}
}

func writeFakeSpawnWorkerAmux(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
cmd="$1"
shift
{
    printf '%s' "$cmd"
    for arg in "$@"; do
        printf ' %s' "$arg"
    done
    printf '\n'
} >>"$FAKE_AMUX_LOG"
if [ "$cmd" = "split" ]; then
    printf '%s\n' "${FAKE_AMUX_SPLIT_OUTPUT:-Split horizontal: new pane pane-2}"
fi
`
	if err := os.WriteFile(filepath.Join(dir, "amux"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}
}

func writeFakeSpawnWorkerGit(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
cmd="$1"
shift
{
    printf '%s' "$cmd"
    for arg in "$@"; do
        printf ' %s' "$arg"
    done
    printf '\n'
} >>"$FAKE_GIT_LOG"

case "$cmd" in
    rev-parse)
        printf '%s\n' "$FAKE_GIT_TOPLEVEL"
        ;;
    worktree)
        mkdir -p "$4"
        ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
}
