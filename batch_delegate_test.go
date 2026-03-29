package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchDelegateScriptDispatchesManifestAndSummarizesResults(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("look up jq: %v", err)
	}

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/usr/bin/env bash
set -euo pipefail

log_call() {
    {
        printf '%s' "$1"
        shift
        for arg in "$@"; do
            printf ' %s' "$arg"
        done
        printf '\n'
    } >>"$FAKE_AMUX_LOG"
}

case "${1:-}" in
    add-meta)
        pane="${2:-}"
        case " ${FAKE_AMUX_FAIL_ADD_META_PANES:-} " in
            *" ${pane} "*) log_call "$@"; exit 1 ;;
        esac
        log_call "$@"
        ;;
    send-keys)
        pane="${2:-}"
        case " ${FAKE_AMUX_FAIL_SEND_KEYS_PANES:-} " in
            *" ${pane} "*) log_call "$@"; exit 1 ;;
        esac
        log_call "$@"
        touch "$FAKE_AMUX_SENT_DIR/$pane"
        ;;
    events)
        pane=""
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --pane)
                    pane="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done
        log_call events --pane "$pane"
        printf '{"type":"idle","pane_name":"%s"}\n' "$pane"
        case " ${FAKE_AMUX_OUTPUT_PANES:-} " in
            *" ${pane} "*)
                if [[ -e "$FAKE_AMUX_SENT_DIR/$pane" ]]; then
                    exit 0
                fi
                for _ in {1..50}; do
                    if [[ -e "$FAKE_AMUX_SENT_DIR/$pane" ]]; then
                        printf '{"type":"output","pane_name":"%s"}\n' "$pane"
                        exit 0
                    fi
                    sleep 0.01
                done
                ;;
            *) sleep "${FAKE_AMUX_EVENT_SLEEP:-0.2}" ;;
        esac
        ;;
    *)
        log_call "$@"
        ;;
esac
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	manifestPath := filepath.Join(tempDir, "manifest.json")
	sentDir := filepath.Join(tempDir, "sent")
	if err := os.Mkdir(sentDir, 0755); err != nil {
		t.Fatalf("mkdir sent dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`[
  {"pane":"pane-47","issue":"LAB-468","task":"Fix black screen"},
  {"pane":"pane-48","issue":"LAB-469","task":"Add vt-idle logging"}
]`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	out, exitCode := runBatchDelegateScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"FAKE_AMUX_OUTPUT_PANES=pane-47",
		"FAKE_AMUX_SENT_DIR=" + sentDir,
		"AMUX_BATCH_ACCEPT_TIMEOUT=0.05",
		"AMUX_BATCH_READY_TIMEOUT=7s",
	}, manifestPath)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "pane-47") || !strings.Contains(out, "SUCCESS") {
		t.Fatalf("output missing pane-47 success row:\n%s", out)
	}
	if !strings.Contains(out, "pane-48") || !strings.Contains(out, "FAILURE") {
		t.Fatalf("output missing pane-48 failure row:\n%s", out)
	}
	if !strings.Contains(out, "acceptance timeout") {
		t.Fatalf("output missing acceptance-timeout detail:\n%s", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := string(got)
	if !strings.Contains(log, "add-meta pane-47 issue=LAB-468") {
		t.Fatalf("amux log missing issue metadata write:\n%s", log)
	}
	if !strings.Contains(log, "send-keys pane-47 --wait ready --timeout 7s Fix black screen Enter") {
		t.Fatalf("amux log missing send-keys dispatch:\n%s", log)
	}
	if !strings.Contains(log, "events --pane pane-47") || !strings.Contains(log, "events --pane pane-48") {
		t.Fatalf("amux log missing output-event acceptance checks:\n%s", log)
	}
}

func TestBatchDelegateScriptSkipsDispatchWhenIssueMetadataFails(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("look up jq: %v", err)
	}

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/usr/bin/env bash
set -euo pipefail

log_call() {
    {
        printf '%s' "$1"
        shift
        for arg in "$@"; do
            printf ' %s' "$arg"
        done
        printf '\n'
    } >>"$FAKE_AMUX_LOG"
}

log_call "$@"

if [[ "${1:-}" == "add-meta" && "${2:-}" == "pane-49" ]]; then
    exit 1
fi
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`[
  {"pane":"pane-49","issue":"LAB-470","task":"Fix worker attach"}
]`), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	out, exitCode := runBatchDelegateScript(t, tempDir, []string{
		"FAKE_AMUX_LOG=" + logPath,
		"AMUX_BATCH_ACCEPT_TIMEOUT=0.05",
	}, manifestPath)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "pane-49") || !strings.Contains(out, "FAILURE") {
		t.Fatalf("output missing pane-49 failure row:\n%s", out)
	}
	if !strings.Contains(out, "set issue metadata failed") {
		t.Fatalf("output missing metadata failure detail:\n%s", out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := string(got)
	if strings.Contains(log, "send-keys pane-49") {
		t.Fatalf("send-keys should not run after add-meta failure:\n%s", log)
	}
}

func runBatchDelegateScript(t *testing.T, tempDir string, extraEnv []string, manifestPath string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", "scripts/batch-delegate.sh", manifestPath)
	cmd.Dir = "."
	cmd.Env = batchDelegateScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run batch-delegate: %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func batchDelegateScriptEnv(tempDir string, extra ...string) []string {
	env := envWithHome(tempDir)
	pathValue := tempDir
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + tempDir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "PATH="+pathValue)
	}
	return append(env, extra...)
}
