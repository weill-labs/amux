package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPaneIssueMetaWarnsWhenIssueMetadataMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
cat <<'EOF'
{"meta":{"kv":{}}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/check-pane-issue-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when issue metadata is missing\n%s", out)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), `scripts/set-pane-issue.sh LAB-XXX`) {
		t.Fatalf("missing helper guidance in output:\n%s", out)
	}
}

func TestCheckPaneIssueMetaSkipsLeadPane(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
cat <<'EOF'
{"lead":true,"meta":{"tracked_issues":[]}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/check-pane-issue-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success for lead pane (no issue metadata required): %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected no output, got:\n%s", out)
	}
}

func TestCheckPaneIssueMetaPassesWhenIssueMetadataExists(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
cat <<'EOF'
{"meta":{"kv":{"issue":"LAB-445"}}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/check-pane-issue-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success when issue metadata exists: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected no output, got:\n%s", out)
	}
}

func TestSetPaneIssueScriptUsesActorPaneByDefault(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
printf '%s' "$1" >"$FAKE_AMUX_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
done
printf '\n' >>"$FAKE_AMUX_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/set-pane-issue.sh", "LAB-445")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_AMUX_LOG="+logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.TrimSpace(string(got)) != "meta set 7 issue=LAB-445" {
		t.Fatalf("amux args = %q, want %q", got, "meta set 7 issue=LAB-445")
	}
}

