package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckClaudeReviewScriptReportsLatestLGTM(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","comments":[{"id":"IC_old","author":{"login":"github-actions"},"body":"**Claude finished @cweill's task in 1m 0s**\n\n### Findings\n\n**Blocking: old finding**","createdAt":"2026-03-29T01:00:00Z","url":"https://example.com/pr/422#issuecomment-old"},{"id":"IC_codecov","author":{"login":"codecov"},"body":"coverage looks good","createdAt":"2026-03-29T01:01:00Z","url":"https://example.com/pr/422#issuecomment-codecov"},{"id":"IC_lgtm","author":{"login":"github-actions"},"body":"**Claude finished @cweill's task in 45s**\n\n### Review\n\nNo blocking issues.\n\nLGTM","createdAt":"2026-03-29T01:02:00Z","url":"https://example.com/pr/422#issuecomment-lgtm"},{"id":"IC_user","author":{"login":"cweill"},"body":"thanks","createdAt":"2026-03-29T01:03:00Z","url":"https://example.com/pr/422#issuecomment-user"}]}
EOF
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	out, exitCode := runClaudeReviewCheck(t, tempDir, nil)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	for _, want := range []string{
		"pr=422",
		"verdict=lgtm",
		"comment_id=IC_lgtm",
		"url=https://example.com/pr/422#issuecomment-lgtm",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCheckClaudeReviewScriptReportsLatestFindings(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":511,"url":"https://example.com/pr/511","comments":[{"id":"IC_lgtm","author":{"login":"github-actions"},"body":"**Claude finished @cweill's task in 1m 0s**\n\nLGTM","createdAt":"2026-03-29T01:00:00Z","url":"https://example.com/pr/511#issuecomment-lgtm"},{"id":"IC_prompt","author":{"login":"cweill"},"body":"@claude review this PR","createdAt":"2026-03-29T01:01:00Z","url":"https://example.com/pr/511#issuecomment-prompt"},{"id":"IC_codecov","author":{"login":"codecov"},"body":"coverage looks good","createdAt":"2026-03-29T01:02:00Z","url":"https://example.com/pr/511#issuecomment-codecov"},{"id":"IC_findings","author":{"login":"claude"},"body":"**Claude finished @cweill's task in 50s**\n\n### Findings\n\n**Blocking: test still fails**","createdAt":"2026-03-29T01:03:00Z","url":"https://example.com/pr/511#issuecomment-findings"}]}
EOF
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	out, exitCode := runClaudeReviewCheck(t, tempDir, nil)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	for _, want := range []string{
		"pr=511",
		"verdict=findings",
		"comment_id=IC_findings",
		"url=https://example.com/pr/511#issuecomment-findings",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCheckClaudeReviewScriptWatchesForNextClaudeReview(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "gh.state")
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	count=0
	if [[ -f "$FAKE_GH_STATE" ]]; then
		count="$(cat "$FAKE_GH_STATE")"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" >"$FAKE_GH_STATE"

	if (( count < 2 )); then
		cat <<'EOF'
{"number":533,"url":"https://example.com/pr/533","comments":[{"id":"IC_old","author":{"login":"github-actions"},"body":"**Claude finished @cweill's task in 1m 5s**\n\n### Findings\n\n**Blocking: still broken**","createdAt":"2026-03-29T01:00:00Z","url":"https://example.com/pr/533#issuecomment-old"}]}
EOF
		exit 0
	fi

	cat <<'EOF'
{"number":533,"url":"https://example.com/pr/533","comments":[{"id":"IC_old","author":{"login":"github-actions"},"body":"**Claude finished @cweill's task in 1m 5s**\n\n### Findings\n\n**Blocking: still broken**","createdAt":"2026-03-29T01:00:00Z","url":"https://example.com/pr/533#issuecomment-old"},{"id":"IC_new","author":{"login":"claude"},"body":"**Claude finished @cweill's task in 45s**\n\n### Review\n\nNo blocking issues.\n\nLGTM","createdAt":"2026-03-29T01:02:00Z","url":"https://example.com/pr/533#issuecomment-new"}]}
EOF
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	out, exitCode := runClaudeReviewCheck(t, tempDir, []string{
		"FAKE_GH_STATE=" + statePath,
		"AMUX_CLAUDE_REVIEW_POLL_INTERVAL=0.01",
		"AMUX_CLAUDE_REVIEW_TIMEOUT=2",
	}, "--watch")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	for _, want := range []string{
		"Watching PR #533 for a new Claude review after comment IC_old",
		"pr=533",
		"verdict=lgtm",
		"comment_id=IC_new",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func runClaudeReviewCheck(t *testing.T, tempDir string, extraEnv []string, args ...string) (string, int) {
	t.Helper()

	cmdArgs := append([]string{"scripts/check-claude-review.sh"}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}
