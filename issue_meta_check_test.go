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
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env,
		"PATH="+tempDir+":"+os.Getenv("PATH"),
		"AMUX_PANE=7",
		"AMUX_SESSION=test-session",
	)
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
	if !strings.Contains(string(out), `amux issue LAB-XXX`) {
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
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env,
		"PATH="+tempDir+":"+os.Getenv("PATH"),
		"AMUX_PANE=7",
		"AMUX_SESSION=test-session",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success when issue metadata exists: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected no output, got:\n%s", out)
	}
}
