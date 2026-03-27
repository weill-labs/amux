package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorkerCIScriptNotifiesIdleOwnerForFailingChecks(t *testing.T) {
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
cat <<'EOF'
[{"number":422,"title":"Fix flaky restart","mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"test","conclusion":"FAILURE"}]}]
EOF
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runWorkerCICheck(t, tempDir, "FAKE_AMUX_LOG="+logPath)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, `owner=pane-7`) {
		t.Fatalf("output missing pane owner:\n%s", out)
	}
	if !strings.Contains(out, `reason="failing checks: test"`) {
		t.Fatalf("output missing failing-check reason:\n%s", out)
	}
	if !strings.Contains(out, `notify=sent`) {
		t.Fatalf("output missing notify=sent:\n%s", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	if !strings.Contains(log, "send-keys pane-7 PR #422 has failing CI (test). Fix and push. Enter") {
		t.Fatalf("amux log = %q, want send-keys notification", log)
	}
}

func TestCheckWorkerCIScriptReportsBusyOwnerForMergeConflict(t *testing.T) {
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
{"idle":false,"current_command":"go test ./..."}
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
cat <<'EOF'
[{"number":77,"title":"Rebase me","mergeable":"CONFLICTING","statusCheckRollup":[]}]
EOF
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runWorkerCICheck(t, tempDir, "FAKE_AMUX_LOG="+logPath)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, `owner=pane-3`) {
		t.Fatalf("output missing pane owner:\n%s", out)
	}
	if !strings.Contains(out, `state=busy(go test ./...)`) {
		t.Fatalf("output missing busy state:\n%s", out)
	}
	if !strings.Contains(out, `reason="merge conflict"`) {
		t.Fatalf("output missing merge-conflict reason:\n%s", out)
	}
	if !strings.Contains(out, `notify=skipped-busy`) {
		t.Fatalf("output missing notify=skipped-busy:\n%s", out)
	}

	if got, err := os.ReadFile(logPath); err == nil && strings.TrimSpace(string(got)) != "" {
		t.Fatalf("expected no send-keys call, got log:\n%s", got)
	}
}

func TestCheckWorkerCIScriptReportsOrphanedPR(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 2     pane-2               local           feature/other                  main       worker       prs=[12]
EOF
exit 0
fi
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"idle":true,"current_command":"bash"}
EOF
exit 0
fi
exit 0
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	ghPath := filepath.Join(tempDir, "gh")
	if err := os.WriteFile(ghPath, []byte(`#!/bin/sh
cat <<'EOF'
[{"number":99,"title":"Missing owner","mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"codecov/patch","conclusion":"FAILURE"}]}]
EOF
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runWorkerCICheck(t, tempDir)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, `owner=orphaned`) {
		t.Fatalf("output missing orphaned owner:\n%s", out)
	}
	if !strings.Contains(out, `notify=skipped-orphaned`) {
		t.Fatalf("output missing skipped-orphaned state:\n%s", out)
	}
}

func TestCheckWorkerCIScriptWaitsForAckWhenRequested(t *testing.T) {
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
cat <<'EOF'
[{"number":500,"title":"Need ack","mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"test","conclusion":"FAILURE"}]}]
EOF
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runWorkerCICheck(t, tempDir, "FAKE_AMUX_LOG="+logPath, "--wait")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, `ack=confirmed`) {
		t.Fatalf("output missing ack confirmation:\n%s", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := string(got)
	if !strings.Contains(log, "wait content pane-5 Working --timeout 15s") {
		t.Fatalf("amux log = %q, want wait content call", log)
	}
}

func TestCheckWorkerCIScriptSucceedsWhenNoProblemPRsExist(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
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
exit 0
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	ghPath := filepath.Join(tempDir, "gh")
	if err := os.WriteFile(ghPath, []byte(`#!/bin/sh
cat <<'EOF'
[{"number":500,"title":"Green PR","mergeable":"MERGEABLE","statusCheckRollup":[{"__typename":"CheckRun","name":"test","conclusion":"SUCCESS"}]}]
EOF
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	out, exitCode := runWorkerCICheck(t, tempDir)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "No open PRs with failing CI or merge conflicts.") {
		t.Fatalf("output = %q, want no-problems message", out)
	}
}

func runWorkerCICheck(t *testing.T, tempDir string, extraEnvOrArgs ...string) (string, int) {
	t.Helper()

	var extraEnv []string
	var extraArgs []string
	for _, item := range extraEnvOrArgs {
		if strings.Contains(item, "=") {
			extraEnv = append(extraEnv, item)
			continue
		}
		extraArgs = append(extraArgs, item)
	}

	args := append([]string{"scripts/check-worker-ci.sh"}, extraArgs...)
	cmd := exec.Command("bash", args...)
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
