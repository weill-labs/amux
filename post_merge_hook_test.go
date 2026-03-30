package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostMergeMainSyncScriptChecksOutMainAndPulls(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeGit(t, tempDir)

	out, exitCode := runBashScriptWithInput(t, "scripts/post-merge-main-sync.sh", "", postMergeHookEnv(tempDir,
		"FAKE_GIT_LOG="+gitLogPath,
		"FAKE_GIT_BRANCH=lab-502-post-merge-main-sync",
	))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(out) != "Checked out main and pulled latest origin/main." {
		t.Fatalf("output = %q, want %q", out, "Checked out main and pulled latest origin/main.")
	}

	gotLog := readTrimmedFile(t, gitLogPath)
	for _, want := range []string{
		"diff --quiet --ignore-submodules --",
		"diff --cached --quiet --ignore-submodules --",
		"branch --show-current",
		"checkout main",
		"pull --ff-only",
	} {
		if !strings.Contains(gotLog, want) {
			t.Fatalf("git log missing %q:\n%s", want, gotLog)
		}
	}
}

func TestPostMergeMainSyncScriptSkipsDirtyWorktree(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeGit(t, tempDir)

	out, exitCode := runBashScriptWithInput(t, "scripts/post-merge-main-sync.sh", "", postMergeHookEnv(tempDir,
		"FAKE_GIT_LOG="+gitLogPath,
		"FAKE_GIT_DIRTY=1",
	))
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "Cannot auto-sync main after merge: worktree has unstaged changes.") {
		t.Fatalf("output = %q, want dirty-worktree warning", out)
	}

	gotLog := readTrimmedFile(t, gitLogPath)
	if strings.Contains(gotLog, "checkout main") || strings.Contains(gotLog, "pull --ff-only") {
		t.Fatalf("dirty worktree should not attempt checkout or pull:\n%s", gotLog)
	}
}

func TestPostMergeMainSyncScriptSkipsUntrackedFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeGit(t, tempDir)

	out, exitCode := runBashScriptWithInput(t, "scripts/post-merge-main-sync.sh", "", postMergeHookEnv(tempDir,
		"FAKE_GIT_LOG="+gitLogPath,
		"FAKE_GIT_UNTRACKED=1",
	))
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "Cannot auto-sync main after merge: worktree has untracked files.") {
		t.Fatalf("output = %q, want untracked-files warning", out)
	}

	gotLog := readTrimmedFile(t, gitLogPath)
	if !strings.Contains(gotLog, "ls-files --others --exclude-standard") {
		t.Fatalf("git log missing untracked-files check:\n%s", gotLog)
	}
	if strings.Contains(gotLog, "checkout main") || strings.Contains(gotLog, "pull --ff-only") {
		t.Fatalf("untracked files should not attempt checkout or pull:\n%s", gotLog)
	}
}

func TestPostMergeHookSyncsMainAndRunsPaneMetaSyncScript(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLogPath := filepath.Join(tempDir, "git.log")
	amuxLogPath := filepath.Join(tempDir, "amux.log")
	writeFakeGit(t, tempDir)
	writeFakeAmux(t, tempDir)
	writeFakeGH(t, tempDir)
	writeFakeCurl(t, tempDir)
	writeFakeDate(t, tempDir)

	input := `{"tool_input":{"command":"gh pr merge 470 --squash"}}`
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/post-merge-postmortem.sh", input, postMergeHookEnv(tempDir,
		"FAKE_GIT_LOG="+gitLogPath,
		"FAKE_GIT_BRANCH=lab-502-post-merge-main-sync",
		"FAKE_AMUX_LOG="+amuxLogPath,
		"AMUX_PANE=7",
		"LINEAR_API_KEY=test-linear-token",
	))
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
	}
	if !strings.Contains(out, "Checked out main and pulled latest origin/main.") {
		t.Fatalf("output = %q, want main-sync confirmation", out)
	}
	if !strings.Contains(out, "Run /postmortem now") {
		t.Fatalf("output = %q, want postmortem reminder", out)
	}

	if got := readTrimmedFile(t, amuxLogPath); !strings.Contains(got, "set-kv 7 ") {
		t.Fatalf("amux args = %q, want set-kv sync call", got)
	}
}

