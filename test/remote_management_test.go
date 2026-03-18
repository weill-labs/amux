package test

import (
	"strings"
	"testing"
)

// TestHostsCommand verifies the `hosts` CLI command shows remote host status.
func TestHostsCommand(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	// Before connecting, hosts should show test-remote as disconnected
	out := h.runCmd("hosts")
	if !strings.Contains(out, "test-remote") {
		t.Fatalf("hosts should list test-remote, got:\n%s", out)
	}
	if !strings.Contains(out, "disconnected") {
		t.Fatalf("hosts should show disconnected before any pane, got:\n%s", out)
	}

	// Split a remote pane to trigger connection
	gen := h.generation()
	splitOut := h.runCmd("split", "--host", "test-remote")
	if strings.Contains(splitOut, "error") || strings.Contains(splitOut, "Error") {
		t.Fatalf("remote split failed: %s", splitOut)
	}
	h.waitLayout(gen)

	// After connecting, hosts should show connected
	out = h.runCmd("hosts")
	if !strings.Contains(out, "connected") {
		t.Fatalf("hosts should show connected after split, got:\n%s", out)
	}
}

// TestDisconnectAndReconnect verifies the disconnect and reconnect CLI commands.
func TestDisconnectAndReconnect(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	// Create a remote pane
	gen := h.generation()
	out := h.runCmd("split", "--host", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}
	h.waitLayout(gen)

	// Verify pane is functional
	h.sendKeys("pane-2", "echo REMOTE_OK", "Enter")
	h.waitForTimeout("pane-2", "REMOTE_OK", "5s")

	// Disconnect
	out = h.runCmd("disconnect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("disconnect failed: %s", out)
	}
	if !strings.Contains(out, "Disconnected") {
		t.Errorf("disconnect should confirm, got: %s", out)
	}

	// Verify hosts shows disconnected
	out = h.runCmd("hosts")
	if !strings.Contains(out, "disconnected") {
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
	if !strings.Contains(out, "connected") {
		t.Errorf("hosts should show connected after reconnect, got:\n%s", out)
	}
}

// TestRemotePaneKill verifies that killing a remote pane cleans up mappings.
func TestRemotePaneKill(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	// Create a remote pane
	gen := h.generation()
	out := h.runCmd("split", "--host", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}
	h.waitLayout(gen)

	// Verify 2 panes exist
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}

	// Kill the remote pane
	gen = h.generation()
	out = h.runCmd("kill", "pane-2")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("kill pane-2 failed: %s", out)
	}
	h.waitLayout(gen)

	// Verify only 1 pane remains
	c = h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("expected 1 pane after kill, got %d", len(c.Panes))
	}
}
