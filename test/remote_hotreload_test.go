package test

import (
	"strings"
	"testing"
)

// TestRemotePaneViaSSH verifies that a remote proxy pane can be created
// over a real SSH connection (in-process test server) and is functional.
func TestRemotePaneViaSSH(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	// Create a remote pane
	out := h.runCmd("split", "--host", "test-remote")
	t.Logf("split --host test-remote: %s", strings.TrimSpace(out))
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}

	// Verify remote pane exists with correct metadata
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "test-remote" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "test-remote")
	}

	// Verify remote pane is functional (shell is running on localhost via SSH)
	h.sendKeys("pane-2", "echo REMOTE_SHELL_OK", "Enter")
	out = h.runCmd("wait-for", "pane-2", "REMOTE_SHELL_OK", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should accept input: %s", out)
	}

	// Remote shells should also see TERM=amux and a resolvable amux terminfo entry.
	h.sendKeys("pane-2", "echo TERM=$TERM; infocmp amux >/dev/null && echo REMOTE_TERMINFO_OK", "Enter")
	out = h.runCmd("wait-for", "pane-2", "TERM=amux", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should run with TERM=amux: %s", out)
	}
	out = h.runCmd("wait-for", "pane-2", "REMOTE_TERMINFO_OK", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should resolve amux terminfo: %s", out)
	}

	// Verify host metadata is visible in list command
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-remote") {
		t.Errorf("list should show test-remote, got:\n%s", listOut)
	}
}
