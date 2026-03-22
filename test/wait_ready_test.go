package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendKeysWaitReadyContinuesCodexTrustDialog(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-wait-ready-%s.sh", h.session))
	script := `#!/bin/bash
set -euo pipefail
printf '\033[2J\033[H\033[?25l'
printf '> You are in %s\n\n' "$PWD"
printf '  Do you trust the contents of this directory? Working with untrusted contents\n'
printf '  comes with higher risk of prompt injection.\n\n'
printf '› 1. Yes, continue\n'
printf '  2. No, quit\n\n'
printf '  Press enter to continue'
IFS= read -r _
printf '\033[?25h\033[2J\033[H'
printf '\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n'
printf '> Ready'
IFS= read -r task
printf '\nTASK:%s\n' "$task"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("writing wait-ready script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "Do you trust the contents of this directory?")

	if out := h.runCmd("wait-ready", "pane-1", "--timeout", "100ms"); !strings.Contains(out, "Codex trust dialog is blocking input in pane-1") {
		t.Fatalf("wait-ready should report trust dialog blocker, got:\n%s", out)
	}

	out := h.runCmd("send-keys", "pane-1", "--wait-ready", "--continue-known-dialogs", "ship it", "Enter")
	if strings.TrimSpace(out) != "Sent 8 bytes to pane-1" {
		t.Fatalf("send-keys --wait-ready output = %q", out)
	}

	h.waitFor("pane-1", "TASK:ship it")
}
