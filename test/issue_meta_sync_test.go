package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncPaneMetaScriptRefreshesTrackedRefsViaSetKV(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	copyIssueMetaFixture(t, tempDir, "scripts/sync-pane-meta.sh")
	logPath := filepath.Join(tempDir, "amux.log")

	amuxPath := filepath.Join(tempDir, "amux")
	if err := os.WriteFile(amuxPath, []byte(`#!/bin/sh
if [ "$1" = "capture" ]; then
cat <<'EOF'
{"cwd":"/tmp/project","meta":{"tracked_prs":[{"number":422}],"tracked_issues":[{"id":"LAB-445"}]}}
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
printf '2026-03-28T12:34:56Z\n'
`), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	curlPath := filepath.Join(tempDir, "curl")
	if err := os.WriteFile(curlPath, []byte(`#!/bin/sh
cat <<'EOF'
{"data":{"issue":{"state":{"type":"completed"}}}}
EOF
`), 0755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}

	datePath := filepath.Join(tempDir, "date")
	if err := os.WriteFile(datePath, []byte(`#!/bin/sh
printf '2026-03-28T12:34:56Z\n'
`), 0755); err != nil {
		t.Fatalf("write fake date: %v", err)
	}

	cmd := exec.Command("bash", filepath.Join(tempDir, "scripts/sync-pane-meta.sh"))
	cmd.Dir = tempDir
	cmd.Env = issueMetaScriptEnv(tempDir,
		"FAKE_AMUX_LOG="+logPath,
		"LINEAR_API_KEY=test-linear-token",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success: %v\n%s", err, out)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake amux log: %v", err)
	}
	log := strings.TrimSpace(string(got))
	for _, want := range []string{"set-kv 7", `tracked_prs=[{"number":422,"status":"completed","checked_at":"2026-03-28T12:34:56Z"}]`, `tracked_issues=[{"id":"LAB-445","status":"completed","checked_at":"2026-03-28T12:34:56Z"}]`} {
		if !strings.Contains(log, want) {
			t.Fatalf("amux log missing %q:\n%s", want, log)
		}
	}
}
