package test

import (
	"strings"
	"testing"
	"time"
)

// TestRemotePaneReconnectsAfterReload verifies that a remote proxy pane
// reconnects and remains functional after a server hot-reload.
func TestRemotePaneReconnectsAfterReload(t *testing.T) {
	t.Parallel()

	keyFile, cleanup := setupTestSSHKey(t)
	defer cleanup()

	h := newAmuxHarnessWithConfig(t, remoteLocalhostConfig(keyFile))

	// Create a remote pane
	out := h.runCmd("split", "--host", "test-remote")
	t.Logf("split --host test-remote: %s", strings.TrimSpace(out))
	if strings.Contains(out, "unable to authenticate") || strings.Contains(out, "no SSH auth") {
		t.Skip("SSH auth to localhost failed (Go crypto/ssh can't use macOS Keychain agent)")
	}
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}

	// Verify remote pane exists
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "test-remote" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "test-remote")
	}

	// Send a marker before reload
	h.runCmd("send-keys", "pane-2", "echo BEFORE_RELOAD", "Enter")
	beforeOut := h.runCmd("wait-for", "pane-2", "BEFORE_RELOAD", "--timeout", "5s")
	if strings.Contains(beforeOut, "timeout") {
		t.Fatalf("BEFORE_RELOAD not seen: %s", beforeOut)
	}

	// Trigger server hot-reload
	h.runCmd("reload-server")

	// Wait for session recovery
	if !h.waitFor("[pane-", 10*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 10*time.Second) {
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", h.captureOuter())
	}

	// Verify remote pane is still listed with correct host
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-remote") {
		t.Errorf("remote pane should show test-remote after reload, got:\n%s", listOut)
	}

	// Verify remote pane is functional post-reload
	h.runCmd("send-keys", "pane-2", "echo AFTER_RELOAD", "Enter")
	afterOut := h.runCmd("wait-for", "pane-2", "AFTER_RELOAD", "--timeout", "10s")
	if strings.Contains(afterOut, "timeout") {
		t.Errorf("remote pane should accept input after reload: %s\nlist:\n%s",
			strings.TrimSpace(afterOut), h.runCmd("list"))
	}
}
