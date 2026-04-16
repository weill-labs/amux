package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
11     pane-11              local           feature/pr-511                 main       worker       prs=[511]
12     pane-12              local           feature/pr-512                 main       worker       prs=[512]
13     pane-13              local           feature/pr-513                 main       worker       prs=[513]
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
  "exited": false,
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
  "exited": true,
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
  "exited": false,
  "content": [
    "  Do you trust the contents of this directory? Working with untrusted contents",
    "  higher risk of prompt injection."
  ]
}
EOF
exit 0
;;
    pane-10)
cat <<'EOF'
{
  "name": "pane-10",
  "task": "build",
  "meta": {
    "tracked_issues": [{"id": "LAB-700", "status": "active"}],
    "tracked_prs": [{"number": 700, "status": "active"}]
  },
  "idle": false,
  "exited": false,
  "content": ["not a worker"]
}
EOF
exit 0
;;
    pane-12)
cat <<'EOF'
{
  "error": {
    "code": "capture_unavailable",
    "message": "capture unavailable"
  }
}
EOF
exit 0
;;
    pane-13)
cat <<'EOF'
{
  "name": "pane-13",
  "task": "worker",
  "meta": {
    "tracked_issues": [{"id": "LAB-513", "status": "active"}],
    "tracked_prs": [{"number": 513, "status": "active"}]
  },
  "idle": false,
  "exited": true,
  "content": ["waiting quietly"]
}
EOF
exit 0
;;
esac
fi

if [ "$1" = "wait" ] && [ "$2" = "idle" ]; then
case "$3" in
    logs-worker|pane-9|pane-13)
        printf 'idle\n'
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

	assertWorkerStatusRow(t, out, "pane-7", "LAB-517", "busy", "422", "streaming output")
	assertWorkerStatusRow(t, out, "logs-worker", "LAB-600", "idle", "600", "worker finished")
	assertWorkerStatusRow(t, out, "pane-9", "LAB-500", "stuck", "500", "higher risk of prompt injection.")
	assertWorkerStatusRow(t, out, "pane-13", "LAB-513", "idle", "513", "waiting quietly")
	assertWorkerStatusRowAbsent(t, out, "pane-10")
	assertWorkerStatusRowAbsent(t, out, "pane-11")
	assertWorkerStatusRowAbsent(t, out, "pane-12")
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

	cmd := exec.Command("bash", repoPath(t, ".agents/skills/amux/scripts/worker-status.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = issueMetaScriptEnv(tempDir, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	exitErr := mustExitError(t, err, out)
	return string(out), exitErr.ExitCode()
}

func assertWorkerStatusRow(t *testing.T, out, pane, issue, state, pr, lastOutput string) {
	t.Helper()

	pattern := "(?m)^" +
		regexp.QuoteMeta(pane) +
		` +` +
		regexp.QuoteMeta(issue) +
		` +` +
		regexp.QuoteMeta(state) +
		` +` +
		regexp.QuoteMeta(pr) +
		` +` +
		regexp.QuoteMeta(lastOutput) +
		`$`
	if !regexp.MustCompile(pattern).MatchString(out) {
		t.Fatalf("output missing row %q/%q/%q/%q/%q:\n%s", pane, issue, state, pr, lastOutput, out)
	}
}

func assertWorkerStatusRowAbsent(t *testing.T, out, pane string) {
	t.Helper()

	pattern := "(?m)^" + regexp.QuoteMeta(pane) + ` +`
	if regexp.MustCompile(pattern).MatchString(out) {
		t.Fatalf("output should not include row for %q:\n%s", pane, out)
	}
}
