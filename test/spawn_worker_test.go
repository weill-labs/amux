package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const spawnWorkerScriptPath = ".agents/skills/amux/scripts/spawn-worker.sh"

func TestSpawnWorkerScriptCreatesWorkerPaneWorktreeAndCodexSession(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "amux36")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	copyIssueMetaFixture(t, repoRoot, spawnWorkerScriptPath)

	amuxLogPath := filepath.Join(tempDir, "amux.log")
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeSpawnWorkerAmux(t, tempDir)
	writeFakeSpawnWorkerGit(t, tempDir)

	out, exitCode := runSpawnWorkerScript(t, repoRoot, tempDir, []string{
		"FAKE_AMUX_LOG=" + amuxLogPath,
		"FAKE_AMUX_SPLIT_OUTPUT=Split horizontal: new pane pane-210",
		"FAKE_GIT_LOG=" + gitLogPath,
		"FAKE_GIT_TOPLEVEL=" + repoRoot,
	}, "--parent", "pane-109", "--issue", "LAB-499")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(string(out)) != "pane-210" {
		t.Fatalf("stdout = %q, want %q", out, "pane-210")
	}

	worktreePath := filepath.Join(tempDir, "amux36-lab-499-pane-210")
	wantAmuxLog := strings.Join([]string{
		"spawn --at pane-109 --horizontal",
		"send-keys pane-210 cd " + worktreePath + " Enter",
		"wait idle pane-210",
		"send-keys pane-210 codex --yolo Enter",
		"wait idle pane-210",
		"send-keys pane-210 Enter",
		"meta set pane-210 issue=LAB-499",
	}, "\n")
	if got := readTrimmedFile(t, amuxLogPath); got != wantAmuxLog {
		t.Fatalf("amux log = %q, want %q", got, wantAmuxLog)
	}

	wantGitLog := strings.Join([]string{
		"rev-parse --show-toplevel",
		"fetch git@github.com:weill-labs/amux.git main:refs/remotes/origin/main",
		"worktree add -b lab-499-pane-210 " + worktreePath + " origin/main",
	}, "\n")
	if got := readTrimmedFile(t, gitLogPath); got != wantGitLog {
		t.Fatalf("git log = %q, want %q", got, wantGitLog)
	}
}

func TestSpawnWorkerScriptRequiresParentAndIssue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing issue",
			args: []string{"--parent", "pane-109"},
		},
		{
			name: "missing parent",
			args: []string{"--issue", "LAB-499"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			repoRoot := filepath.Join(tempDir, "amux36")
			if err := os.MkdirAll(repoRoot, 0755); err != nil {
				t.Fatalf("mkdir repo root: %v", err)
			}
			copyIssueMetaFixture(t, repoRoot, spawnWorkerScriptPath)

			out, exitCode := runSpawnWorkerScript(t, repoRoot, tempDir, nil, tt.args...)
			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
			}
			if !strings.Contains(string(out), "usage: scripts/spawn-worker.sh --parent <pane> --issue <issue>") {
				t.Fatalf("output = %q, want usage", out)
			}
		})
	}
}

func TestSpawnWorkerScriptFailsWhenSplitOutputIsUnparseable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "amux36")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	copyIssueMetaFixture(t, repoRoot, spawnWorkerScriptPath)

	amuxLogPath := filepath.Join(tempDir, "amux.log")
	writeFakeSpawnWorkerAmux(t, tempDir)
	writeFakeSpawnWorkerGit(t, tempDir)

	out, exitCode := runSpawnWorkerScript(t, repoRoot, tempDir, []string{
		"FAKE_AMUX_LOG=" + amuxLogPath,
		"FAKE_AMUX_SPLIT_OUTPUT=Split horizontal: pane missing",
		"FAKE_GIT_TOPLEVEL=" + repoRoot,
	}, "--parent", "pane-109", "--issue", "LAB-499")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
	}
	if !strings.Contains(string(out), "scripts/spawn-worker.sh: failed to parse new pane from: Split horizontal: pane missing") {
		t.Fatalf("output = %q, want parse failure", out)
	}
	if got := readTrimmedFile(t, amuxLogPath); got != "spawn --at pane-109 --horizontal" {
		t.Fatalf("amux log = %q, want only spawn call", got)
	}
}

func TestSpawnWorkerScriptPropagatesSplitFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "amux36")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	copyIssueMetaFixture(t, repoRoot, spawnWorkerScriptPath)

	amuxLogPath := filepath.Join(tempDir, "amux.log")
	writeFakeSpawnWorkerAmux(t, tempDir)
	writeFakeSpawnWorkerGit(t, tempDir)

	out, exitCode := runSpawnWorkerScript(t, repoRoot, tempDir, []string{
		"FAKE_AMUX_LOG=" + amuxLogPath,
		"FAKE_AMUX_SPLIT_STATUS=7",
		"FAKE_AMUX_SPLIT_STDERR=split failed",
		"FAKE_GIT_TOPLEVEL=" + repoRoot,
	}, "--parent", "pane-109", "--issue", "LAB-499")
	if exitCode != 7 {
		t.Fatalf("exit code = %d, want 7\n%s", exitCode, out)
	}
	if !strings.Contains(string(out), "split failed") {
		t.Fatalf("output = %q, want spawn stderr", out)
	}
	if got := readTrimmedFile(t, amuxLogPath); got != "spawn --at pane-109 --horizontal" {
		t.Fatalf("amux log = %q, want only spawn call", got)
	}
}

func runSpawnWorkerScript(t *testing.T, repoRoot, tempDir string, extraEnv []string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot, spawnWorkerScriptPath)}, args...)...)
	cmd.Dir = repoRoot
	cmd.Env = issueMetaScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exitErr := mustExitError(t, err, out)
	return string(out), exitErr.ExitCode()
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
if [ "$cmd" = "spawn" ]; then
    status="${FAKE_AMUX_SPLIT_STATUS:-0}"
    if [ -n "${FAKE_AMUX_SPLIT_STDERR:-}" ]; then
        printf '%s\n' "$FAKE_AMUX_SPLIT_STDERR" >&2
    fi
    if [ "$status" -eq 0 ]; then
        printf '%s\n' "${FAKE_AMUX_SPLIT_OUTPUT:-Split horizontal: new pane pane-2}"
    fi
    exit "$status"
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
        worktree_path="$4"
        mkdir -p "$worktree_path"
        ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
}
