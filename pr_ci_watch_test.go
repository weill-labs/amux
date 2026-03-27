package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchPRCIScriptPassesWhenRequiredChecksSucceed(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"deadbeef"}
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / test","url":"https://example.com/run/999","conclusion":"","status":"in_progress"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --watch "* ]]; then
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	cmd := exec.Command("bash", "scripts/watch-pr-ci.sh")
	cmd.Dir = "."
	cmd.Env = ciWatchScriptEnv(t, tempDir, "FAKE_GH_LOG="+ghLog)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PR #422 CI passed") {
		t.Fatalf("output = %q, want success message", out)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "run list --commit deadbeef") {
		t.Fatalf("gh log = %q, want pre-watch head run discovery", log)
	}
	if !strings.Contains(log, "pr checks 422 --required --watch") {
		t.Fatalf("gh log = %q, want watch invocation", log)
	}
}

func TestWatchPRCIScriptPrintsFailedRunLogs(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"deadbeef"}
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --watch "* ]]; then
	exit 1
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --json "* ]]; then
	cat <<'EOF'
[{"name":"go test ./...","bucket":"fail","state":"FAILURE","link":"https://github.com/weill-labs/amux/actions/runs/999/job/123","workflow":"CI"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / test","url":"https://example.com/run/999","conclusion":"","status":"in_progress"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "view" && "${3:-}" == "--job" && "${4:-}" == "123" && "${5:-}" == "--log-failed" ]]; then
	cat <<'EOF'
FAIL step output
--- FAIL: TestFlakyThing
EOF
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	cmd := exec.Command("bash", "scripts/watch-pr-ci.sh")
	cmd.Dir = "."
	cmd.Env = ciWatchScriptEnv(t, tempDir, "FAKE_GH_LOG="+ghLog)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure when CI fails\n%s", out)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}

	output := string(out)
	for _, want := range []string{
		"PR #422 CI failed",
		"go test ./...",
		"https://github.com/weill-labs/amux/actions/runs/999/job/123",
		"FAIL step output",
		"TestFlakyThing",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "run view --job 123 --log-failed") {
		t.Fatalf("gh log = %q, want failed-log lookup by job id", log)
	}
}

func TestPushAndWatchCIScriptRunsPushBeforeWatching(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLog := filepath.Join(tempDir, "git.log")
	ghLog := filepath.Join(tempDir, "gh.log")

	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GIT_LOG"
exit 0
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"deadbeef"}
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --watch "* ]]; then
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	cmd := exec.Command("bash", "scripts/push-and-watch-ci.sh", "origin", "HEAD")
	cmd.Dir = "."
	cmd.Env = ciWatchScriptEnv(t, tempDir, "FAKE_GIT_LOG="+gitLog, "FAKE_GH_LOG="+ghLog)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	gitBytes, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatalf("read git log: %v", err)
	}
	if strings.TrimSpace(string(gitBytes)) != "push origin HEAD" {
		t.Fatalf("git log = %q, want %q", gitBytes, "push origin HEAD")
	}

	ghBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	if !strings.Contains(string(ghBytes), "pr checks 422 --required --watch") {
		t.Fatalf("gh log = %q, want watch invocation", ghBytes)
	}
}

func ciWatchScriptEnv(t *testing.T, fakeBinDir string, extra ...string) []string {
	t.Helper()

	env := append([]string{}, hermeticMainEnv()...)
	env = upsertCIWatchEnv(env, "PATH", fakeBinDir+string(os.PathListSeparator)+ciWatchEnvValue(env, "PATH"))
	return append(env, extra...)
}

func ciWatchEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func upsertCIWatchEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
