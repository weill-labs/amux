package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWatchPRCIScriptPassesWhenRequiredChecksSucceed(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	ghState := filepath.Join(tempDir, "gh.state")
	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "HEAD" ]]; then
	printf 'deadbeef\n'
	exit 0
fi

echo "unexpected git invocation: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"stale-sha"}
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / test","url":"https://example.com/run/999","conclusion":"","status":"in_progress"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --json "* ]]; then
	count=0
	if [[ -f "$FAKE_GH_STATE" ]]; then
		count="$(cat "$FAKE_GH_STATE")"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" >"$FAKE_GH_STATE"
	if [[ "$count" -eq 1 ]]; then
		echo "no checks reported on the 'lab-489-workers-self-monitor-ci' branch" >&2
		exit 1
	fi
	cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS","link":"https://example.com/run/999","workflow":"CI","startedAt":"2024-01-01T00:00:00Z","completedAt":"2024-01-01T00:00:05Z"}]
EOF
	exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	cmd := exec.Command("bash", "scripts/watch-pr-ci.sh")
	cmd.Dir = "."
	cmd.Env = ciWatchScriptEnv(t, tempDir, "FAKE_GH_LOG="+ghLog, "FAKE_GH_STATE="+ghState, "AMUX_PR_RUN_DISCOVERY_TIMEOUT=1", "AMUX_PR_RUN_DISCOVERY_INTERVAL=1")
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
	if got := strings.Count(log, "pr checks 422 --required --json"); got < 2 {
		t.Fatalf("gh log = %q, want repeated required-check polling", log)
	}
}

func TestWatchPRCIScriptPrintsCheckProgressHeartbeatsAndTransitions(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	ghState := filepath.Join(tempDir, "gh.state")
	nowFile := installFakeClock(t, tempDir, 1700000000)

	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "HEAD" ]]; then
	printf 'deadbeef\n'
	exit 0
fi

echo "unexpected git invocation: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"stale-sha"}
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / test","url":"https://example.com/run/999","conclusion":"","status":"in_progress"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --json "* ]]; then
	count=0
	if [[ -f "$FAKE_GH_STATE" ]]; then
		count="$(cat "$FAKE_GH_STATE")"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" >"$FAKE_GH_STATE"

	case "$count" in
		1)
			cat <<'EOF'
[{"name":"test","bucket":"pending","state":"QUEUED","link":"https://example.com/run/1000","workflow":"CI","startedAt":"","completedAt":""},{"name":"claude-review","bucket":"pending","state":"QUEUED","link":"https://example.com/run/1001","workflow":"Claude","startedAt":"","completedAt":""}]
EOF
			exit 8
			;;
		2|3|4)
			cat <<'EOF'
[{"name":"test","bucket":"pending","state":"IN_PROGRESS","link":"https://example.com/run/1000","workflow":"CI","startedAt":"2023-11-14T22:13:20Z","completedAt":""},{"name":"claude-review","bucket":"pending","state":"QUEUED","link":"https://example.com/run/1001","workflow":"Claude","startedAt":"","completedAt":""}]
EOF
			exit 8
			;;
		5)
			cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS","link":"https://example.com/run/1000","workflow":"CI","startedAt":"2023-11-14T22:13:20Z","completedAt":"2023-11-14T22:13:58Z"},{"name":"claude-review","bucket":"pending","state":"IN_PROGRESS","link":"https://example.com/run/1001","workflow":"Claude","startedAt":"2023-11-14T22:13:50Z","completedAt":""}]
EOF
			exit 8
			;;
		*)
			cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS","link":"https://example.com/run/1000","workflow":"CI","startedAt":"2023-11-14T22:13:20Z","completedAt":"2023-11-14T22:13:58Z"},{"name":"claude-review","bucket":"pass","state":"SUCCESS","link":"https://example.com/run/1001","workflow":"Claude","startedAt":"2023-11-14T22:13:50Z","completedAt":"2023-11-14T22:14:05Z"}]
