package test

import (
	"strings"
	"testing"
)

// TestSplitRemotePaneInheritsHost verifies that splitting a remote proxy
// pane without --host creates the new pane on the same remote host.
func TestSplitRemotePaneInheritsHost(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Inject a mock proxy pane for "gpu-server" (no SSH required)
	out := h.runCmd("_inject-proxy", "gpu-server")
	if !strings.Contains(out, "@gpu-server") {
		t.Fatalf("inject-proxy failed: %s", out)
	}

	// Verify: pane-1 (local) + pane-2 (proxy @gpu-server)
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "gpu-server" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "gpu-server")
	}

	// Focus the proxy pane, then split WITHOUT --host
	h.runCmd("focus", "pane-2")
	out = h.runCmd("split")

	// The split should try to create a remote pane on gpu-server.
	// It will fail (no RemoteManager or config) but mentioning "gpu-server"
	// in the output proves the host was inherited from the proxy pane.
	if !strings.Contains(out, "gpu-server") {
		t.Errorf("split on proxy pane should inherit host, got: %s", strings.TrimSpace(out))
	}
}
