package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type recoverWorkerFixture struct {
	tempDir   string
	logPath   string
	stagePath string
}

func TestRecoverWorkerScriptRecoversStuckWorker(t *testing.T) {
	t.Parallel()

	fixture := newRecoverWorkerFixture(t)

	out, exitCode := runRecoverWorkerScript(t, fixture.tempDir,
		"FAKE_AMUX_LOG="+fixture.logPath,
		"FAKE_STAGE_FILE="+fixture.stagePath,
	)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "Recovered pane-68") {
		t.Fatalf("output missing recovery confirmation:\n%s", out)
	}

	got, err := os.ReadFile(fixture.logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	assertRecoverWorkerLogSequence(t, string(got), []string{
		"wait idle pane-68 --settle 2s --timeout 20s",
		"capture --format json pane-68",
		"send-keys pane-68 Escape",
		"wait idle pane-68 --settle 2s --timeout 20s",
		"send-keys pane-68 /exit Enter",
		"wait idle pane-68 --settle 2s --timeout 20s",
		"send-keys pane-68 codex --yolo resume Enter",
		"wait idle pane-68 --settle 2s --timeout 20s",
		"send-keys pane-68 Enter",
		"wait idle pane-68 --settle 2s --timeout 20s",
		"capture --format json pane-68",
		"send-keys pane-68 . Enter",
		"wait idle pane-68 --settle 2s --timeout 20s",
		"capture --format json pane-68",
	})
}

func TestRecoverWorkerScriptRejectsPaneWithoutKnownDialog(t *testing.T) {
	t.Parallel()

	fixture := newRecoverWorkerFixture(t)

	out, exitCode := runRecoverWorkerScript(t, fixture.tempDir,
		"FAKE_AMUX_LOG="+fixture.logPath,
		"FAKE_STAGE_FILE="+fixture.stagePath,
		"FAKE_INITIAL_STAGE=not_stuck",
	)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "does not look stuck") {
		t.Fatalf("output missing stuck-state failure:\n%s", out)
	}

	got, err := os.ReadFile(fixture.logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.Contains(string(got), "send-keys") {
		t.Fatalf("expected no recovery input for non-stuck pane, got log:\n%s", got)
	}
}

func TestRecoverWorkerScriptRejectsPaneWithoutChildProcesses(t *testing.T) {
	t.Parallel()

	fixture := newRecoverWorkerFixture(t)

	out, exitCode := runRecoverWorkerScript(t, fixture.tempDir,
		"FAKE_AMUX_LOG="+fixture.logPath,
		"FAKE_STAGE_FILE="+fixture.stagePath,
		"FAKE_INITIAL_STAGE=no_children",
	)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "has no foreground process") {
		t.Fatalf("output missing foreground-process failure:\n%s", out)
	}

	got, err := os.ReadFile(fixture.logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.Contains(string(got), "send-keys") {
		t.Fatalf("expected no recovery input without a foreground process, got log:\n%s", got)
	}
}

func TestRecoverWorkerScriptFailsWhenOutputDoesNotAdvance(t *testing.T) {
	t.Parallel()

	fixture := newRecoverWorkerFixture(t)

	out, exitCode := runRecoverWorkerScript(t, fixture.tempDir,
		"FAKE_AMUX_LOG="+fixture.logPath,
		"FAKE_STAGE_FILE="+fixture.stagePath,
		"FAKE_FINAL_STAGE=unchanged",
	)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "pane content did not change") {
		t.Fatalf("output missing no-progress failure:\n%s", out)
	}
}

func TestRecoverWorkerScriptFailsWhenDialogStillBlocksAfterRecovery(t *testing.T) {
	t.Parallel()

	fixture := newRecoverWorkerFixture(t)

	out, exitCode := runRecoverWorkerScript(t, fixture.tempDir,
		"FAKE_AMUX_LOG="+fixture.logPath,
		"FAKE_STAGE_FILE="+fixture.stagePath,
		"FAKE_FINAL_STAGE=still_blocked",
	)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, out)
	}
	if !strings.Contains(out, "still matches a blocking dialog") {
		t.Fatalf("output missing dialog-still-blocked failure:\n%s", out)
	}
}

func newRecoverWorkerFixture(t *testing.T) recoverWorkerFixture {
	t.Helper()

	tempDir := t.TempDir()
	writeFakeRecoverWorkerAmux(t, tempDir)
	return recoverWorkerFixture{
		tempDir:   tempDir,
		logPath:   filepath.Join(tempDir, "amux.log"),
		stagePath: filepath.Join(tempDir, "stage"),
	}
}