func TestPostMergeHookIgnoresNonMergeCommands(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLogPath := filepath.Join(tempDir, "git.log")
	writeFakeGit(t, tempDir)

	input := `{"tool_input":{"command":"git push origin HEAD"}}`
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/post-merge-postmortem.sh", input, postMergeHookEnv(tempDir,
		"FAKE_GIT_LOG="+gitLogPath,
	))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output, got:\n%s", out)
	}
	if _, err := os.Stat(gitLogPath); !os.IsNotExist(err) {
		t.Fatalf("non-merge command should not call git; git log exists with: %q", readTrimmedFile(t, gitLogPath))
	}
}

func runBashScriptWithInput(t *testing.T, scriptPath, input string, env []string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = "."
	cmd.Env = env
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error running %s: %v\n%s", scriptPath, err, out)
	}
	return string(out), exitErr.ExitCode()
}

func postMergeHookEnv(tempDir string, extra ...string) []string {
	env := append([]string{}, hermeticMainEnv()...)
	env = upsertIssueMetaEnv(env, "PATH", tempDir+string(os.PathListSeparator)+issueMetaEnvValue(env, "PATH"))
	return append(env, extra...)
}

func writeFakeGit(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
cmd="$1"
shift
if [ -n "$FAKE_GIT_LOG" ]; then
    {
        printf '%s' "$cmd"
        for arg in "$@"; do
            printf ' %s' "$arg"
        done
        printf '\n'
    } >>"$FAKE_GIT_LOG"
fi
	case "$cmd" in
	    rev-parse)
	        printf '%s\n' "${FAKE_GIT_TOPLEVEL:-$PWD}"
	        ;;
	    diff)
        if [ "$1" = "--cached" ]; then
            [ -n "$FAKE_GIT_CACHED_DIRTY" ] && exit 1
            exit 0
        fi
	        [ -n "$FAKE_GIT_DIRTY" ] && exit 1
	        exit 0
	        ;;
	    ls-files)
	        if [ -n "$FAKE_GIT_UNTRACKED" ]; then
	            printf '%s\n' "scratch.txt"
	        fi
	        ;;
	    branch)
	        if [ "$1" = "--show-current" ]; then
	            printf '%s\n' "${FAKE_GIT_BRANCH:-feature-branch}"
        fi
        ;;
    checkout)
        if [ -n "$FAKE_GIT_CHECKOUT_FAIL" ]; then
            printf '%s\n' "$FAKE_GIT_CHECKOUT_FAIL" >&2
            exit 1
        fi
        ;;
    pull)
        if [ -n "$FAKE_GIT_PULL_FAIL" ]; then
            printf '%s\n' "$FAKE_GIT_PULL_FAIL" >&2
            exit 1
        fi
        ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
}

func writeFakeAmux(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"cwd":"/tmp/project","meta":{"tracked_prs":[{"number":470}],"tracked_issues":[{"id":"LAB-445"}]}}
EOF
exit 0
fi
printf '%s' "$1" >>"$FAKE_AMUX_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
done
printf '\n' >>"$FAKE_AMUX_LOG"
`
	if err := os.WriteFile(filepath.Join(dir, "amux"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}
}

func writeFakeGH(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
printf '2026-03-28T12:34:56Z\n'
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}

func writeFakeCurl(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
cat <<'EOF'
{"data":{"issue":{"state":{"type":"completed"}}}}
EOF
`
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
}

func writeFakeDate(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
printf '2026-03-28T12:34:56Z\n'
`
	if err := os.WriteFile(filepath.Join(dir, "date"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake date: %v", err)
	}
}

func readTrimmedFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}
