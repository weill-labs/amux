package test

import (
	"strings"
	"testing"
)

// TestSplitRemotePaneInheritsHost verifies that splitting a remote proxy
// pane without --host creates the new pane on the same remote host.
func TestSplitRemotePaneInheritsHost(t *testing.T) {
	t.Parallel()

	keyFile, cleanup := setupTestSSHKey(t)
	defer cleanup()

	h := newServerHarnessWithConfig(t, remoteLocalhostConfig(keyFile))

	// Create a remote pane on test-remote (localhost)
	out := h.runCmd("split", "--host", "test-remote")
	t.Logf("split --host test-remote: %s", strings.TrimSpace(out))
	if strings.Contains(out, "unable to authenticate") || strings.Contains(out, "no SSH auth") {
		t.Skip("SSH auth to localhost failed (Go crypto/ssh can't use macOS Keychain agent)")
	}
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("split --host test-remote failed: %s", out)
	}

	// Verify: pane-1 (local) + pane-2 (remote)
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d\nlist: %s", len(c.Panes), h.runCmd("list"))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "test-remote" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "test-remote")
	}

	// Focus remote pane, then split WITHOUT --host
	h.runCmd("focus", "pane-2")
	out = h.runCmd("split")

	// New pane should inherit the remote host
	if !strings.Contains(out, "@test-remote") {
		t.Errorf("split on remote pane should create remote pane, got: %s", strings.TrimSpace(out))
	}

	c = h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes after second split, got %d", len(c.Panes))
	}
	p3 := h.jsonPane(c, "pane-3")
	if p3.Host != "test-remote" {
		t.Errorf("pane-3 host = %q, want %q (should inherit from active remote pane)", p3.Host, "test-remote")
	}
}
