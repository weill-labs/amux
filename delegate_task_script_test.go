package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegateTaskScriptSendsTaskAndWaitsForAcceptance(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	sendMarkerPath := filepath.Join(tempDir, "send.marker")
	writeFakeDelegateTaskAmux(t, tempDir)

	out, exitCode := runDelegateTaskScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"FAKE_AMUX_SEND_MARKER=" + sendMarkerPath,
		"FAKE_AMUX_EVENT_MODE=success",
	}, "pane-47", "--timeout", "1s", "Fix the black screen bug")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "pane-47 accepted task") {
		t.Fatalf("stdout = %q, want acceptance message", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	if !strings.Contains(log, "send-keys pane-47 Fix the black screen bug Enter") {
		t.Fatalf("amux log = %q, want send-keys call", log)
	}
	if strings.Contains(log, "capture pane-47") {
		t.Fatalf("amux log = %q, did not expect capture on success", log)
	}
}

func TestDelegateTaskScriptCapturesPaneWhenWorkerNeverAccepts(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	sendMarkerPath := filepath.Join(tempDir, "send.marker")
	writeFakeDelegateTaskAmux(t, tempDir)

	out, exitCode := runDelegateTaskScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"FAKE_AMUX_SEND_MARKER=" + sendMarkerPath,
		"FAKE_AMUX_EVENT_MODE=timeout",
		"FAKE_AMUX_CAPTURE_TEXT=worker prompt is still idle",
	}, "pane-47", "--timeout", "100ms", "Fix the black screen bug")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "pane-47 appears stuck") {
		t.Fatalf("output = %q, want stuck-state message", out)
	}
	if !strings.Contains(out, "expected output within 100ms") {
		t.Fatalf("output = %q, want output-timeout message", out)
	}
	if !strings.Contains(out, "worker prompt is still idle") {
		t.Fatalf("output = %q, want captured pane contents", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	if !strings.Contains(log, "capture pane-47") {
		t.Fatalf("amux log = %q, want capture call", log)
	}
	if strings.Contains(log, "meta set pane-47 issue=") {
		t.Fatalf("amux log = %q, did not expect issue metadata on timeout", log)
	}
}

func TestDelegateTaskScriptSetsIssueMetadataAfterAcceptance(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	sendMarkerPath := filepath.Join(tempDir, "send.marker")
	writeFakeDelegateTaskAmux(t, tempDir)

	out, exitCode := runDelegateTaskScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"FAKE_AMUX_SEND_MARKER=" + sendMarkerPath,
		"FAKE_AMUX_EVENT_MODE=success",
	}, "pane-47", "--issue", "LAB-468", "--timeout", "1s", "Fix the black screen bug")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	sendIndex := strings.Index(log, "send-keys pane-47 Fix the black screen bug Enter")
	addMetaIndex := strings.Index(log, "meta set pane-47 issue=LAB-468")
	if sendIndex == -1 || addMetaIndex == -1 {
		t.Fatalf("amux log = %q, want send-keys then meta set", log)
	}
	if addMetaIndex < sendIndex {
		t.Fatalf("amux log = %q, want issue metadata after acceptance", log)
	}
}

func TestDelegateTaskScriptRejectsInvalidTimeout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	sendMarkerPath := filepath.Join(tempDir, "send.marker")
	writeFakeDelegateTaskAmux(t, tempDir)

	out, exitCode := runDelegateTaskScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"FAKE_AMUX_SEND_MARKER=" + sendMarkerPath,
		"FAKE_AMUX_EVENT_MODE=success",
	}, "pane-47", "--timeout", "later", "Fix the black screen bug")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
	}
	if !strings.Contains(out, "invalid duration: later") {
		t.Fatalf("output = %q, want invalid duration message", out)
	}
	if strings.Contains(out, "appears stuck") {
		t.Fatalf("output = %q, did not expect stuck-state fallback", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("read fake amux log: %v", err)
		}
		return
	}
	if strings.Contains(string(got), "send-keys") {
		t.Fatalf("amux log = %q, did not expect send-keys for invalid timeout", got)
	}
}

func TestDelegateTaskScriptRequiresPaneArgument(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	out, exitCode := runDelegateTaskScript(t, tempDir, nil)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
	}
	if !strings.Contains(out, "usage: scripts/delegate-task.sh") {
		t.Fatalf("output = %q, want usage text", out)
	}
}

func TestDelegateTaskScriptRequiresTaskArgument(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	out, exitCode := runDelegateTaskScript(t, tempDir, nil, "pane-47")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, out)
	}
	if !strings.Contains(out, "usage: scripts/delegate-task.sh") {
		t.Fatalf("output = %q, want usage text", out)
	}
}

func runDelegateTaskScript(t *testing.T, tempDir string, extraEnv []string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", append([]string{".agents/skills/amux/scripts/delegate-task.sh"}, args...)...)
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run delegate-task script: %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func writeFakeDelegateTaskAmux(t *testing.T, tempDir string) {
	t.Helper()

	amuxPath := filepath.Join(tempDir, "amux")
	script := `#!/usr/bin/env bash
set -euo pipefail

log_call() {
    printf '%s' "$1" >>"$FAKE_AMUX_LOG"
    shift
    for arg in "$@"; do
        printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
    done
    printf '\n' >>"$FAKE_AMUX_LOG"
}

cmd="${1:-}"
shift || true

case "$cmd" in
    events)
        printf '{"type":"layout","pane_name":"pane-47"}\n'
        if [[ "${FAKE_AMUX_EVENT_MODE:-success}" == "success" ]]; then
            deadline=$((SECONDS + 5))
            while [[ ! -f "$FAKE_AMUX_SEND_MARKER" && $SECONDS -lt $deadline ]]; do
                sleep 0.01
            done
            printf '{"type":"output","pane_name":"pane-47"}\n'
            exit 0
        fi
        sleep 0.5
        exit 0
        ;;
    send-keys)
        : >"$FAKE_AMUX_SEND_MARKER"
        log_call "$cmd" "$@"
        ;;
    capture)
        log_call "$cmd" "$@"
        printf '%s\n' "${FAKE_AMUX_CAPTURE_TEXT:-pane capture}"
        ;;
    meta)
        log_call "$cmd" "$@"
        ;;
    *)
        log_call "$cmd" "$@"
        ;;
esac
`
	if err := os.WriteFile(amuxPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}
}