EOF
			exit 0
			;;
	esac
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`)

	cmd := exec.Command("bash", "scripts/watch-pr-ci.sh")
	cmd.Dir = "."
	cmd.Env = ciWatchScriptEnv(
		t,
		tempDir,
		"FAKE_GH_LOG="+ghLog,
		"FAKE_GH_STATE="+ghState,
		"FAKE_NOW_FILE="+nowFile,
		"AMUX_PR_CHECK_INTERVAL=10",
		"AMUX_PR_HEARTBEAT_INTERVAL=30",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	output := string(out)
	for _, want := range []string{
		"Waiting for: test, claude-review",
		"test: queued",
		"claude-review: queued",
		"test: in_progress",
		"Heartbeat: test: in_progress (30s), claude-review: queued",
		"test: completed (pass)",
		"claude-review: in_progress",
		"claude-review: completed (pass)",
		"PR #422 CI passed",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWatchPRCIScriptPrintsFailedRunLogs(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "HEAD" ]]; then
	printf 'deadbeef\n'
	exit 0
fi

echo "unexpected git invocation: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"stale-sha"}
EOF
	exit 0
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

func TestWatchPRCIScriptFallbackLogsOnlyConcludedFailures(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	ghLog := filepath.Join(tempDir, "gh.log")
	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "HEAD" ]]; then
	printf 'deadbeef\n'
	exit 0
fi

echo "unexpected git invocation: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"stale-sha"}
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --json "* ]]; then
	cat <<'EOF'
[{"name":"go test ./...","bucket":"fail","state":"FAILURE","link":"","workflow":"CI"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / pending","url":"https://example.com/run/999","conclusion":"","status":"in_progress"},{"databaseId":1000,"workflowName":"CI","displayTitle":"ci / failed","url":"https://example.com/run/1000","conclusion":"failure","status":"completed"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "view" && "${3:-}" == "1000" && "${4:-}" == "--log-failed" ]]; then
	cat <<'EOF'
FAIL step output
--- FAIL: TestOnlyFailedRunIsFetched
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

	output := string(out)
	for _, want := range []string{
		"PR #422 CI failed",
		"ci / failed",
		"https://example.com/run/1000",
		"TestOnlyFailedRunIsFetched",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(output, "ci / pending") {
		t.Fatalf("output should not include in-progress fallback run:\n%s", out)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "run view 1000 --log-failed") {
		t.Fatalf("gh log = %q, want failed fallback run lookup", log)
	}
	if strings.Contains(log, "run view 999 --log-failed") {
		t.Fatalf("gh log = %q, should not fetch logs for in-progress fallback runs", log)
	}
}

func TestPushAndWatchCIScriptRunsPushBeforeWatching(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	gitLog := filepath.Join(tempDir, "git.log")
	ghLog := filepath.Join(tempDir, "gh.log")

	writeExecutable(t, filepath.Join(tempDir, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "rev-parse" && "${2:-}" == "HEAD" ]]; then
	printf 'deadbeef\n'
	exit 0
fi
printf '%s\n' "$*" >>"$FAKE_GIT_LOG"
exit 0
`)
	writeExecutable(t, filepath.Join(tempDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "${1:-}" == "pr" && "${2:-}" == "view" ]]; then
	cat <<'EOF'
{"number":422,"url":"https://example.com/pr/422","headRefName":"feat/ci-watch","headRefOid":"stale-sha"}
EOF
	exit 0
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "checks" && " $* " == *" --json "* ]]; then
	cat <<'EOF'
[{"name":"test","bucket":"pass","state":"SUCCESS","link":"https://example.com/run/999","workflow":"CI","startedAt":"2024-01-01T00:00:00Z","completedAt":"2024-01-01T00:00:05Z"}]
EOF
	exit 0
fi

if [[ "${1:-}" == "run" && "${2:-}" == "list" ]]; then
	cat <<'EOF'
[{"databaseId":999,"workflowName":"CI","displayTitle":"ci / test","url":"https://example.com/run/999","conclusion":"","status":"in_progress"}]
EOF
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
	if !strings.Contains(string(ghBytes), "pr checks 422 --required --json") {
		t.Fatalf("gh log = %q, want required-check polling", ghBytes)
	}
}

func ciWatchScriptEnv(t *testing.T, fakeBinDir string, extra ...string) []string {
	t.Helper()

	env := append([]string{}, hermeticMainEnv()...)
	env = upsertCIWatchEnv(env, "PATH", fakeBinDir+string(os.PathListSeparator)+ciWatchEnvValue(env, "PATH"))
	env = upsertCIWatchEnv(env, "AMUX_PR_CHECK_INTERVAL", "0")
	env = upsertCIWatchEnv(env, "AMUX_PR_RUN_DISCOVERY_TIMEOUT", "1")
	env = upsertCIWatchEnv(env, "AMUX_PR_RUN_DISCOVERY_INTERVAL", "0")
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

func installFakeClock(t *testing.T, dir string, start int) string {
	t.Helper()

	nowFile := filepath.Join(dir, "now")
	if err := os.WriteFile(nowFile, []byte(strconv.Itoa(start)+"\n"), 0644); err != nil {
		t.Fatalf("write fake clock state: %v", err)
	}

	writeExecutable(t, filepath.Join(dir, "date"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "+%s" ]]; then
	cat "$FAKE_NOW_FILE"
	exit 0
fi

exec /bin/date "$@"
`)
	writeExecutable(t, filepath.Join(dir, "sleep"), `#!/usr/bin/env bash
set -euo pipefail

delay="${1:-0}"
current="$(cat "$FAKE_NOW_FILE")"
printf '%s\n' $((current + delay)) >"$FAKE_NOW_FILE"
`)

	return nowFile
}