func runRecoverWorkerScript(t *testing.T, tempDir string, extraEnv ...string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", repoPath(t, ".agents/skills/amux/scripts/recover-worker.sh"), "pane-68")
	cmd.Dir = repoRoot(t)
	cmd.Env = issueMetaScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exitErr := mustExitError(t, err, out)
	return string(out), exitErr.ExitCode()
}

func writeFakeRecoverWorkerAmux(t *testing.T, tempDir string) {
	t.Helper()

	amuxPath := filepath.Join(tempDir, "amux")
	script := `#!/bin/sh
set -eu

log_call() {
    if [ -n "${FAKE_AMUX_LOG:-}" ]; then
        printf '%s' "$1" >>"$FAKE_AMUX_LOG"
        shift
        for arg in "$@"; do
            printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
        done
        printf '\n' >>"$FAKE_AMUX_LOG"
    fi
}

stage_file=${FAKE_STAGE_FILE:?missing FAKE_STAGE_FILE}
initial_stage=${FAKE_INITIAL_STAGE:-stuck}
final_stage=${FAKE_FINAL_STAGE:-recovered}

read_stage() {
    if [ -f "$stage_file" ]; then
        cat "$stage_file"
        return
    fi
    printf '%s' "$initial_stage"
}

write_stage() {
    printf '%s' "$1" >"$stage_file"
}

emit_capture() {
    case "$(read_stage)" in
        stuck)
            cat <<'EOF'
{"exited":false,"content":["Do you trust the contents of this directory?","Working with untrusted contents comes with higher risk of prompt injection.","Press enter to continue"]}
EOF
            ;;
        dismissed|exited)
            cat <<'EOF'
{"exited":false,"content":["Exited current dialog"]}
EOF
            ;;
        resume_picker)
            cat <<'EOF'
{"exited":false,"content":["Recent sessions:","  abc123  LAB-518 worker","Press Enter to continue"]}
EOF
            ;;
        ready_to_continue|unchanged)
            cat <<'EOF'
{"exited":false,"content":["Resumed rollout successfully from abc123","> ."]}
EOF
            ;;
        recovered)
            cat <<'EOF'
{"exited":false,"content":["Resumed rollout successfully from abc123","Working on it now","Inspecting tests..."]}
EOF
            ;;
        not_stuck)
            cat <<'EOF'
{"exited":true,"content":["build passed","shell prompt ready"]}
EOF
            ;;
        no_children)
            cat <<'EOF'
{"exited":true,"content":["Do you trust the contents of this directory?","Working with untrusted contents comes with higher risk of prompt injection.","Press enter to continue"]}
EOF
            ;;
        still_blocked)
            cat <<'EOF'
{"exited":false,"content":["Resumed rollout successfully from abc123","Press enter to continue"]}
EOF
            ;;
        *)
            echo "unknown fake stage: $(read_stage)" >&2
            exit 1
            ;;
    esac
}

cmd=${1:-}
shift || true
log_call "$cmd" "$@"

case "$cmd" in
    wait)
        exit 0
        ;;
    capture)
        emit_capture
        exit 0
        ;;
    send-keys)
        stage=$(read_stage)
        case "$*" in
            "pane-68 Escape")
                write_stage dismissed
                ;;
            "pane-68 /exit Enter")
                write_stage exited
                ;;
            "pane-68 codex --yolo resume Enter")
                write_stage resume_picker
                ;;
            "pane-68 Enter")
                if [ "$stage" = "resume_picker" ]; then
                    write_stage ready_to_continue
                fi
                ;;
            "pane-68 . Enter")
                if [ "$stage" = "ready_to_continue" ]; then
                    write_stage "$final_stage"
                fi
                ;;
        esac
        exit 0
        ;;
    *)
        echo "unexpected fake amux command: $cmd $*" >&2
        exit 1
        ;;
esac
`
	if err := os.WriteFile(amuxPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}
}

func assertRecoverWorkerLogSequence(t *testing.T, log string, want []string) {
	t.Helper()

	pos := 0
	for _, item := range want {
		idx := strings.Index(log[pos:], item)
		if idx == -1 {
			t.Fatalf("log missing %q after offset %d:\n%s", item, pos, log)
		}
		pos += idx + len(item)
	}
}
