package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPaneIssueMetaWarnsWhenIssueMetadataMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
cat <<'EOF'
{"meta":{"issues":[]}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/check-pane-issue-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when issue metadata is missing\n%s", out)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), `scripts/set-pane-issue.sh LAB-XXX`) {
		t.Fatalf("missing helper guidance in output:\n%s", out)
	}
}

func TestCheckPaneIssueMetaPassesWhenIssueMetadataExists(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
cat <<'EOF'
{"meta":{"issues":["LAB-445"]}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/check-pane-issue-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success when issue metadata exists: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected no output, got:\n%s", out)
	}
}

func TestSetPaneIssueScriptUsesActorPaneByDefault(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
printf '%s' "$1" >"$FAKE_AMUX_LOG"
shift
for arg in "$@"; do
    printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
done
printf '\n' >>"$FAKE_AMUX_LOG"
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	cmd := exec.Command("bash", "scripts/set-pane-issue.sh", "LAB-445")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_AMUX_LOG="+logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.TrimSpace(string(got)) != "add-meta 7 issue=LAB-445" {
		t.Fatalf("amux args = %q, want %q", got, "add-meta 7 issue=LAB-445")
	}
}

func TestSyncPanePRMetaScriptAddsCurrentIssueAndPR(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "amux.log")
	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"meta":{"issues":["LAB-445"]}}
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
printf '422\n'
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	cmd := exec.Command("bash", "scripts/sync-pane-pr-meta.sh")
	cmd.Dir = "."
	cmd.Env = issueMetaScriptEnv(tempDir, "FAKE_AMUX_LOG="+logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	if strings.TrimSpace(string(got)) != "add-meta 7 pr=422 issue=LAB-445" {
		t.Fatalf("amux args = %q, want %q", got, "add-meta 7 pr=422 issue=LAB-445")
	}
}

func issueMetaScriptEnv(tempDir string, extra ...string) []string {
	env := append([]string{}, hermeticMainEnv()...)
	env = upsertIssueMetaEnv(env, "PATH", tempDir+string(os.PathListSeparator)+issueMetaEnvValue(env, "PATH"))
	env = upsertIssueMetaEnv(env, "AMUX_PANE", "7")
	env = upsertIssueMetaEnv(env, "AMUX_SESSION", "test-session")
	return append(env, extra...)
}

func issueMetaEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func upsertIssueMetaEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
