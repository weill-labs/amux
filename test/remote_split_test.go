package test

import (
	"os"
	"strings"
	"testing"
)

// TestSplitRemotePaneInheritsHost verifies that splitting a remote proxy
// pane without --host creates the new pane on the same remote host.
// Requires SSH to localhost — skips if unavailable (CI, no agent keys).
func TestSplitRemotePaneInheritsHost(t *testing.T) {
	t.Parallel()

	user := os.Getenv("USER")
	if user == "" {
		t.Skip("USER env not set")
	}

	h := newServerHarnessWithConfig(t, `
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
		t.Skip("SSH to localhost not available (no agent keys loaded)")
	}
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Skipf("remote split failed: %s", strings.TrimSpace(out))
	}

	// Verify: pane-1 (local) + pane-2 (remote)
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
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
