package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerStatusScriptListsWorkersByMetadataAndName(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 7     pane-7               local           feature/pr-422                 main       worker       prs=[422] issues=[LAB-517]
 8     logs-worker          local           feature/logs                   main       build
 9     pane-9               local           feature/pr-500                 main       worker       prs=[500] issues=[LAB-500]
10     pane-10              local           feature/other                  main       build
EOF
exit 0
fi

if [ "$1" = "capture" ]; then
case "$4" in
    pane-7)
cat <<'EOF'
{
  "name": "pane-7",
  "task": "worker",
  "meta": {
    "tracked_issues": [{"id": "LAB-517", "status": "active"}],
    "tracked_prs": [{"number": 422, "status": "active"}]
  },
  "idle": false,
  "child_pids": [101],
  "content": ["running tests", "streaming output"]
}
EOF
exit 0
;;
    logs-worker)
cat <<'EOF'
{
  "name": "logs-worker",
  "task": "build",
  "meta": {
    "tracked_issues": [{"id": "LAB-600", "status": "active"}],
    "tracked_prs": [{"number": 600, "status": "active"}]
  },
  "idle": true,
  "child_pids": [],
  "content": ["", "worker finished"]
}
EOF
exit 0
;;
    pane-9)
cat <<'EOF'
{
  "name": "pane-9",
  "task": "worker",
  "meta": {
    "tracked_issues": [{"id": "LAB-500", "status": "active"}],
    "tracked_prs": [{"number": 500, "status": "active"}]
  },
  "idle": false,
  "child_pids": [202],
  "content": [
    "  Do you trust the contents of this directory? Working with untrusted contents",
    "  higher risk of prompt injection."
  ]
}
EOF
exit 0
;;
esac
fi

if [ "$1" = "wait" ] && [ "$2" = "vt-idle" ]; then
case "$3" in
    logs-worker|pane-9)
        printf 'vt-idle\n'
        exit 0
        ;;
    *)
        exit 1
        ;;
esac
fi

exit 1
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	out, exitCode := runWorkerStatusScript(t, tempDir)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}

	for _, want := range []string{"PANE", "ISSUE", "STATE", "PR", "LAST OUTPUT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing header %q:\n%s", want, out)
		}
	}

	if !strings.Contains(out, "pane-7") || !strings.Contains(out, "LAB-517") || !strings.Contains(out, "busy") || !strings.Contains(out, "422") || !strings.Contains(out, "streaming output") {
		t.Fatalf("output missing metadata-worker row:\n%s", out)
	}
	if !strings.Contains(out, "logs-worker") || !strings.Contains(out, "LAB-600") || !strings.Contains(out, "idle") || !strings.Contains(out, "600") || !strings.Contains(out, "worker finished") {
		t.Fatalf("output missing name-convention worker row:\n%s", out)
	}
	if !strings.Contains(out, "pane-9") || !strings.Contains(out, "stuck") || !strings.Contains(out, "500") || !strings.Contains(out, "higher risk of prompt injection.") {
		t.Fatalf("output missing stuck worker row:\n%s", out)
	}
	if strings.Contains(out, "pane-10") {
		t.Fatalf("output should skip non-worker panes:\n%s", out)
	}
}

func TestWorkerStatusScriptPrintsHeaderWhenNoWorkersMatch(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
cat <<'EOF'
PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
11     pane-11              local           feature/other                  main       build
EOF
exit 0
fi

exit 1
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	out, exitCode := runWorkerStatusScript(t, tempDir)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if !strings.Contains(out, "PANE") || !strings.Contains(out, "LAST OUTPUT") {
		t.Fatalf("output missing table header:\n%s", out)
	}
	if strings.Contains(out, "pane-11") {
		t.Fatalf("output should not include non-worker panes:\n%s", out)
	}
}

func runWorkerStatusScript(t *testing.T, tempDir string, extraEnv ...string) (string, int) {
	t.Helper()

	cmd := exec.Command("bash", "scripts/worker-status.sh")
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
