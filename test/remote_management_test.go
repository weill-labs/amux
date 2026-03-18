package test

import (
	"strings"
	"testing"
)

// remoteHarness bundles a ServerHarness with SSH test infrastructure.
// Tests that need a remote host call splitRemotePane to create the connection.
func newRemoteHarness(t *testing.T) *ServerHarness {
	t.Helper()
	addr, keyFile := setupTestSSH(t)
	return newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))
}

// splitRemotePane creates a remote pane on "test-remote" and waits for the
// layout to update. Fails the test if the split command errors.
func splitRemotePane(t *testing.T, h *ServerHarness) {
	t.Helper()
	gen := h.generation()
	out := h.runCmd("split", "--host", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}
	h.waitLayout(gen)
}

// hostsShowsState checks that the `hosts` output contains the exact state
// string (e.g. "connected") without false-matching substrings like
// "disconnected" containing "connected".
func hostsShowsState(hostsOutput, state string) bool {
	for _, line := range strings.Split(hostsOutput, "\n") {
		if strings.Contains(line, "test-remote") {
			// The hosts table uses "%-15s" for state, so the state appears
			// as a whitespace-delimited field after the host name.
			fields := strings.Fields(line)
			for _, f := range fields {
				if f == state {
					return true
				}
			}
		}
	}
	return false
}

// TestHostsCommand verifies the `hosts` CLI command shows remote host status.
func TestHostsCommand(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)

	// Before connecting, hosts should show test-remote as disconnected
	out := h.runCmd("hosts")
	if !strings.Contains(out, "test-remote") {
		t.Fatalf("hosts should list test-remote, got:\n%s", out)
	}
	if !hostsShowsState(out, "disconnected") {
		t.Fatalf("hosts should show disconnected before any pane, got:\n%s", out)
	}

	// Split a remote pane to trigger connection
	splitRemotePane(t, h)

	// After connecting, hosts should show connected (not "disconnected")
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "connected") {
		t.Fatalf("hosts should show connected after split, got:\n%s", out)
	}
}

// TestDisconnectAndReconnect verifies the disconnect and reconnect CLI commands.
func TestDisconnectAndReconnect(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)
	splitRemotePane(t, h)

	// Verify pane is functional
	h.sendKeys("pane-2", "echo REMOTE_OK", "Enter")
	h.waitForTimeout("pane-2", "REMOTE_OK", "5s")

	// Disconnect
	out := h.runCmd("disconnect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("disconnect failed: %s", out)
	}
	if !strings.Contains(out, "Disconnected") {
		t.Errorf("disconnect should confirm, got: %s", out)
	}

	// Verify hosts shows disconnected
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "disconnected") {
		t.Errorf("hosts should show disconnected after disconnect, got:\n%s", out)
	}

	// Reconnect
	out = h.runCmd("reconnect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("reconnect failed: %s", out)
	}
	if !strings.Contains(out, "Reconnected") {
		t.Errorf("reconnect should confirm, got: %s", out)
	}

	// Verify hosts shows connected again
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "connected") {
		t.Errorf("hosts should show connected after reconnect, got:\n%s", out)
	}
}

// TestRemotePaneKill verifies that killing a remote pane cleans up mappings.
func TestRemotePaneKill(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)
	splitRemotePane(t, h)

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}

	// Kill the remote pane
	gen := h.generation()
	out := h.runCmd("kill", "pane-2")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("kill pane-2 failed: %s", out)
	}
	h.waitLayout(gen)

	c = h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("expected 1 pane after kill, got %d", len(c.Panes))
	}
}
