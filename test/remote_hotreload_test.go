package test

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestRemotePaneReconnectsAfterReload verifies that a remote proxy pane
// reconnects and remains functional after a server hot-reload.
// Requires SSH to localhost — skips if unavailable.
func TestRemotePaneReconnectsAfterReload(t *testing.T) {
	t.Parallel()

	user := os.Getenv("USER")
	if user == "" {
		t.Skip("USER env not set")
	}

	h := newAmuxHarnessWithConfig(t, `
[hosts.test-remote]
type = "remote"
user = "`+user+`"
address = "127.0.0.1"
`)

	// Create a remote pane — skip if SSH auth fails
	out := h.runCmd("split", "--host", "test-remote")
	if strings.Contains(out, "unable to authenticate") ||
		strings.Contains(out, "connection refused") ||
		strings.Contains(out, "no SSH auth") {
		t.Skip("SSH to localhost not available")
	}
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Skipf("remote split failed: %s", strings.TrimSpace(out))
	}

	// Verify remote pane exists and is connected
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "test-remote" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "test-remote")
	}

	// Send a marker to the remote pane before reload
	h.runCmd("focus", "pane-2")
	h.runCmd("send-keys", "pane-2", "echo BEFORE_RELOAD", "Enter")
	beforeOut := h.runCmd("wait-for", "pane-2", "BEFORE_RELOAD", "--timeout", "5s")
	if strings.Contains(beforeOut, "timeout") {
		t.Fatalf("BEFORE_RELOAD not seen in pane-2: %s", beforeOut)
	}

	// Trigger server hot-reload
	h.runCmd("reload-server")

	// Wait for the session to recover
	if !h.waitFor("[pane-", 10*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	// Wait for both panes to be visible
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 10*time.Second) {
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", h.captureOuter())
	}

	// Verify the remote pane is still listed with the correct host
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-remote") {
		t.Errorf("remote pane should still show test-remote host after reload, got:\n%s", listOut)
	}

	// Verify the remote pane is functional: send a command and check output
	h.runCmd("send-keys", "pane-2", "echo AFTER_RELOAD", "Enter")
	afterOut := h.runCmd("wait-for", "pane-2", "AFTER_RELOAD", "--timeout", "10s")
	if strings.Contains(afterOut, "timeout") {
		t.Errorf("remote pane should accept input after reload, wait-for output: %s\nlist:\n%s",
			strings.TrimSpace(afterOut), h.runCmd("list"))
	}
}
