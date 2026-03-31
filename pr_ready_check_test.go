package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPRReadyScriptNotifiesIdleOwnerWhenPRIsReadyForHumanMerge(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")

	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 7     pane-7               local           feature/pr-422                 main       worker       prs=[422]
EOF
exit 0
fi
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"idle":true,"current_command":"bash"}
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
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
cat <<'EOF'
[{"number":422,"title":"Ready for merge","url":"https://github.com/weill-labs/amux/pull/422","mergeable":"MERGEABLE"}]
EOF
exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS"}]
EOF
exit 0
fi
if [ "$1" = "api" ]; then
cat <<'EOF'
[{"user":{"login":"claude[bot]"},"body":"Looks good.\n\nLGTM","submitted_at":"2026-03-28T12:00:00Z"}]
EOF
exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runPRReadyCheck(t, tempDir, []string{"FAKE_AMUX_LOG=" + logPath}, nil)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, `owner=pane-7`) {
		t.Fatalf("output missing pane owner:\n%s", out)
	}
	if !strings.Contains(out, `notify=sent`) {
		t.Fatalf("output missing sent notification:\n%s", out)
	}
	if !strings.Contains(out, `review=LGTM`) {
		t.Fatalf("output missing Claude LGTM status:\n%s", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	if !strings.Contains(log, "send-keys pane-7 PR #422 is ready for human merge. CI is green, Claude left LGTM, and there are no merge conflicts. Enter") {
		t.Fatalf("amux log = %q, want ready notification", log)
	}
}

func TestCheckPRReadyScriptUsesLatestClaudeReviewBody(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")

	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 5     pane-5               local           feature/pr-500                 main       worker       prs=[500]
EOF
exit 0
fi
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"idle":true,"current_command":"bash"}
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
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
cat <<'EOF'
[{"number":500,"title":"Needs another pass","url":"https://github.com/weill-labs/amux/pull/500","mergeable":"MERGEABLE"}]
EOF
exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS"}]
EOF
exit 0
fi
if [ "$1" = "api" ]; then
cat <<'EOF'
[{"user":{"login":"claude[bot]"},"body":"Earlier approval.\n\nLGTM","submitted_at":"2026-03-28T10:00:00Z"},{"user":{"login":"claude[bot]"},"body":"Found one more issue to fix.","submitted_at":"2026-03-28T12:00:00Z"}]
EOF
exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runPRReadyCheck(t, tempDir, []string{"FAKE_AMUX_LOG=" + logPath}, nil)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "No open PRs are ready for human merge.") {
		t.Fatalf("output = %q, want no-ready message", out)
	}

	if got, err := os.ReadFile(logPath); err == nil && strings.TrimSpace(string(got)) != "" {
		t.Fatalf("expected no send-keys call, got log:\n%s", got)
	}
}

func TestCheckPRReadyScriptSkipsConflictingPRs(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")

	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 3     pane-3               local           feature/pr-77                  main       worker       prs=[77]
EOF
exit 0
fi
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"idle":true,"current_command":"bash"}
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
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
cat <<'EOF'
[{"number":77,"title":"Conflicting PR","url":"https://github.com/weill-labs/amux/pull/77","mergeable":"CONFLICTING"}]
EOF
exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS"}]
EOF
exit 0
fi
if [ "$1" = "api" ]; then
cat <<'EOF'
[{"user":{"login":"claude[bot]"},"body":"Still looks good.\n\nLGTM","submitted_at":"2026-03-28T12:00:00Z"}]
EOF
exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runPRReadyCheck(t, tempDir, []string{"FAKE_AMUX_LOG=" + logPath}, nil)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "No open PRs are ready for human merge.") {
		t.Fatalf("output = %q, want no-ready message", out)
	}

	if got, err := os.ReadFile(logPath); err == nil && strings.TrimSpace(string(got)) != "" {
		t.Fatalf("expected no send-keys call, got log:\n%s", got)
	}
}

func runPRReadyCheck(t *testing.T, tempDir string, extraEnv []string, extraArgs []string) (string, int) {
	t.Helper()

	args := append([]string{"run", "./cmd/check-pr-ready"}, extraArgs...)
	cmd := exec.Command("go", args...)
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