func TestSyncPanePRMetaScriptAddsCurrentIssueAndPR(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	copyIssueMetaFixture(t, tempDir, "scripts/sync-pane-pr-meta.sh")
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"meta":{"kv":{"issue":"LAB-445"}}}
EOF
exit 0
fi
printf '%s' "$1" >"$FAKE_AMUX_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
done
printf '\n' >>"$FAKE_AMUX_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	ghPath := filepath.Join(tempDir, "gh")
	if err := os.WriteFile(ghPath, []byte(`#!/bin/sh
printf '422\n'
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	cmd := exec.Command("bash", filepath.Join(tempDir, "scripts/sync-pane-pr-meta.sh"))
	cmd.Dir = tempDir
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_AMUX_LOG="+logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.TrimSpace(string(got)) != "meta set 7 pr=422 issue=LAB-445" {
		t.Fatalf("amux args = %q, want %q", got, "meta set 7 pr=422 issue=LAB-445")
	}
}

func TestGHPRCreateScriptSyncsPanePRMeta(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	copyIssueMetaFixture(t, tempDir, "scripts/gh-pr-create.sh")
	copyIssueMetaFixture(t, tempDir, "scripts/sync-pane-pr-meta.sh")
	amuxLogPath := filepath.Join(tempDir, "amux.log")
	ghLogPath := filepath.Join(tempDir, "gh.log")

	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"meta":{"kv":{"issue":"LAB-445"}}}
EOF
exit 0
fi
printf '%s' "$1" >"$FAKE_AMUX_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
done
printf '\n' >>"$FAKE_AMUX_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	ghPath := filepath.Join(tempDir, "gh")
	if err := os.WriteFile(ghPath, []byte(`#!/bin/sh
printf '%s' "$1" >>"$FAKE_GH_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_GH_LOG"
done
printf '\n' >>"$FAKE_GH_LOG"

if [ "$1" = "create" ]; then
    printf 'https://github.com/weill-labs/amux/pull/422\n'
    exit 0
fi

if [ "$1" = "view" ]; then
    printf '422\n'
    exit 0
fi

exit 1
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	cmd := exec.Command("bash", filepath.Join(tempDir, "scripts/gh-pr-create.sh"), "--fill", "--draft")
	cmd.Dir = tempDir
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_AMUX_LOG="+amuxLogPath, "FAKE_GH_LOG="+ghLogPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "https://github.com/weill-labs/amux/pull/422" {
		t.Fatalf("stdout = %q, want PR URL", out)
	}

	ghLog, err := os.ReadFile(ghLogPath)
	if err != nil {
		t.Fatalf("read fake gh log: %v", err)
	}
	if got := strings.TrimSpace(string(ghLog)); got != "pr create --fill --draft\npr view --json number --jq .number" {
		t.Fatalf("gh args = %q, want create then view", got)
	}

	amuxLog, err := os.ReadFile(amuxLogPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.TrimSpace(string(amuxLog)) != "meta set 7 pr=422 issue=LAB-445" {
		t.Fatalf("amux args = %q, want %q", amuxLog, "meta set 7 pr=422 issue=LAB-445")
	}
}

func TestPrePushHookSyncsPanePRMeta(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	copyIssueMetaFixture(t, tempDir, ".githooks/pre-push")
	syncLogPath := filepath.Join(tempDir, "sync.log")
	syncScriptPath := filepath.Join(tempDir, "scripts/sync-pane-pr-meta.sh")
	diffCoveragePath := filepath.Join(tempDir, "scripts/check-diff-coverage.sh")

	if err := os.MkdirAll(filepath.Dir(syncScriptPath), 0755); err != nil {
		t.Fatalf("mkdir scripts dir: %v", err)
	}
	if err := os.WriteFile(syncScriptPath, []byte(`#!/bin/sh
printf 'sync\n' >"$FAKE_SYNC_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake sync script: %v", err)
	}
	if err := os.WriteFile(diffCoveragePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake diff coverage script: %v", err)
	}

	initRepo := exec.Command("git", "init", "-q")
	initRepo.Dir = tempDir
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("git init temp repo: %v\n%s", err, out)
	}

	cmd := exec.Command("bash", filepath.Join(tempDir, ".githooks/pre-push"), "origin", "git@github.com:weill-labs/amux.git")
	cmd.Dir = tempDir
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_SYNC_LOG="+syncLogPath)
	cmd.Stdin = strings.NewReader("refs/heads/pr-476 1111111111111111111111111111111111111111 refs/heads/pr-476 2222222222222222222222222222222222222222\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	syncLog, err := os.ReadFile(syncLogPath)
	if err != nil {
		t.Fatalf("read fake sync log: %v", err)
	}
	if strings.TrimSpace(string(syncLog)) != "sync" {
		t.Fatalf("sync log = %q, want %q", syncLog, "sync")
	}
}

func TestPrePushHookWarnsWhenOriginMainDiffersFromGitHubMain(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	copyIssueMetaFixture(t, tempDir, ".githooks/pre-push")
	syncLogPath := filepath.Join(tempDir, "sync.log")
	syncScriptPath := filepath.Join(tempDir, "scripts/sync-pane-pr-meta.sh")
	diffCoveragePath := filepath.Join(tempDir, "scripts/check-diff-coverage.sh")
	ghPath := filepath.Join(tempDir, "gh")

	if err := os.MkdirAll(filepath.Dir(syncScriptPath), 0755); err != nil {
		t.Fatalf("mkdir scripts dir: %v", err)
	}
	if err := os.WriteFile(syncScriptPath, []byte(`#!/bin/sh
printf 'sync\n' >"$FAKE_SYNC_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake sync script: %v", err)
	}
	if err := os.WriteFile(diffCoveragePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake diff coverage script: %v", err)
	}
	if err := os.WriteFile(ghPath, []byte(`#!/bin/sh
printf '%s\n' "$FAKE_GITHUB_MAIN_SHA"
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	initRepo := exec.Command("git", "init", "-q")
	initRepo.Dir = tempDir
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("git init temp repo: %v\n%s", err, out)
	}

	writeTrackedFile(t, tempDir, "README.md", "one\n")
	firstSHA := commitAll(t, tempDir, "first")
	writeTrackedFile(t, tempDir, "README.md", "two\n")
	secondSHA := commitAll(t, tempDir, "second")

	updateRef := exec.Command("git", "update-ref", "refs/remotes/origin/main", firstSHA)
	updateRef.Dir = tempDir
	if out, err := updateRef.CombinedOutput(); err != nil {
		t.Fatalf("git update-ref origin/main: %v\n%s", err, out)
	}

	cmd := exec.Command("bash", filepath.Join(tempDir, ".githooks/pre-push"), "origin", "git@github.com:weill-labs/amux.git")
	cmd.Dir = tempDir
	cmd.Env = issueMetaScriptEnv(
		tempDir,
		"FAKE_SYNC_LOG="+syncLogPath,
		"FAKE_GITHUB_MAIN_SHA="+secondSHA,
	)
	cmd.Stdin = strings.NewReader("refs/heads/pr-537 1111111111111111111111111111111111111111 refs/heads/pr-537 2222222222222222222222222222222222222222\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	if !strings.Contains(string(out), "WARNING: local origin/main") {
		t.Fatalf("expected drift warning in output:\n%s", out)
	}
	if !strings.Contains(string(out), firstSHA[:12]) || !strings.Contains(string(out), secondSHA[:12]) {
		t.Fatalf("warning missing short SHAs:\n%s", out)
	}
	if !strings.Contains(string(out), "git fetch git@github.com:weill-labs/amux.git main:refs/remotes/origin/main") {
		t.Fatalf("warning missing GitHub fetch guidance:\n%s", out)
	}

	syncLog, err := os.ReadFile(syncLogPath)
	if err != nil {
		t.Fatalf("read fake sync log: %v", err)
	}
	if strings.TrimSpace(string(syncLog)) != "sync" {
		t.Fatalf("sync log = %q, want %q", syncLog, "sync")
	}
}

func issueMetaScriptEnv(tempDir string, extra ...string) []string {
	env := append([]string{}, hermeticMainEnv()...)
	env = upsertIssueMetaEnv(env, "PATH", tempDir+string(os.PathListSeparator)+issueMetaEnvValue(env, "PATH"))
	env = upsertIssueMetaEnv(env, "AMUX_PANE", "7")
	env = upsertIssueMetaEnv(env, "AMUX_SESSION", "test-session")
	return append(env, extra...)
}

func writeTrackedFile(t *testing.T, repoDir, relPath, contents string) {
	t.Helper()

	path := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func commitAll(t *testing.T, repoDir, message string) string {
	t.Helper()

	add := exec.Command("git", "add", ".")
	add.Dir = repoDir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add .: %v\n%s", err, out)
	}

	commit := exec.Command(
		"git",
		"-c", "user.name=amux-tests",
		"-c", "user.email=amux-tests@example.com",
		"commit",
		"-q",
		"-m",
		message,
	)
	commit.Dir = repoDir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit %q: %v\n%s", message, err, out)
	}

	revParse := exec.Command("git", "rev-parse", "HEAD")
	revParse.Dir = repoDir
	out, err := revParse.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func issueMetaEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func upsertIssueMetaEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func copyIssueMetaFixture(t *testing.T, root, relPath string) string {
	t.Helper()

	data, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", relPath, err)
	}
	info, err := os.Stat(relPath)
	if err != nil {
		t.Fatalf("stat fixture %s: %v", relPath, err)
	}

	dst := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatalf("mkdir fixture dir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(dst, data, info.Mode()); err != nil {
		t.Fatalf("write fixture %s: %v", relPath, err)
	}
	return dst
}
